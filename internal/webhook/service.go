package webhook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/privacy"
	"github.com/bestruirui/octopus/internal/relay"
	"github.com/charmbracelet/log"
)

const (
	eventBuffer        = 256
	deliveryBuffer     = 256
	deliveryWorkers    = 4
	maxDeliveryAttempt = 3
)

type deliveryJob struct {
	endpoint model.WebhookEndpoint
	event    Event
}

type Service struct {
	ctx             context.Context
	cancel          context.CancelFunc
	healthUpdates   chan relay.HealthSnapshot
	initialHealth   []relay.HealthSnapshot
	events          chan Event
	deliveries      chan deliveryJob
	observeDone     chan struct{}
	manualActive    map[int]bool
	failoverActive  map[int]bool
	identities      map[int]uint64
	manualEpoch     map[int]uint64
	manualVersion   map[int]uint64
	recoveryVersion map[int]uint64
	wg              sync.WaitGroup
}

func Start() *Service {
	ctx, cancel := context.WithCancel(context.Background())
	snapshot, updates := relay.OpenHealthStream()
	service := &Service{
		ctx:            ctx,
		cancel:         cancel,
		healthUpdates:  updates,
		initialHealth:  snapshot,
		events:         make(chan Event, eventBuffer),
		deliveries:     make(chan deliveryJob, deliveryBuffer),
		observeDone:    make(chan struct{}),
		manualActive:   make(map[int]bool),
		failoverActive: make(map[int]bool),
	}
	service.wg.Add(2 + deliveryWorkers)
	go service.observe()
	go service.dispatch()
	for range deliveryWorkers {
		go service.deliver()
	}
	return service
}

func (service *Service) Close() {
	service.cancel()
	<-service.observeDone
	relay.CloseHealthStream(service.healthUpdates)
	service.wg.Wait()
}

func (service *Service) observe() {
	defer service.wg.Done()
	defer close(service.observeDone)
	for _, state := range service.initialHealth {
		service.observeHealth(state)
	}
	service.initialHealth = nil
	for {
		select {
		case <-service.ctx.Done():
			return
		case state, ok := <-service.healthUpdates:
			if !ok {
				if service.ctx.Err() != nil {
					return
				}
				service.reopenHealthStream()
				continue
			}
			service.observeHealth(state)
		}
	}
}

func (service *Service) reopenHealthStream() {
	snapshot, updates := relay.OpenHealthStream()
	service.healthUpdates = updates
	for _, state := range snapshot {
		service.observeHealth(state)
	}
}

func (service *Service) observeHealth(state relay.HealthSnapshot) {
	group, err := op.GroupGet(state.GroupID)
	if err != nil {
		return
	}
	if service.identities == nil {
		service.identities = make(map[int]uint64)
		service.manualEpoch = make(map[int]uint64)
		service.manualVersion = make(map[int]uint64)
		service.recoveryVersion = make(map[int]uint64)
	}
	if state.ConfigVersion < service.identities[group.ID] {
		return
	}
	if state.ConfigVersion != service.identities[group.ID] {
		service.identities[group.ID] = state.ConfigVersion
		service.manualActive[group.ID], service.failoverActive[group.ID] = false, false
		service.recoveryVersion[group.ID] = state.RecoveryVersion
	}
	if group.Mode == model.GroupModeManual {
		h, ok := state.Members[group.ActiveItemID]
		if !ok {
			return
		}
		if service.manualVersion[group.ID] != h.Version {
			service.manualVersion[group.ID] = h.Version
			service.manualActive[group.ID] = false
		}
		threshold := settingThreshold(model.SettingKeyWebhookManualFailureThreshold, 10)
		event := Event{Timestamp: time.Now().UTC().Format(time.RFC3339Nano), GroupID: group.ID, GroupName: group.Name, Mode: group.Mode, FailureStreak: h.FailureStreak, Threshold: threshold, RetryCount: h.RetryCount, LastError: redactConfiguredError(group, h.LastError)}
		for _, item := range group.Items {
			if item.ID == h.ItemID {
				event.CurrentChannel = item.ChannelName
			}
		}
		// A reconnect can skip recovery and land in the next failure epoch.
		if service.manualActive[group.ID] && h.Epoch > service.manualEpoch[group.ID] {
			event.Event = "recovery"
			if service.enqueue(event) {
				service.manualActive[group.ID] = false
			}
		}
		if h.FailureStreak >= threshold && !service.manualActive[group.ID] {
			event.Event = "failure"
			if service.enqueue(event) {
				service.manualActive[group.ID] = true
				service.manualEpoch[group.ID] = h.Epoch
			}
		}
		return
	}
	threshold := settingThreshold(model.SettingKeyWebhookFailoverChannelThreshold, 3)
	now := time.Now().UnixMilli()
	failedChannels := 0
	for _, deadline := range state.Cooldowns {
		if deadline > now {
			failedChannels++
		}
	}
	runtime := runtimeOfGroup(state.GroupID)
	// Health is published before request finalization, so the latest upstream
	// failure must come from this snapshot rather than a possibly stale request.
	var latest relay.MemberHealth
	for _, h := range state.Members {
		if h.LastFailureAt.After(latest.LastFailureAt) {
			latest = h
		}
	}
	if latest.LastError != "" {
		runtime.LastError, runtime.RetryCount = latest.LastError, latest.RetryCount
	}
	if failedChannels >= threshold && !service.failoverActive[group.ID] {
		event := Event{
			Event: "failure", Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			GroupID: group.ID, GroupName: group.Name, Mode: group.Mode,
			CurrentChannel: runtime.CurrentChannel, FailedChannels: failedChannels,
			Threshold: threshold, RetryCount: runtime.RetryCount, LastError: redactConfiguredError(group, runtime.LastError),
		}
		if service.enqueue(event) {
			service.failoverActive[group.ID] = true
			service.recoveryVersion[group.ID] = state.RecoveryVersion
		}
		return
	}
	if failedChannels < threshold && state.FailedChannelsAtRecovery < threshold && service.failoverActive[group.ID] && state.RecoveryVersion > service.recoveryVersion[group.ID] {
		event := Event{
			Event: "recovery", Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			GroupID: group.ID, GroupName: group.Name, Mode: group.Mode,
			CurrentChannel: runtime.CurrentChannel, FailedChannels: failedChannels,
			Threshold: threshold, RetryCount: runtime.RetryCount, LastError: redactConfiguredError(group, runtime.LastError),
		}
		if service.enqueue(event) {
			service.failoverActive[group.ID] = false
		}
	}
}

func (service *Service) enqueue(event Event) bool {
	select {
	case service.events <- event:
		return true
	default:
		log.Warnf("webhook notification queue is full; dropped %s event for group %d", event.Event, event.GroupID)
		return false
	}
}

func (service *Service) dispatch() {
	defer service.wg.Done()
	for {
		select {
		case <-service.ctx.Done():
			return
		case event := <-service.events:
			endpoints, err := op.WebhookEndpointListEnabled(service.ctx)
			if err != nil {
				log.Warnf("failed to read enabled webhook endpoints: %v", err)
				continue
			}
			for _, endpoint := range endpoints {
				select {
				case <-service.ctx.Done():
					return
				case service.deliveries <- deliveryJob{endpoint: endpoint, event: event}:
				default:
					log.Warnf("webhook delivery queue is full; dropped event for endpoint %d", endpoint.ID)
				}
			}
		}
	}
}

func (service *Service) deliver() {
	defer service.wg.Done()
	for {
		select {
		case <-service.ctx.Done():
			return
		case job := <-service.deliveries:
			result := sendWithRetry(service.ctx, job.endpoint, job.event)
			if !result.Success {
				log.Warnf("webhook endpoint %d delivery failed: %s", job.endpoint.ID, result.Error)
			}
		}
	}
}

func SendTest(ctx context.Context, endpoint model.WebhookEndpoint) SendResult {
	return sendWithRetry(ctx, endpoint, Event{Event: "test", Timestamp: time.Now().UTC().Format(time.RFC3339Nano)})
}

func sendWithRetry(ctx context.Context, endpoint model.WebhookEndpoint, event Event) SendResult {
	var result SendResult
	for attempt := 0; attempt < maxDeliveryAttempt; attempt++ {
		result = sendOnce(ctx, endpoint, event)
		if result.Success || !retryable(result) || attempt+1 == maxDeliveryAttempt {
			return result
		}
		delay := time.Duration(attempt+1) * 250 * time.Millisecond
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return SendResult{Error: "delivery canceled"}
		case <-timer.C:
		}
	}
	return result
}

func sendOnce(parent context.Context, endpoint model.WebhookEndpoint, event Event) SendResult {
	body, err := Render(endpoint.BodyTemplate, event)
	if err != nil {
		return SendResult{Error: "body template could not be rendered"}
	}
	if err := ValidateRenderedJSON(endpoint.ContentType, body); err != nil {
		return SendResult{Error: "rendered body is invalid for the configured content type"}
	}
	timeout := time.Duration(endpoint.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = DefaultTimeoutSeconds * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, endpoint.Method, endpoint.URL, bytes.NewReader(body))
	if err != nil {
		return SendResult{Error: "invalid request configuration"}
	}
	request.Header.Set("Content-Type", endpoint.ContentType)
	for key, value := range endpoint.Headers {
		request.Header.Set(key, value)
	}
	response, err := (&http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}).Do(request)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return SendResult{Error: "request timed out"}
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return SendResult{Error: "request timed out"}
		}
		return SendResult{Error: "request failed"}
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64*1024))
	result := SendResult{Success: response.StatusCode >= 200 && response.StatusCode < 300, StatusCode: response.StatusCode}
	if !result.Success {
		result.Error = fmt.Sprintf("endpoint returned HTTP %d", response.StatusCode)
	}
	return result
}

func retryable(result SendResult) bool {
	return result.StatusCode == http.StatusTooManyRequests || result.StatusCode >= 500 || (result.StatusCode == 0 && result.Error != "")
}

func settingThreshold(key model.SettingKey, fallback int) int {
	threshold, err := op.SettingGetInt(key)
	if err != nil || threshold < 1 {
		return fallback
	}
	return threshold
}

func runtimeOfGroup(groupID int) relay.GroupRuntime {
	for _, runtime := range relay.RuntimeSnapshot() {
		if runtime.GroupID == groupID {
			return runtime
		}
	}
	return relay.GroupRuntime{}
}

func redactConfiguredError(group model.Group, message string) string {
	if message == "" {
		return ""
	}
	redactor := privacy.NewDiagnostic()
	for _, item := range group.Items {
		if key, err := op.ChannelGrantKeyGet(item.ChannelGrantID); err == nil {
			redactor.Add(key.Key)
		}
		channel, err := op.ChannelGet(item.ChannelID)
		if err != nil {
			continue
		}
		redactor.AddURL(channel.BaseURL)
		redactor.AddURL(channel.ChannelProxy)
		for _, header := range channel.CustomHeader {
			redactor.Add(header.HeaderValue)
		}
	}
	return redactor.Text(message)
}
