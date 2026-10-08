package relay

import (
	"crypto/sha256"
	"encoding/json"
	"maps"
	"math"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
)

// HealthSnapshot is independent of request history. All fields are value copies;
// registration and the initial snapshot share routeMu with producers.
type HealthSnapshot struct {
	GroupID                  int
	ConfigVersion            uint64
	RecoveryVersion          uint64
	RecoveryBarrier          uint64 // 冷却结束或人工选择前的成功不能作为本次恢复依据。
	FailedChannelsAtRecovery int    // 在最近一次完整成功时仍有效的冷却数量。
	CurrentItemID            int
	Cooldowns                map[int]int64
	Members                  map[int]MemberHealth
}

type MemberHealth struct {
	ItemID         int
	Version        uint64
	Epoch          uint64
	FailureStreak  int
	LastError      string
	LastFailureAt  time.Time
	RetryNotBefore time.Time
	RetryCount     int
}

type memberHealth struct {
	MemberHealth
	identity        [32]byte
	probe           *healthAttempt
	recoveryPending bool
}

// Qualified recovery and failover cooldown completion end an epoch. The latter
// renews the budget while recoveryPending still requires a real recovery probe.
type healthAttempt struct {
	route   *routeRuntime
	member  *memberHealth
	epoch   uint64
	probe   bool
	settled bool
}

var healthStreams = make(map[chan HealthSnapshot]struct{}) // guarded by routeMu

// Only a digest of effective outbound configuration is retained. Capacity and
// discovery preferences do not change the identity of an upstream attempt.
func memberIdentity(item model.GroupItem) [32]byte {
	channel, _ := op.ChannelGet(item.ChannelID)
	grant, _ := op.ChannelGrantGet(item.ChannelGrantID)
	return outboundIdentity(channel, grant)
}

func outboundIdentity(channel model.Channel, grant model.ChannelGrant) [32]byte {
	config := channel.ChannelConfig
	config.MaxConcurrency = 0
	config.FollowGlobalAutoUpdate = nil
	config.MatchRegex = ""
	config.Name = ""
	var key model.ChannelKeyConfig
	var modelName string
	if grant.ChannelKey != nil {
		key = grant.ChannelKey.ChannelKeyConfig
	}
	if grant.ChannelModel != nil {
		modelName = grant.ChannelModel.Name
	}
	data, _ := json.Marshal(struct {
		Config         model.ChannelConfig
		Key            model.ChannelKeyConfig
		Model          string
		KeyID, ModelID int
		Protocols      model.Protocol
	}{config, key, modelName, grant.ChannelKeyID, grant.ChannelModelID, grant.Protocols})
	return sha256.Sum256(data)
}

func memberHealthLocked(route *routeRuntime, item model.GroupItem) *memberHealth {
	if route.health == nil {
		route.health = make(map[int]*memberHealth)
	}
	identity := memberIdentity(item)
	h := route.health[item.ID]
	if h == nil || h.identity != identity {
		h = &memberHealth{MemberHealth: MemberHealth{ItemID: item.ID, Version: routeVersion.Add(1)}, identity: identity}
		route.health[item.ID] = h
		hadCooldown := route.state.Cooldowns[item.ID] != 0
		delete(route.state.Cooldowns, item.ID)
		delete(route.state.CooldownVersions, item.ID)
		delete(route.probeRetryAfter, item.ID)
		if hadCooldown {
			scheduleRouteCooldownLocked(route, time.Now())
		}
		signalRouteLocked(route)
		publishHealthLocked(route)
	}
	return h
}

func healthWaitLocked(route *routeRuntime, item model.GroupItem, now time.Time) (time.Time, bool) {
	expireRouteCooldownsLocked(route, now)
	h := memberHealthLocked(route, item)
	// Cooldown completion clears this cycle's backoff, even if that backoff would
	// otherwise expire later. Ordinary backoff never resets the failure streak.
	if cooldown := route.state.Cooldowns[item.ID]; cooldown > now.UnixMilli() {
		return time.UnixMilli(cooldown), true
	}
	if h.probe != nil {
		return time.Time{}, true
	}
	deadline := h.RetryNotBefore
	if deadline.After(now) {
		return deadline, true
	}
	return time.Time{}, false
}

// Called after capacity acquisition, immediately before creating the outbound
// attempt. No lock is held over transport, capacity waiting, or request telemetry.
func admitHealth(group model.Group, selection routeSelection, channel model.Channel, grant model.ChannelGrant) *healthAttempt {
	return admitHealthAt(group, selection, time.Now(), outboundIdentity(channel, grant))
}

func admitHealthAt(group model.Group, selection routeSelection, now time.Time, expected ...[32]byte) *healthAttempt {
	routeMu.Lock()
	defer routeMu.Unlock()
	route := routes[group.ID]
	if route == nil || route.generation != selection.Generation || !sameRouteConfig(routeConfigs[group.ID], routeConfigSignatureOf(group)) {
		return nil
	}
	if _, waiting := healthWaitLocked(route, selection.Item, now); waiting {
		return nil
	}
	if deadline := route.state.Cooldowns[selection.Item.ID]; deadline > now.UnixMilli() {
		return nil
	}
	h := route.health[selection.Item.ID]
	if selection.MemberVersion != h.Version || selection.MemberEpoch != h.Epoch {
		return nil
	}
	if len(expected) > 0 && expected[0] != h.identity {
		return nil
	}
	a := &healthAttempt{route: route, member: h, epoch: h.Epoch, probe: h.recoveryPending || h.FailureStreak > 0}
	if a.probe {
		h.probe = a
	}
	return a
}

func healthAttemptCurrent(group model.Group, selection routeSelection, a *healthAttempt) bool {
	routeMu.Lock()
	defer routeMu.Unlock()
	route := routes[group.ID]
	return a != nil && route == a.route && route.generation == selection.Generation && route.health[selection.Item.ID] == a.member && a.member.Epoch == a.epoch
}

// settleHealth atomically records the result before releasing recovery ownership.
// success=false and failure="" means unknown/canceled/unsent, never a failure.
// failure must already have passed the request's privacy redactor.
func settleHealth(group model.Group, selection routeSelection, a *healthAttempt, success bool, failure string, retryCount int) time.Duration {
	return settleHealthAt(group, selection, a, success, failure, retryCount, time.Now())
}

func settleHealthAt(group model.Group, selection routeSelection, a *healthAttempt, success bool, failure string, retryCount int, now time.Time) time.Duration {
	if a == nil {
		return 0
	}
	routeMu.Lock()
	defer routeMu.Unlock()
	if a.settled {
		return 0
	}
	a.settled = true
	route := routes[group.ID]
	if route != a.route {
		return 0
	}
	expireRouteCooldownsLocked(route, now)
	h := memberHealthLocked(route, selection.Item)
	if h != a.member || h.Epoch != a.epoch {
		return 0
	}
	qualified := a.probe && h.probe == a
	if h.probe == a {
		h.probe = nil
	}
	if failure != "" {
		h.recoveryPending = true
		if h.FailureStreak < math.MaxInt {
			h.FailureStreak++
		}
		h.LastError, h.LastFailureAt, h.RetryCount = failure, now, retryCount
		deadline := now.Add(RetryDelay(group.RelayConfig, h.FailureStreak))
		if deadline.After(h.RetryNotBefore) {
			h.RetryNotBefore = deadline
		}
		if group.Mode == model.GroupModeFailover && h.FailureStreak >= group.RelayConfig.MemberMaxAttempts {
			selectionChanged := false
			cooldown := now.Add(secondsToDuration(group.RelayConfig.MemberCooldownSeconds))
			startMemberCooldownLocked(route, selection.Item.ID, cooldown, now)
			if route.state.CurrentItemID == selection.Item.ID {
				route.state.CurrentItemID = 0
				selectionChanged = true
			}
			if route.state.ProbeItemID == selection.Item.ID {
				route.state.ProbeItemID, route.probeToken = 0, 0
				selectionChanged = true
			}
			if selectionChanged {
				advanceRouteLocked(route)
			}
		}
	} else if success && ((!h.recoveryPending && h.FailureStreak == 0) || qualified) {
		h.RetryCount = retryCount
		if qualified {
			h.recoveryPending = false
			h.FailureStreak = 0
			h.RetryNotBefore = time.Time{}
			h.Epoch++
		}
		route.recoveryVersion = routeVersion.Add(1)
		if group.Mode == model.GroupModeFailover && route.generation == selection.Generation &&
			selection.ProbeToken != 0 && route.probeToken == selection.ProbeToken && route.state.ProbeItemID == selection.Item.ID {
			route.state.ProbeItemID, route.probeToken = 0, 0
			route.state.CurrentItemID = selection.Item.ID
			delete(route.state.Cooldowns, selection.Item.ID)
			delete(route.state.CooldownVersions, selection.Item.ID)
			advanceRouteLocked(route)
		}
		route.failedChannelsAtRecovery = 0
		for _, deadline := range route.state.Cooldowns {
			if deadline > now.UnixMilli() {
				route.failedChannelsAtRecovery++
			}
		}
	}
	signalRouteLocked(route)
	if group.Mode == model.GroupModeManual {
		publishHealthLocked(route)
	} else {
		publishRouteLocked(route)
	}
	deadline := h.RetryNotBefore
	if cooldown := route.state.Cooldowns[selection.Item.ID]; cooldown > now.UnixMilli() {
		deadline = time.UnixMilli(cooldown)
	}
	return max(0, deadline.Sub(now))
}

func healthSnapshotLocked(route *routeRuntime) HealthSnapshot {
	s := HealthSnapshot{GroupID: route.state.GroupID, ConfigVersion: route.identity, RecoveryVersion: route.recoveryVersion, CurrentItemID: route.state.CurrentItemID, Cooldowns: maps.Clone(route.state.Cooldowns), Members: make(map[int]MemberHealth)}
	s.FailedChannelsAtRecovery = route.failedChannelsAtRecovery
	s.RecoveryBarrier = route.recoveryBarrier
	for id, h := range route.health {
		s.Members[id] = h.MemberHealth
	}
	return s
}

func publishHealthLocked(route *routeRuntime) {
	for stream := range healthStreams {
		select {
		case stream <- healthSnapshotLocked(route):
		default:
			delete(healthStreams, stream)
			close(stream)
		}
	}
}

func OpenHealthStream() ([]HealthSnapshot, chan HealthSnapshot) {
	routeMu.Lock()
	defer routeMu.Unlock()
	for _, route := range routes {
		expireRouteCooldownsLocked(route, time.Now())
	}
	stream := make(chan HealthSnapshot, routeStreamBuffer)
	healthStreams[stream] = struct{}{}
	snapshot := make([]HealthSnapshot, 0, len(routes))
	for _, route := range routes {
		snapshot = append(snapshot, healthSnapshotLocked(route))
	}
	return snapshot, stream
}

func CloseHealthStream(stream chan HealthSnapshot) {
	routeMu.Lock()
	defer routeMu.Unlock()
	if _, ok := healthStreams[stream]; ok {
		delete(healthStreams, stream)
		close(stream)
	}
}
