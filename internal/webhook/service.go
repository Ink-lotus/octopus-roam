package webhook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
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
	ctx            context.Context
	cancel         context.CancelFunc
	requestUpdates chan relay.RequestState
	routeUpdates   chan relay.RouteState
	events         chan Event
	deliveries     chan deliveryJob
	observeDone    chan struct{}
	manualActive   map[int]bool
	failoverActive map[int]bool
	wg             sync.WaitGroup
}

func Start() *Service {
	ctx, cancel := context.WithCancel(context.Background())
	_, requestUpdates := relay.OpenRequestStream()
	service := &Service{
		ctx:            ctx,
		cancel:         cancel,
		requestUpdates: requestUpdates,
		routeUpdates:   relay.OpenRouteStream(),
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
	relay.CloseRequestStream(service.requestUpdates)
	relay.CloseRouteStream(service.routeUpdates)
	service.wg.Wait()
}

func (service *Service) observe() {
	defer service.wg.Done()
	defer close(service.observeDone)
	for {
		select {
		case <-service.ctx.Done():
			return
		case request, ok := <-service.requestUpdates:
			if !ok {
				if service.ctx.Err() != nil {
					return
				}
				service.reopenRequestStream()
				continue
			}
			service.observeRequest(request)
		case state, ok := <-service.routeUpdates:
			if !ok {
				if service.ctx.Err() != nil {
					return
				}
				service.reopenRouteStream()
				continue
			}
			service.observeRoute(state)
		}
	}
}

func (service *Service) reopenRequestStream() {
	snapshot, updates := relay.OpenRequestStream()
	service.requestUpdates = updates
	terminalSeen := make(map[int]bool)
	for _, request := range snapshot {
		if request.Status == relay.StatusRunning || request.Status == relay.StatusCommitted {
			service.observeRequest(request)
			continue
		}
		if terminalSeen[request.GroupID] {
			continue
		}
		terminalSeen[request.GroupID] = true
		if request.Status == relay.StatusSuccess || request.ErrorCount >= settingThreshold(model.SettingKeyWebhookManualFailureThreshold, 10) {
			service.observeRequest(request)
		}
	}
}

func (service *Service) reopenRouteStream() {
	service.routeUpdates = relay.OpenRouteStream()
	for _, group := range op.GroupList() {
		if group.Mode == model.GroupModeFailover {
			service.observeRoute(relay.RouteStateOf(group))
		}
	}
}

func (service *Service) observeRequest(request relay.RequestState) {
	group, err := op.GroupGet(request.GroupID)
	if err != nil || group.Mode != model.GroupModeManual {
		return
	}
	threshold := settingThreshold(model.SettingKeyWebhookManualFailureThreshold, 10)
	if request.ChannelStatus == relay.ChannelStatusError && request.ErrorCount >= threshold && !service.manualActive[group.ID] {
		event := eventFromRequest("failure", group, request, threshold)
		if service.enqueue(event) {
			service.manualActive[group.ID] = true
		}
		return
	}
	if request.Status == relay.StatusSuccess && service.manualActive[group.ID] {
		if service.enqueue(eventFromRequest("recovery", group, request, threshold)) {
			service.manualActive[group.ID] = false
		}
	}
}

func (service *Service) observeRoute(state relay.RouteState) {
	group, err := op.GroupGet(state.GroupID)
	if err != nil || group.Mode != model.GroupModeFailover {
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
	if failedChannels >= threshold && !service.failoverActive[group.ID] {
		event := Event{
			Event: "failure", Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			GroupID: group.ID, GroupName: group.Name, Mode: group.Mode,
			CurrentChannel: runtime.CurrentChannel, FailedChannels: failedChannels,
			Threshold: threshold, RetryCount: runtime.RetryCount, LastError: redactConfiguredError(group, runtime.LastError),
		}
		if service.enqueue(event) {
			service.failoverActive[group.ID] = true
		}
		return
	}
	if failedChannels < threshold && service.failoverActive[group.ID] {
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

func eventFromRequest(eventName string, group model.Group, request relay.RequestState, threshold int) Event {
	return Event{
		Event: eventName, Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		GroupID: group.ID, GroupName: group.Name, Mode: group.Mode,
		CurrentChannel: request.TargetChannel, FailureStreak: request.ErrorCount,
		Threshold: threshold, RetryCount: request.RetryCount, LastError: redactConfiguredError(group, request.LastError),
	}
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
	secrets := make([]string, 0, len(group.Items)*4)
	for _, item := range group.Items {
		if key, err := op.ChannelGrantKeyGet(item.ChannelGrantID); err == nil {
			secrets = append(secrets, key.Key)
		}
		channel, err := op.ChannelGet(item.ChannelID)
		if err != nil {
			continue
		}
		secrets = append(secrets, channel.BaseURL, channel.ChannelProxy)
		for _, header := range channel.CustomHeader {
			secrets = append(secrets, header.HeaderValue)
		}
	}
	filtered := secrets[:0]
	for _, secret := range secrets {
		if len(secret) >= 4 {
			filtered = append(filtered, secret)
		}
	}
	sort.Slice(filtered, func(i, j int) bool { return len(filtered[i]) > len(filtered[j]) })
	replacements := make([]string, 0, len(filtered)*2)
	for _, secret := range filtered {
		replacements = append(replacements, secret, "[redacted]")
	}
	if len(replacements) == 0 {
		return message
	}
	return strings.NewReplacer(replacements...).Replace(message)
}
