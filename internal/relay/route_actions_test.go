package relay

import (
	"errors"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

func TestCooldownCompletionRenewsHealthWithoutPreemptingCurrent(t *testing.T) {
	for _, ending := range []string{"expiry", "release"} {
		t.Run(ending, func(t *testing.T) {
			group := routeTestGroup(t, 8101, 8102)
			group.RelayConfig.MemberMaxAttempts = 2
			group.RelayConfig.MemberCooldownSeconds = 2
			group.RelayConfig.RetryMode = model.RetryModeExponential
			group.RelayConfig.RetryInitialIntervalSeconds = 10
			now := time.Now().Truncate(time.Millisecond)
			first := pickGroupItemAt(group, 0, now)
			a := admitHealthAt(group, first, now)
			lateFailure, lateSuccess := admitHealthAt(group, first, now), admitHealthAt(group, first, now)
			settleHealthAt(group, first, a, false, "first failure", 1, now)
			retryAt := now.Add(10 * time.Second)
			retry := pickGroupItemAt(group, 0, retryAt)
			if h := healthOf(t, group.ID).Members[first.Item.ID]; h.FailureStreak != 1 {
				t.Fatal("ordinary backoff reset the failure streak")
			}
			settleHealthAt(group, retry, admitHealthAt(group, retry, retryAt), false, "second failure", 2, retryAt)
			cooling := RouteStateOf(group)
			token := cooling.CooldownVersions[first.Item.ID]
			routeMu.Lock()
			oldRoute := routes[group.ID]
			oldSchedule := oldRoute.cooldownSchedule
			routeMu.Unlock()
			waitingHealth := healthOf(t, group.ID).Members[first.Item.ID]
			if !waitingHealth.RetryNotBefore.After(time.UnixMilli(cooling.Cooldowns[first.Item.ID])) {
				t.Fatal("fixture must have backoff later than cooldown")
			}
			backup := pickGroupItemAt(group, 0, retryAt)
			if backup.Item.ID != group.Items[1].ID {
				t.Fatal("did not fail over")
			}
			if _, err := selectRouteItemAt(group, backup.Item.ID, cooling.SelectionVersion, retryAt); !errors.Is(err, ErrRouteConflict) {
				t.Fatal("an automatic selection reused the previous selection version")
			}
			endAt := retryAt.Add(2 * time.Second)
			if ending == "release" {
				endAt = retryAt.Add(time.Second)
				if _, err := releaseRouteCooldownAt(group, first.Item.ID, token, endAt); err != nil {
					t.Fatal(err)
				}
			}
			current := pickGroupItemAt(group, 0, endAt)
			h := healthOf(t, group.ID).Members[first.Item.ID]
			if h.FailureStreak != 0 || !h.RetryNotBefore.IsZero() || h.Epoch != waitingHealth.Epoch+1 {
				t.Fatalf("cooldown did not renew protection: %+v", h)
			}
			if h.LastError != "second failure" || h.RetryCount != 2 || h.LastFailureAt != waitingHealth.LastFailureAt {
				t.Fatal("cooldown completion removed failure history")
			}
			if current.Item.ID != backup.Item.ID || current.Generation != backup.Generation {
				t.Fatal("cooldown completion preempted or reset the healthy backup")
			}
			state := RouteStateOf(group)
			if state.Cooldowns[first.Item.ID] != 0 || state.CooldownVersions[first.Item.ID] != "" {
				t.Fatal("completed cooldown remained in snapshot")
			}
			if _, err := selectRouteItemAt(group, first.Item.ID, state.SelectionVersion, endAt); err != nil {
				t.Fatal(err)
			}
			selected := pickGroupItemAt(group, 0, endAt)
			probe := admitHealthAt(group, selected, endAt)
			if probe == nil || !probe.probe || admitHealthAt(group, selected, endAt) != nil {
				t.Fatal("reset lost exclusive recovery admission")
			}
			settleHealthAt(group, selected, probe, false, "", 0, endAt)
			probe = admitHealthAt(group, selected, endAt)
			if probe == nil || healthOf(t, group.ID).Members[first.Item.ID].FailureStreak != 0 {
				t.Fatal("canceled or unsent recovery attempt leaked ownership or counted a failure")
			}
			settleHealthAt(group, first, lateFailure, false, "old cycle failure", 1, endAt)
			settleHealthAt(group, first, lateSuccess, true, "", 1, endAt)
			if admitHealthAt(group, selected, endAt) != nil {
				t.Fatal("late result released the new probe")
			}
			settleHealthAt(group, selected, probe, false, "new first failure", 3, endAt)
			h = healthOf(t, group.ID).Members[first.Item.ID]
			if h.FailureStreak != 1 || !h.RetryNotBefore.Equal(endAt.Add(10*time.Second)) || RouteStateOf(group).Cooldowns[first.Item.ID] != 0 {
				t.Fatalf("new budget or exponential delay was not fresh: %+v", h)
			}
			if _, err := releaseRouteCooldownAt(group, first.Item.ID, token, endAt); !errors.Is(err, ErrRouteConflict) {
				t.Fatal("old release was accepted after completion")
			}
			pickGroupItemAt(group, 0, endAt)
			if healthOf(t, group.ID).Members[first.Item.ID].FailureStreak != 1 {
				t.Fatal("repeat read or release cleared new failures")
			}
			secondAt := endAt.Add(10 * time.Second)
			second := pickGroupItemAt(group, 0, secondAt)
			settleHealthAt(group, second, admitHealthAt(group, second, secondAt), false, "new second failure", 4, secondAt)
			newState := RouteStateOf(group)
			if newState.Cooldowns[first.Item.ID] == 0 || newState.CooldownVersions[first.Item.ID] == token {
				t.Fatal("second new failure did not start a new cooldown cycle")
			}
			expireRouteTimerAt(oldRoute, oldSchedule, secondAt.Add(time.Hour))
			if RouteStateOf(group).Cooldowns[first.Item.ID] != newState.Cooldowns[first.Item.ID] {
				t.Fatal("a previous timer expired the new cooldown cycle")
			}
			if _, err := releaseRouteCooldownAt(group, first.Item.ID, token, secondAt); !errors.Is(err, ErrRouteConflict) {
				t.Fatal("old confirmation released a new cooldown")
			}
			if pickGroupItemAt(group, 0, secondAt).Item.ID != backup.Item.ID {
				t.Fatal("manual selection disabled subsequent automatic failover")
			}
		})
	}
}

func TestCooldownReleaseIsScopedToOneGroupMember(t *testing.T) {
	group := routeTestGroup(t, 8501, 8502)
	other := routeTestGroup(t, 8503)
	group.Items[1].ChannelID = group.Items[0].ChannelID
	other.Items[0].ChannelID = group.Items[0].ChannelID
	other.Items[0].ChannelGrantID = group.Items[0].ChannelGrantID
	now := time.Now()
	for _, fixture := range []model.Group{group, group, other} {
		selection := pickGroupItemAt(fixture, 0, now)
		settleHealthAt(fixture, selection, admitHealthAt(fixture, selection, now), false, "member failure", 1, now)
	}
	before, otherBefore := RouteStateOf(group), RouteStateOf(other)
	if _, err := releaseRouteCooldownAt(group, group.Items[0].ID, before.CooldownVersions[group.Items[0].ID], now); err != nil {
		t.Fatal(err)
	}
	after := RouteStateOf(group)
	if after.CurrentItemID != 0 || after.Cooldowns[group.Items[1].ID] != before.Cooldowns[group.Items[1].ID] || RouteStateOf(other).Version != otherBefore.Version {
		t.Fatal("release changed another member, group or current selection")
	}
	if healthOf(t, group.ID).Members[group.Items[1].ID].FailureStreak != 1 || healthOf(t, other.ID).Members[other.Items[0].ID].FailureStreak != 1 {
		t.Fatal("release cleared another member's failure history")
	}
}

func TestCooldownDeadlinePrecedesOldBackoffAndThresholdOneRestarts(t *testing.T) {
	group := routeTestGroup(t, 8201)
	group.RelayConfig.MemberCooldownSeconds = 2
	group.RelayConfig.MemberRetryIntervalSeconds = 30
	now := time.Now().Truncate(time.Millisecond)
	first := pickGroupItemAt(group, 0, now)
	settleHealthAt(group, first, admitHealthAt(group, first, now), false, "failed", 0, now)
	wait := pickGroupItemAt(group, 0, now)
	if !wait.Wait || wait.WaitUntil.UnixMilli() != now.Add(2*time.Second).UnixMilli() {
		t.Fatalf("old backoff postponed cooldown expiry: %+v", wait)
	}
	now = now.Add(2 * time.Second)
	fresh := pickGroupItemAt(group, 0, now)
	if fresh.Wait || fresh.ProbeToken == 0 || fresh.MemberEpoch == first.MemberEpoch {
		t.Fatal("expiry did not reopen the same member's request budget")
	}
	settleHealthAt(group, fresh, admitHealthAt(group, fresh, now), false, "new failure", 1, now)
	if h := healthOf(t, group.ID).Members[first.Item.ID]; h.FailureStreak != 1 || RouteStateOf(group).Cooldowns[first.Item.ID] == 0 {
		t.Fatal("threshold one did not cool on its first new failure")
	}
}

func TestManualSelectionKeepsSentHealthAndRejectsStaleProbeCommit(t *testing.T) {
	group := routeTestGroup(t, 8301, 8302)
	group.RelayConfig.MemberMaxAttempts = 2
	now := time.Now()
	first := pickGroupItemAt(group, 0, now)
	inflight := admitHealthAt(group, first, now)
	state := RouteStateOf(group)
	selected, err := selectRouteItemAt(group, group.Items[1].ID, state.SelectionVersion, now)
	if err != nil {
		t.Fatal(err)
	}
	settleHealthAt(group, first, inflight, false, "sent before selection", 1, now)
	if healthOf(t, group.ID).Members[first.Item.ID].FailureStreak != 1 || RouteStateOf(group).CurrentItemID != group.Items[1].ID {
		t.Fatal("selection discarded an ordinary sent result or changed its target")
	}
	repeated, err := selectRouteItemAt(group, group.Items[1].ID, selected.SelectionVersion, now)
	if err != nil || repeated.SelectionVersion != selected.SelectionVersion {
		t.Fatal("repeat selection was not idempotent")
	}
	if _, err := selectRouteItemAt(group, first.Item.ID, state.SelectionVersion, now); !errors.Is(err, ErrRouteConflict) {
		t.Fatal("outdated selection was accepted")
	}

	// Create a recovery lease, then switch away before its successful result.
	group.RelayConfig.MemberMaxAttempts = 1
	ResetRouteStateForGroup(group)
	first = pickGroupItemAt(group, 0, now)
	recordRouteFailureAt(group, first.Item.ID, 1, first.Generation, 0, now)
	second := pickGroupItemAt(group, 0, now)
	recordRouteFailureAt(group, second.Item.ID, 1, second.Generation, 0, now)
	later := now.Add(time.Minute)
	probeSelection := pickGroupItemAt(group, 0, later)
	probe := admitHealthAt(group, probeSelection, later)
	state = RouteStateOf(group)
	if _, err := selectRouteItemAt(group, second.Item.ID, state.SelectionVersion, later); err != nil {
		t.Fatal(err)
	}
	settleHealthAt(group, probeSelection, probe, true, "", 0, later)
	if RouteStateOf(group).CurrentItemID != second.Item.ID {
		t.Fatal("old probe success reclaimed the manual selection")
	}
}

func TestCooldownTimerResetsWhileBackupIsIdleAndIgnoresRebuild(t *testing.T) {
	group := routeTestGroup(t, 8401, 8402)
	group.RelayConfig.MemberCooldownSeconds = 1
	now := time.Now()
	first := pickGroupItemAt(group, 0, now)
	settleHealthAt(group, first, admitHealthAt(group, first, now), false, "failed", 0, now)
	backup := pickGroupItemAt(group, 0, now)
	stream := OpenRouteStream()
	defer CloseRouteStream(stream)
	routeMu.Lock()
	oldRoute := routes[group.ID]
	oldSchedule := oldRoute.cooldownSchedule
	routeMu.Unlock()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case state := <-stream:
			if state.GroupID != group.ID || state.Cooldowns[first.Item.ID] != 0 {
				continue
			}
			if state.CurrentItemID != backup.Item.ID || healthOf(t, group.ID).Members[first.Item.ID].FailureStreak != 0 {
				t.Fatal("timer did not preserve backup and reset the expired member")
			}
			goto expired
		case <-deadline:
			t.Fatal("idle cooldown was not expired by its timer")
		}
	}
expired:
	before := RouteStateOf(group)
	ResetRouteStateForGroup(group)
	reset := RouteStateOf(group)
	if reset.Version == before.Version || reset.SelectionVersion == before.SelectionVersion {
		t.Fatal("reset reused a public state version")
	}
	expireRouteTimerAt(oldRoute, oldSchedule, now.Add(2*time.Minute))
	if after := RouteStateOf(group); after.Version != reset.Version || after.CurrentItemID != 0 {
		t.Fatal("old timer modified or recreated the reset runtime")
	}
	if _, err := SelectRouteItem(group, first.Item.ID, before.SelectionVersion); !errors.Is(err, ErrRouteConflict) {
		t.Fatal("pre-reset operation changed the rebuilt route")
	}
}
