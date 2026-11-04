package relay

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
)

func TestGroupCategoryChangesPreserveProbeAndMemberCooldown(t *testing.T) {
	for _, mode := range []model.GroupMode{model.GroupModeManual, model.GroupModeFailover} {
		t.Run(string(mode), func(t *testing.T) {
			engine, clock := useCooldownRuntimeClock(t)
			first := newTestChannel(t, testName(t, "category-first"), "http://127.0.0.1", "fixture-model-a")
			second := newTestChannel(t, testName(t, "category-second"), "http://127.0.0.1", "fixture-model-b")
			for _, grant := range []int{first, second} {
				id := channelIDOfGrant(t, grant)
				t.Cleanup(func() { _ = op.ChannelDel(id, context.Background()) })
			}
			config := testRelayConfig(2)
			config.MemberCooldownSeconds = 300
			group := manualWaitGroup(t, newTestGroup(t, mode, config, first, second))
			t.Cleanup(func() { _ = op.GroupDel(group.ID, context.Background()); ResetRouteState(group.ID) })
			SetRouteConfig(group)
			selection := pickGroupItemAt(group, 0, clock.Now())
			if mode == model.GroupModeFailover {
				// 用真实失败结算使第一个成员进入原有冷却，随后对第二个成员保留恢复探测。
				for range 2 {
					attempt := admitHealthAt(group, selection, clock.Now())
					if attempt == nil {
						t.Fatal("could not admit member failure")
					}
					settleHealthAt(group, selection, attempt, false, "fixture failure", 0, clock.Now())
					clock.advance(2 * time.Second)
					selection = pickGroupItemAt(group, 0, clock.Now())
				}
				if selection.Item.ChannelGrantID != second || len(RouteStateOf(group).Cooldowns) != 1 {
					t.Fatal("fixture did not retain failed first member cooldown")
				}
			}
			target := selection.Item.ChannelGrantID
			channelID := channelIDOfGrant(t, target)
			setRuntimeCooldownRules(t, channelID,
				model.CooldownRule{ErrorCode: "429", CooldownSeconds: 300, Scope: model.CooldownScopeModel},
				model.CooldownRule{Keyword: "quota", CooldownSeconds: 400, Scope: model.CooldownScopeChannel})
			attempt := admitHealthAt(group, selection, clock.Now())
			if attempt == nil {
				t.Fatal("could not admit initial recovery failure")
			}
			settleHealthAt(group, selection, attempt, false, "fixture recoverable failure", 0, clock.Now())
			clock.advance(2 * time.Second)
			selection = pickGroupItemAt(group, 0, clock.Now())
			probe := admitHealthAt(group, selection, clock.Now())
			if probe == nil || !probe.probe {
				t.Fatal("fixture did not acquire a recovery probe")
			}
			recordRuntimeCooldown(t, engine, target)
			beforeRoute := RouteStateOf(group)
			beforeHits := engine.Snapshot(channelID)
			beforeConfig, err := op.ChannelRelaySnapshot(target)
			if err != nil || len(beforeHits) != 2 {
				t.Fatalf("fixture rules missing: %v", err)
			}
			routeMu.Lock()
			beforeHealth := healthSnapshotLocked(routes[group.ID])
			identity, generation, probeToken := routes[group.ID].identity, routes[group.ID].generation, routes[group.ID].probeToken
			routeMu.Unlock()
			category, err := op.GroupCategoryCreate(fmt.Sprintf("W08 Relay %d", group.ID), t.Context())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = op.GroupCategoryDelete(category.ID, context.Background()) })

			// 状态消费者与分类写入同时运行；读侧只取不可变缓存副本，不进入 op 写锁。
			stop := make(chan struct{})
			var readers sync.WaitGroup
			for range 2 {
				readers.Add(1)
				go func() {
					defer readers.Done()
					for {
						select {
						case <-stop:
							return
						default:
						}
						current, err := op.GroupGet(group.ID)
						if err != nil {
							t.Error(err)
							return
						}
						_ = RouteStateOf(current)
						_ = op.GroupCategoryList()
					}
				}()
			}
			var stopOnce sync.Once
			stopReaders := func() { stopOnce.Do(func() { close(stop) }); readers.Wait() }
			t.Cleanup(stopReaders)
			for i := range 8 {
				ref := &category.ID
				if i%2 == 0 {
					ref = nil
				}
				if _, err := op.GroupUpdate(group.ID, &model.GroupUpdateRequest{CategoryID: model.GroupCategoryReference{Present: true, ID: ref}}, t.Context()); err != nil {
					t.Fatal(err)
				}
				if _, err := op.GroupCategoryUpdate(category.ID, fmt.Sprintf("W08 Relay %d Step %d", group.ID, i), t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := op.GroupCategoryDelete(category.ID, t.Context()); err != nil {
				t.Fatal(err)
			}
			stopReaders()
			current, err := op.GroupGet(group.ID)
			if err != nil || current.CategoryID != nil || current.MetadataVersion == group.MetadataVersion || current.ActiveItemID != group.ActiveItemID || !reflect.DeepEqual(current.Items, group.Items) || current.RelayConfig != group.RelayConfig {
				t.Fatalf("category writes changed group routing data: %v", err)
			}
			if !reflect.DeepEqual(RouteStateOf(current), beforeRoute) || !reflect.DeepEqual(engine.Snapshot(channelID), beforeHits) {
				t.Fatal("category writes changed route versions or member/shared cooldowns")
			}
			afterConfig, _ := op.ChannelRelaySnapshot(target)
			routeMu.Lock()
			afterHealth := healthSnapshotLocked(routes[group.ID])
			preserved := routes[group.ID].identity == identity && routes[group.ID].generation == generation && routes[group.ID].probeToken == probeToken && probe.member.probe == probe
			routeMu.Unlock()
			if !preserved || afterConfig.Generation != beforeConfig.Generation || !reflect.DeepEqual(afterHealth, beforeHealth) || !healthAttemptCurrent(group, selection, probe) {
				t.Fatal("category writes changed health failure count, probe owner or attempt identity")
			}
			settleHealthAt(group, selection, probe, true, "", 0, clock.Now())
			if probe.member.FailureStreak != 0 || len(engine.Snapshot(channelID)) != 2 {
				t.Fatal("old successful probe could not settle or cleared shared rules")
			}
		})
	}
}
