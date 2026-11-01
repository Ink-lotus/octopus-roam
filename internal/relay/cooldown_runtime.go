package relay

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
)

var ruleCooldownEngine atomic.Pointer[CooldownEngine]

// These maps/streams share routeMu. The only nested reads are immutable op
// snapshots/cache reads and engine Observe/Snapshot; neither invokes callbacks.
var channelRuleStates = make(map[int]ChannelCooldownState)
var channelRuleStreams = make(map[chan ChannelCooldownState]struct{})
var emptyRuleStates = make(map[int][]CooldownHit)

type ChannelCooldownState struct {
	ChannelID     int           `json:"channel_id"`
	Version       string        `json:"version"`
	RuleCooldowns []CooldownHit `json:"rule_cooldowns"`
}

func init() {
	engine := NewCooldownEngine(nil, notifyChannelRuleCooldown)
	ruleCooldownEngine.Store(engine)
	if err := op.RegisterChannelCooldownPublisher(engine); err != nil {
		panic(err)
	}
}

func channelRuleStateLocked(id int, advance bool) ChannelCooldownState {
	hits := ruleCooldownEngine.Load().Snapshot(id)
	state, exists := channelRuleStates[id]
	if advance || !exists || !slices.Equal(state.RuleCooldowns, hits) {
		state = ChannelCooldownState{ChannelID: id, Version: publicRouteVersion(routeVersion.Add(1)), RuleCooldowns: hits}
		channelRuleStates[id] = state
	}
	state.RuleCooldowns = slices.Clone(state.RuleCooldowns)
	return state
}

func ChannelCooldownStateOf(id int) ChannelCooldownState {
	routeMu.Lock()
	defer routeMu.Unlock()
	return channelRuleStateLocked(id, false)
}

func OpenChannelCooldownStream() chan ChannelCooldownState {
	routeMu.Lock()
	defer routeMu.Unlock()
	stream := make(chan ChannelCooldownState, routeStreamBuffer)
	channelRuleStreams[stream] = struct{}{}
	return stream
}

func CloseChannelCooldownStream(stream chan ChannelCooldownState) {
	routeMu.Lock()
	defer routeMu.Unlock()
	if _, exists := channelRuleStreams[stream]; exists {
		delete(channelRuleStreams, stream)
		close(stream)
	}
}

// Engine mutations have released their lock, and op Configure/Remove callers
// have also released every write/publication lock before reaching this method.
// Pure rule notifications never advance selection/configuration generations or
// touch member health/probe/cooldown-cycle identities.
func notifyChannelRuleCooldown(id int) {
	routeMu.Lock()
	defer routeMu.Unlock()
	// GroupList takes cache read locks only. Read after acquiring routeMu so a
	// delayed callback cannot overwrite a newer route's membership projection.
	groups := op.GroupList()
	state := channelRuleStateLocked(id, true)
	for stream := range channelRuleStreams {
		message := state
		message.RuleCooldowns = slices.Clone(state.RuleCooldowns)
		select {
		case stream <- message:
		default:
			delete(channelRuleStreams, stream)
			close(stream)
		}
	}
	for _, group := range groups {
		references := slices.ContainsFunc(group.Items, func(item model.GroupItem) bool { return item.ChannelID == id })
		if !references {
			// Unvisited groups can lose their last member on channel deletion.
			// Retain the previously published projection long enough to clear it.
			references = slices.ContainsFunc(emptyRuleStates[group.ID], func(hit CooldownHit) bool { return hit.ChannelID == id })
		}
		if !references {
			// Deletion can remove the last reference before the deferred notice.
			if route := routes[group.ID]; route != nil {
				references = slices.ContainsFunc(route.ruleItems, func(item model.GroupItem) bool { return item.ChannelID == id })
			}
		}
		if !references {
			continue
		}
		if route := routes[group.ID]; route != nil {
			route.ruleItems = slices.Clone(group.Items)
			signalRouteLocked(route)
			publishRouteLocked(route)
		} else {
			// Project unvisited groups without creating a route or probe identity.
			routeStateVersions[group.ID] = routeVersion.Add(1)
			message := emptyRouteStateLocked(group)
			for stream := range routeStreams {
				select {
				case stream <- message:
				default:
					delete(routeStreams, stream)
					close(stream)
				}
			}
		}
	}
}

func ruleCooldownsForItemsLocked(items []model.GroupItem) []CooldownHit {
	models := make(map[int]map[string]struct{})
	for _, item := range items {
		if models[item.ChannelID] == nil {
			models[item.ChannelID] = make(map[string]struct{})
		}
		models[item.ChannelID][item.ModelName] = struct{}{}
	}
	engine := ruleCooldownEngine.Load()
	hits := []CooldownHit{}
	for id, names := range models {
		// One channel snapshot avoids mixing deadlines if Record extends a
		// shared rule between two Key references to the same upstream model.
		for _, hit := range engine.Snapshot(id) {
			if _, referenced := names[hit.ModelName]; referenced || hit.Scope == model.CooldownScopeChannel {
				hits = append(hits, hit)
			}
		}
	}
	slices.SortFunc(hits, func(a, b CooldownHit) int {
		if a.ChannelID != b.ChannelID {
			if a.ChannelID < b.ChannelID {
				return -1
			}
			return 1
		}
		if a.RuleIndex != b.RuleIndex {
			return a.RuleIndex - b.RuleIndex
		}
		return strings.Compare(a.ModelName, b.ModelName)
	})
	return hits
}

func ruleCooldownDeadline(item model.GroupItem) time.Time {
	return ruleCooldownEngine.Load().Observe(item.ChannelID, item.ModelName).Until
}

func cooldownWaitLocked(route *routeRuntime, item model.GroupItem, now time.Time) (time.Time, bool) {
	deadline, wait := healthWaitLocked(route, item, now)
	if ruleDeadline := ruleCooldownDeadline(item); !ruleDeadline.IsZero() {
		if ruleDeadline.After(deadline) {
			deadline = ruleDeadline
		}
		wait = true
	}
	return deadline, wait
}

var errCooldownSendChanged = errors.New("cooldown or outbound configuration changed before send")

type cooldownSendContextKey struct{}
type cooldownSendContext struct {
	config          op.ChannelRelayConfig
	token           CooldownToken
	groupID         int
	routeGeneration uint64
	routeConfig     routeConfigSignature
}

func bindCooldownSend(ctx context.Context, config op.ChannelRelayConfig, token CooldownToken, group model.Group, selection routeSelection) context.Context {
	ctx = withCooldownAttempt(ctx, token)
	return context.WithValue(ctx, cooldownSendContextKey{}, cooldownSendContext{config: config, token: token,
		groupID: group.ID, routeGeneration: selection.Generation, routeConfig: routeConfigSignatureOf(group)})
}

// Last check is immediately before trackingTransport marks the attempt sent.
// No lock spans the HTTP call. A subsequent change is an in-flight request;
// Record still rejects its stale generation. Standalone protocol tests without
// the production send context keep their existing send/extraction semantics.
func checkCooldownSend(ctx context.Context, capture *upstreamFailureCapture) error {
	attempt, bound := ctx.Value(cooldownSendContextKey{}).(cooldownSendContext)
	if !bound {
		return nil
	}
	if capture != nil {
		capture.mu.Lock()
		identity := capture.identity
		capture.mu.Unlock()
		if identity.ChannelID != attempt.token.channelID || identity.Generation != attempt.token.generation || identity.UpstreamModel != attempt.token.modelName {
			return errCooldownSendChanged
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// changed also signals ordinary health settlements and unrelated model
	// rules. Recheck the actual route identity so those wakeups cannot create
	// unsent rounds or repeatedly interrupt a healthy concurrent sender.
	routeMu.Lock()
	defer routeMu.Unlock()
	route := routes[attempt.groupID]
	if route == nil || route.generation != attempt.routeGeneration || !sameRouteConfig(routeConfigs[attempt.groupID], attempt.routeConfig) {
		return errCooldownSendChanged
	}
	current, err := op.ChannelRelaySnapshot(attempt.config.Grant.ID)
	if err != nil || !current.Channel.Enabled || current.Channel.ID != attempt.config.Channel.ID ||
		current.Generation != attempt.config.Generation || current.Grant.Protocols != attempt.config.Grant.Protocols ||
		current.Grant.ChannelKeyID != attempt.config.Grant.ChannelKeyID || current.Grant.ChannelModelID != attempt.config.Grant.ChannelModelID ||
		current.Grant.ChannelModel.Name != attempt.token.modelName {
		return errCooldownSendChanged
	}
	observation := attempt.token.engine.Observe(current.Channel.ID, attempt.token.modelName)
	if !observation.Configured || observation.Generation != attempt.token.generation || !observation.Until.IsZero() {
		return errCooldownSendChanged
	}
	return nil
}

func recordAttemptCooldown(ctx context.Context, token CooldownToken, sent bool, result *upstreamResponse, err error) {
	if !sent || token.engine == nil || ctx.Err() != nil {
		return
	}
	failure, ok := UpstreamFailureFromError(err)
	if !ok {
		failure, ok = result.upstreamFailure()
	}
	if ok {
		token.engine.Record(token, failure)
	}
}

// Ordinary HTTP 200 error objects can be returned without a Go error by a
// converter. Treat the retained source failure as failure before committing or
// settling success, without parsing client-facing/fallback text.
func ordinaryUpstreamFailure(result *upstreamResponse) error {
	failure, ok := result.upstreamFailure()
	if !ok {
		return nil
	}
	message := failure.Message
	if message == "" {
		message = "upstream error response"
	}
	return &upstreamFailureError{cause: errors.New(message), failure: failure}
}
