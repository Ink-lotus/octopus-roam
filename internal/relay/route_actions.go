package relay

import (
	"errors"
	"fmt"
	"maps"
	"strconv"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/model"
)

var ErrRouteConflict = errors.New("route state changed")

// Versions are strings on the wire: the process start distinguishes restarts,
// and the shared counter orders configuration, snapshots and cooldown cycles.
var routeProcessVersion = strconv.FormatInt(time.Now().UnixNano(), 10)
var routeConfigVersions = make(map[int]uint64) // guarded by routeMu
var routeStateVersions = make(map[int]uint64)  // includes empty states after reset

func publicRouteVersion(version uint64) string {
	return routeProcessVersion + ":" + strconv.FormatUint(version, 10)
}

func touchRouteConfigLocked(groupID int) {
	version := routeVersion.Add(1)
	routeConfigVersions[groupID], routeStateVersions[groupID] = version, version
	if route := routes[groupID]; route != nil {
		route.state.ConfigVersion = publicRouteVersion(version)
		route.state.Version = publicRouteVersion(version)
	}
}

func emptyRouteStateLocked(group model.Group) RouteState {
	state := RouteState{
		GroupID:          group.ID,
		Version:          publicRouteVersion(routeStateVersions[group.ID]),
		ConfigVersion:    publicRouteVersion(routeConfigVersions[group.ID]),
		SelectionVersion: publicRouteVersion(routeStateVersions[group.ID]),
		Cooldowns:        map[int]int64{}, CooldownVersions: map[int]string{},
	}
	if group.Mode == model.GroupModeManual {
		state.CurrentItemID = group.ActiveItemID
	}
	return state
}

func routeSnapshotLocked(route *routeRuntime) RouteState {
	state := route.state
	state.Cooldowns = maps.Clone(state.Cooldowns)
	state.CooldownVersions = maps.Clone(state.CooldownVersions)
	return state
}

// SelectRouteItem changes only the failover selection. Sent attempts retain
// their member health ownership; their old route/probe cannot reclaim selection.
func SelectRouteItem(group model.Group, itemID int, expectedVersion string) (RouteState, error) {
	return selectRouteItemAt(group, itemID, expectedVersion, time.Now())
}

func selectRouteItemAt(group model.Group, itemID int, expectedVersion string, now time.Time) (RouteState, error) {
	routeMu.Lock()
	defer routeMu.Unlock()
	if err := validateRouteActionLocked(group, itemID); err != nil {
		return RouteState{}, err
	}
	state := emptyRouteStateLocked(group)
	if route := routes[group.ID]; route != nil {
		expireRouteCooldownsLocked(route, now)
		state = route.state
	}
	if expectedVersion == "" || state.SelectionVersion != expectedVersion {
		return RouteState{}, fmt.Errorf("%w: selection is out of date", ErrRouteConflict)
	}
	if state.Cooldowns[itemID] > now.UnixMilli() {
		return RouteState{}, fmt.Errorf("%w: member is cooling down", ErrRouteConflict)
	}
	route := ensureRouteLocked(group)
	if route.state.CurrentItemID == itemID {
		return routeSnapshotLocked(route), nil
	}
	route.state.CurrentItemID = itemID
	route.state.ProbeItemID, route.probeToken = 0, 0
	route.recoveryBarrier = routeVersion.Add(1)
	advanceRouteLocked(route)
	signalRouteLocked(route)
	publishRouteLocked(route)
	return routeSnapshotLocked(route), nil
}

// ReleaseRouteCooldown requires the token captured when confirmation opened,
// not a later snapshot's token. Releasing never actively selects the member.
func ReleaseRouteCooldown(group model.Group, itemID int, expectedVersion string) (RouteState, error) {
	return releaseRouteCooldownAt(group, itemID, expectedVersion, time.Now())
}

func releaseRouteCooldownAt(group model.Group, itemID int, expectedVersion string, now time.Time) (RouteState, error) {
	routeMu.Lock()
	defer routeMu.Unlock()
	if err := validateRouteActionLocked(group, itemID); err != nil {
		return RouteState{}, err
	}
	route := routes[group.ID]
	if route == nil {
		return RouteState{}, fmt.Errorf("%w: cooldown no longer exists", ErrRouteConflict)
	}
	expireRouteCooldownsLocked(route, now)
	if !finishMemberCooldownLocked(route, itemID, expectedVersion) {
		return RouteState{}, fmt.Errorf("%w: cooldown is out of date", ErrRouteConflict)
	}
	scheduleRouteCooldownLocked(route, now)
	signalRouteLocked(route)
	publishRouteLocked(route)
	return routeSnapshotLocked(route), nil
}

func validateRouteActionLocked(group model.Group, itemID int) error {
	if group.Mode != model.GroupModeFailover {
		return fmt.Errorf("%w: group is not in failover mode", ErrRouteConflict)
	}
	if _, blocked := routeBlocked[group.ID]; blocked {
		return fmt.Errorf("%w: group was removed or reset", ErrRouteConflict)
	}
	if expected, exists := routeConfigs[group.ID]; exists && !sameRouteConfig(expected, routeConfigSignatureOf(group)) {
		return fmt.Errorf("%w: group configuration is out of date", ErrRouteConflict)
	}
	if _, available := availableItemOf(group, itemID); !available {
		return fmt.Errorf("%w: member is unavailable or no longer belongs to this group", ErrRouteConflict)
	}
	return nil
}

func startMemberCooldownLocked(route *routeRuntime, itemID int, deadline, now time.Time) {
	if deadline.UnixMilli() > route.state.Cooldowns[itemID] {
		route.state.Cooldowns[itemID] = deadline.UnixMilli()
		route.state.CooldownVersions[itemID] = publicRouteVersion(routeVersion.Add(1))
	}
	if h := route.health[itemID]; h != nil {
		h.recoveryPending = true
	}
	scheduleRouteCooldownLocked(route, now)
}

// Both expiry and release end exactly one cooldown. Epoch invalidates old
// results and request budgets without resetting another member's attempts.
func finishMemberCooldownLocked(route *routeRuntime, itemID int, version string) bool {
	if version == "" || route.state.CooldownVersions[itemID] != version {
		return false
	}
	delete(route.state.Cooldowns, itemID)
	delete(route.state.CooldownVersions, itemID)
	delete(route.probeRetryAfter, itemID)
	if h := route.health[itemID]; h != nil {
		h.Epoch++
		h.FailureStreak = 0
		h.RetryNotBefore = time.Time{}
		h.probe = nil
		h.recoveryPending = true
	}
	if route.state.ProbeItemID == itemID {
		route.state.ProbeItemID, route.probeToken = 0, 0
		advanceRouteLocked(route)
	}
	// A previous success cannot be reused to announce this cooldown's recovery.
	route.recoveryBarrier = routeVersion.Add(1)
	return true
}

func expireRouteCooldownsLocked(route *routeRuntime, now time.Time) {
	changed := false
	for itemID, deadline := range route.state.Cooldowns {
		if deadline <= now.UnixMilli() {
			changed = finishMemberCooldownLocked(route, itemID, route.state.CooldownVersions[itemID]) || changed
		}
	}
	if changed {
		scheduleRouteCooldownLocked(route, now)
		signalRouteLocked(route)
		publishRouteLocked(route)
	}
}

func stopRouteCooldownLocked(route *routeRuntime) {
	route.cooldownSchedule++
	if route.cooldownTimer != nil {
		route.cooldownTimer.Stop()
		route.cooldownTimer = nil
	}
}

func scheduleRouteCooldownLocked(route *routeRuntime, now time.Time) {
	stopRouteCooldownLocked(route)
	var earliest int64
	for _, deadline := range route.state.Cooldowns {
		if earliest == 0 || deadline < earliest {
			earliest = deadline
		}
	}
	if earliest == 0 {
		return
	}
	schedule := route.cooldownSchedule
	route.cooldownTimer = time.AfterFunc(max(0, time.UnixMilli(earliest).Sub(now)), func() {
		expireRouteTimerAt(route, schedule, time.Now())
	})
}

func expireRouteTimerAt(route *routeRuntime, schedule uint64, now time.Time) {
	routeMu.Lock()
	defer routeMu.Unlock()
	if routes[route.state.GroupID] != route || route.cooldownSchedule != schedule {
		return
	}
	expireRouteCooldownsLocked(route, now)
}
