package relay

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

var routeTestGroupID atomic.Int64

func TestBusyProbesYieldAllCandidatesAndRetryWithoutChangingCooldowns(t *testing.T) {
	group := routeTestGroup(t, 31, 32)
	now := time.Now()
	old := now.Add(-2 * time.Minute)
	for range group.Items {
		selection := pickGroupItemAt(group, 0, old)
		recordRouteFailureAt(group, selection.Item.ID, 1, selection.Generation, 0, old)
	}
	before := RouteStateOf(group)
	first := pickGroupItemAt(group, 0, now)
	if first.ProbeToken == 0 {
		t.Fatal("first recovery probe missing")
	}
	deferRouteProbeForCapacity(group, first)
	second := pickGroupItemAt(group, 0, now)
	if second.Item.ID != 32 || second.ProbeToken == 0 {
		t.Fatalf("did not yield to next candidate: %+v", second)
	}
	deferRouteProbeForCapacity(group, second)
	wait := pickGroupItemAt(group, 0, now)
	if !wait.Wait || wait.WaitUntil.IsZero() || !wait.WaitUntil.After(now) || wait.WaitUntil.After(now.Add(time.Second)) {
		t.Fatalf("all busy probes should wait with a bounded retry: %+v", wait)
	}
	after := RouteStateOf(group)
	if after.ProbeItemID != 0 {
		t.Fatal("capacity wait retained probe")
	}
	for id, deadline := range before.Cooldowns {
		if after.Cooldowns[id] != deadline {
			t.Fatal("capacity shortage changed failure cooldown")
		}
	}
	retry := pickGroupItemAt(group, 0, now.Add(time.Second))
	if retry.Item.ID != 31 || retry.ProbeToken == 0 {
		t.Fatalf("probe never became eligible again: %+v", retry)
	}
	deferRouteProbeForCapacity(group, first) // 迟到的持有者不得释放新探测。
	held := pickGroupItemAt(group, retry.ProbeToken, now.Add(time.Second))
	if held.ProbeToken != retry.ProbeToken {
		t.Fatal("stale capacity report released new probe")
	}
	recordRouteSuccess(group, retry.Item.ID, retry.Generation, retry.ProbeToken)
}

func routeTestGroup(t *testing.T, itemIDs ...int) model.Group {
	t.Helper()
	groupID := 1000000 + int(routeTestGroupID.Add(1))
	group := model.Group{
		ID:          groupID,
		Mode:        model.GroupModeFailover,
		RelayConfig: testRelayConfig(1),
		Items:       make([]model.GroupItem, len(itemIDs)),
	}
	for i, itemID := range itemIDs {
		group.Items[i] = model.GroupItem{
			ID: itemID, GroupID: groupID, ChannelGrantID: itemID + 100,
			Priority: i + 1, ChannelID: itemID + 200, Available: true,
		}
	}
	t.Cleanup(func() { ResetRouteState(groupID) })
	return group
}

func TestRouteStateOfMissingFailoverRouteDoesNotCreateRuntime(t *testing.T) {
	group := routeTestGroup(t, 1)
	blockedUntil := time.Now().Add(routeBlockRetention)

	routeMu.Lock()
	routeBlocked[group.ID] = blockedUntil
	versionBefore := routeVersion.Load()
	routeMu.Unlock()

	state := RouteStateOf(group)

	routeMu.Lock()
	route := routes[group.ID]
	gotBlockedUntil, stillBlocked := routeBlocked[group.ID]
	versionAfter := routeVersion.Load()
	routeMu.Unlock()

	if route != nil {
		t.Fatal("读取缺失路由状态创建了 routeRuntime")
	}
	if versionAfter != versionBefore {
		t.Fatalf("读取缺失路由状态改变了 routeVersion: before=%d after=%d", versionBefore, versionAfter)
	}
	if !stillBlocked || !gotBlockedUntil.Equal(blockedUntil) {
		t.Fatalf("读取缺失路由状态修改了删除墓碑: present=%t until=%v", stillBlocked, gotBlockedUntil)
	}
	if state.GroupID != group.ID || state.CurrentItemID != 0 || state.ProbeItemID != 0 || len(state.Cooldowns) != 0 {
		t.Fatalf("缺失路由状态应为空运行状态: %+v", state)
	}
}

func TestFailoverKeepsHealthyCurrentAfterAffinityAndPriorityRecovery(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	group := routeTestGroup(t, 11, 12)
	group.RelayConfig.MemberAffinitySeconds = 1 // 兼容字段不得影响路由。
	group.RelayConfig.MemberCooldownSeconds = 10

	high := pickGroupItemAt(group, 0, now)
	if high.Item.ID != 11 {
		t.Fatalf("首次成员 = %d, 期望 11", high.Item.ID)
	}
	if !recordRouteFailureAt(group, high.Item.ID, 1, high.Generation, 0, now) {
		t.Fatal("首成员失败后未耗尽尝试预算")
	}

	low := pickGroupItemAt(group, 0, now)
	if low.Item.ID != 12 {
		t.Fatalf("首成员冷却后选择 = %d, 期望 12", low.Item.ID)
	}
	recordRouteSuccess(group, low.Item.ID, low.Generation, 0)

	// 高优先级成员冷却到期后仍不抢占健康的当前成员。
	recovered := pickGroupItemAt(group, 0, now.Add(11*time.Second))
	if recovered.Item.ID != 12 || recovered.ProbeToken != 0 {
		t.Fatalf("高优先级恢复后选路 = member %d, probe %d; 期望继续使用 12", recovered.Item.ID, recovered.ProbeToken)
	}
}

func TestFailoverFailureRescansFromFirstAndSkipsCooling(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	group := routeTestGroup(t, 21, 22, 23)
	group.RelayConfig.MemberCooldownSeconds = 5

	first := pickGroupItemAt(group, 0, now)
	if !recordRouteFailureAt(group, first.Item.ID, 1, first.Generation, 0, now) {
		t.Fatal("首成员失败后未冷却")
	}
	second := pickGroupItemAt(group, 0, now)
	if second.Item.ID != 22 {
		t.Fatalf("首成员冷却时选择 = %d, 期望 22", second.Item.ID)
	}
	if !recordRouteFailureAt(group, second.Item.ID, 1, second.Generation, 0, now.Add(time.Second)) {
		t.Fatal("当前成员失败后未冷却")
	}

	third := pickGroupItemAt(group, 0, now.Add(2*time.Second))
	if third.Item.ID != 23 {
		t.Fatalf("两个成员冷却时选择 = %d, 期望 23", third.Item.ID)
	}
	if !recordRouteFailureAt(group, third.Item.ID, 1, third.Generation, 0, now.Add(7*time.Second)) {
		t.Fatal("当前第三成员失败后未冷却")
	}

	firstProbe := pickGroupItemAt(group, 0, now.Add(8*time.Second))
	if firstProbe.Item.ID != 21 || firstProbe.ProbeToken == 0 {
		t.Fatalf("冷却到期后首个探测 = member %d, probe %d; 期望探测首成员 21", firstProbe.Item.ID, firstProbe.ProbeToken)
	}
}

func TestFailoverWaitsForEarliestCooldownAndCancellation(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	group := routeTestGroup(t, 31, 32)
	group.RelayConfig.MemberCooldownSeconds = 10

	first := pickGroupItemAt(group, 0, now)
	recordRouteFailureAt(group, first.Item.ID, 1, first.Generation, 0, now)
	second := pickGroupItemAt(group, 0, now)
	recordRouteFailureAt(group, second.Item.ID, 1, second.Generation, 0, now.Add(time.Second))

	waiting := pickGroupItemAt(group, 0, now.Add(2*time.Second))
	wantDeadline := now.Add(10 * time.Second)
	if !waiting.Wait || waiting.WaitUntil.UnixMilli() != wantDeadline.UnixMilli() {
		t.Fatalf("等待结果 = wait %v, deadline %v; 期望最早截止 %v", waiting.Wait, waiting.WaitUntil, wantDeadline)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if waitRoute(ctx.Done(), waiting) {
		t.Fatal("已取消的冷却等待返回了继续选路")
	}
}

func TestStaleRouteConfigMismatchWaitsBeforeRetry(t *testing.T) {
	now := time.Now()
	group := routeTestGroup(t, 35)
	SetRouteConfig(group)

	updated := group
	updated.Items = append([]model.GroupItem(nil), group.Items...)
	updated.Items[0].Available = false

	selection := pickGroupItemAt(updated, 0, now)
	wantDeadline := now.Add(routeConfigMismatchRetryDelay)
	if !selection.Wait || selection.Changed != nil || !selection.WaitUntil.Equal(wantDeadline) {
		t.Fatalf("配置指纹失配结果 = %+v; 期望在 %v 后重试", selection, wantDeadline)
	}

	started := time.Now()
	if !waitRoute(context.Background().Done(), selection) {
		t.Fatal("配置指纹失配等待未恢复选路")
	}
	if elapsed := time.Since(started); elapsed < routeConfigMismatchRetryDelay/2 {
		t.Fatalf("配置指纹失配等待过短: %v", elapsed)
	}
}

func TestExpiredRouteBlockedTombstonesArePruned(t *testing.T) {
	now := time.Now()
	expiredID, retainedID := -1, -2

	routeMu.Lock()
	previousCleanupAt := routeBlockCleanupAt
	routeBlockCleanupAt = time.Time{}
	routeBlocked[expiredID] = now.Add(-time.Second)
	routeBlocked[retainedID] = now.Add(routeBlockRetention)
	cleanupRouteBlockedLocked(now)
	_, expiredStillPresent := routeBlocked[expiredID]
	_, retainedStillPresent := routeBlocked[retainedID]
	delete(routeBlocked, retainedID)
	routeBlockCleanupAt = previousCleanupAt
	routeMu.Unlock()

	if expiredStillPresent {
		t.Fatal("过期分组墓碑未被回收")
	}
	if !retainedStillPresent {
		t.Fatal("有效分组墓碑被提前回收")
	}
}

func TestRouteGenerationAndProbeLeaseRejectStaleResults(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	group := routeTestGroup(t, 41)
	group.RelayConfig.MemberCooldownSeconds = 1

	first := pickGroupItemAt(group, 0, now)
	if !recordRouteFailureAt(group, first.Item.ID, 1, first.Generation, 0, now) {
		t.Fatal("首成员失败后未冷却")
	}
	probeA := pickGroupItemAt(group, 0, now.Add(2*time.Second))
	if probeA.Item.ID != 41 || probeA.ProbeToken == 0 {
		t.Fatalf("首次恢复探测 = member %d, token %d", probeA.Item.ID, probeA.ProbeToken)
	}
	waiter := pickGroupItemAt(group, 0, now.Add(2*time.Second))
	if !waiter.Wait || waiter.Changed == nil {
		t.Fatalf("探测持有期间的并发请求未等待: %+v", waiter)
	}
	releaseRouteProbe(group, probeA.Item.ID, probeA.Generation, probeA.ProbeToken)
	select {
	case <-waiter.Changed:
	default:
		t.Fatal("释放探测后未唤醒并发等待请求")
	}

	probeB := pickGroupItemAt(group, 0, now.Add(2*time.Second))
	if probeB.Item.ID != probeA.Item.ID || probeB.ProbeToken == probeA.ProbeToken {
		t.Fatalf("再次探测未取得新令牌: A=%d B=%d", probeA.ProbeToken, probeB.ProbeToken)
	}
	releaseRouteProbe(group, probeA.Item.ID, probeA.Generation, probeA.ProbeToken)
	if state := RouteStateOf(group); state.ProbeItemID != 41 {
		t.Fatalf("旧探测释放了新持有者: ProbeItemID=%d", state.ProbeItemID)
	}
	if !recordRouteFailureAt(group, probeA.Item.ID, 1, probeA.Generation, probeA.ProbeToken, now.Add(3*time.Second)) {
		t.Fatal("旧探测失败回报未识别为过期")
	}
	if state := RouteStateOf(group); state.ProbeItemID != 41 {
		t.Fatalf("旧失败回报修改了新探测: ProbeItemID=%d", state.ProbeItemID)
	}

	newConfig := group
	newConfig.RelayConfig.MemberCooldownSeconds = 20
	ResetRouteStateForGroup(newConfig)
	staleAt := now.Add(4 * time.Second)
	stale := pickGroupItemAt(group, 0, staleAt)
	if !stale.Wait || stale.Changed != nil || !stale.WaitUntil.Equal(staleAt.Add(routeConfigMismatchRetryDelay)) {
		t.Fatalf("重置后旧配置未进入限频重试: %+v", stale)
	}

	fresh := pickGroupItemAt(newConfig, 0, now.Add(4*time.Second))
	if fresh.Item.ID != 41 || fresh.Generation == probeB.Generation {
		t.Fatalf("新配置选路 = member %d generation %d, 旧 generation %d", fresh.Item.ID, fresh.Generation, probeB.Generation)
	}
	if !recordRouteFailureAt(group, fresh.Item.ID, 1, probeB.Generation, probeB.ProbeToken, now.Add(4*time.Second)) {
		t.Fatal("重置前探测结果应判为过期")
	}
	recordRouteSuccess(group, probeB.Item.ID, probeB.Generation, probeB.ProbeToken)
	if state := RouteStateOf(newConfig); state.Cooldowns[41] != 0 || state.CurrentItemID != 41 {
		t.Fatalf("旧结果污染重置后的状态: %+v", state)
	}
}

func TestRouteConfigChangesClearPreviousRuntimeState(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	group := routeTestGroup(t, 61, 62)
	first := pickGroupItemAt(group, 0, now)
	if !recordRouteFailureAt(group, first.Item.ID, 1, first.Generation, 0, now) {
		t.Fatal("成员失败后未冷却")
	}
	if state := RouteStateOf(group); state.Cooldowns[61] == 0 {
		t.Fatal("预期测试初始状态包含冷却")
	}

	changedRelay := group
	changedRelay.RelayConfig.MemberCooldownSeconds++
	ResetRouteStateForGroup(changedRelay)
	if state := RouteStateOf(changedRelay); state.CurrentItemID != 0 || len(state.Cooldowns) != 0 || state.ProbeItemID != 0 {
		t.Fatalf("Relay 配置变更后未清理状态: %+v", state)
	}

	changedMembers := changedRelay
	changedMembers.Items = append([]model.GroupItem(nil), changedRelay.Items...)
	changedMembers.Items = changedMembers.Items[1:]
	ResetRouteStateForGroup(changedMembers)
	if state := RouteStateOf(changedMembers); state.CurrentItemID != 0 || len(state.Cooldowns) != 0 {
		t.Fatalf("成员列表变更后未清理状态: %+v", state)
	}

	manual := changedMembers
	manual.Mode = model.GroupModeManual
	manual.ActiveItemID = 62
	ResetRouteStateForGroup(manual)
	if state := RouteStateOf(manual); state.CurrentItemID != 62 || len(state.Cooldowns) != 0 {
		t.Fatalf("切到手动模式后仍残留故障转移状态: %+v", state)
	}
}

func TestUnavailableMembersWaitForAvailabilityNotification(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	group := routeTestGroup(t, 51)
	group.Items[0].Available = false

	waiting := pickGroupItemAt(group, 0, now)
	if !waiting.Wait || !waiting.WaitUntil.IsZero() {
		t.Fatalf("无可用成员应等待配置变更: %+v", waiting)
	}

	group.Items[0].Available = true
	NotifyRouteStateChanged(group)
	select {
	case <-waiting.Changed:
	default:
		t.Fatal("成员恢复后未唤醒等待请求")
	}
	selected := pickGroupItemAt(group, 0, now)
	if selected.Item.ID != 51 {
		t.Fatalf("成员恢复后选择 = %d, 期望 51", selected.Item.ID)
	}
}

func TestLegacyRelayConfigSnapshotMatchesNormalizedRuntimeConfig(t *testing.T) {
	group := routeTestGroup(t, 71)
	group.RelayConfig = model.GroupRelayConfig{
		MemberMaxAttempts:                     2,
		MemberRetryIntervalSeconds:            3,
		MemberNonStreamResponseTimeoutSeconds: 120,
		MemberStreamFirstEventTimeoutSeconds:  30,
		MemberCooldownSeconds:                 60,
		MemberAffinitySeconds:                 300,
	}
	normalized := group
	model.NormalizeGroupRelayConfig(&normalized.RelayConfig)
	SetRouteConfig(normalized)

	selection := pickGroupItemAt(group, 0, time.Now())
	if selection.Item.ID != 71 || selection.Wait {
		t.Fatalf("旧配置快照与补默认后的路由配置不匹配: %+v", selection)
	}
}

func TestRouteSignatureIgnoresInactiveModeParameters(t *testing.T) {
	group := routeTestGroup(t, 701, 702)
	group.Mode = model.GroupModeManual
	group.ActiveItemID = group.Items[0].ID
	before := routeConfigSignatureOf(group)
	group.RelayConfig.MemberMaxAttempts += 10
	group.RelayConfig.MemberCooldownSeconds += 100
	if !sameRouteConfig(before, routeConfigSignatureOf(group)) {
		t.Fatal("manual route changed for inactive failover fields")
	}
	group.Mode = model.GroupModeFailover
	before = routeConfigSignatureOf(group)
	group.RelayConfig.ManualMaxAttempts += 10
	if !sameRouteConfig(before, routeConfigSignatureOf(group)) {
		t.Fatal("failover route changed for inactive manual limit")
	}
}

func TestManualSelectionNotificationsSurviveChangeBeforeWait(t *testing.T) {
	for _, reset := range []bool{false, true} {
		name := "availability notification"
		if reset {
			name = "policy reset"
		}
		t.Run(name, func(t *testing.T) {
			group := routeTestGroup(t, 81)
			group.Mode = model.GroupModeManual
			group.ActiveItemID = 81
			selection := pickGroupItem(group, 0)
			if selection.Generation == 0 || selection.Changed == nil {
				t.Fatal("手动选择缺少版本或等待通知")
			}
			stream := OpenRouteStream()
			defer CloseRouteStream(stream)
			if reset {
				group.RelayConfig.MemberRetryIntervalSeconds++
				ResetRouteStateForGroup(group)
			} else {
				NotifyRouteStateChanged(group)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			request := &RequestState{}
			if !request.waitRetry(ctx, time.Minute, selection.Changed) {
				t.Fatal("开始退避前发生的通知丢失, 仍在等待旧间隔")
			}
			fresh := pickGroupItem(group, 0)
			if fresh.Generation == selection.Generation || fresh.Item.ID != 81 {
				t.Fatal("手动选择未更新版本或未保留人工指定成员")
			}
			select {
			case state := <-stream:
				t.Fatalf("手动等待通知不应发布空的故障转移路由状态: %+v", state)
			default:
			}
			state := RouteStateOf(group)
			if state.CurrentItemID != 81 || state.ProbeItemID != 0 || len(state.Cooldowns) != 0 {
				t.Fatalf("手动路由展示出现冷却或探测状态: %+v", state)
			}
		})
	}
}

func TestManualResetRejectsOldConfigWithoutRebuildingRoute(t *testing.T) {
	group := routeTestGroup(t, 91)
	group.Mode = model.GroupModeManual
	group.ActiveItemID = 91
	oldSelection := pickGroupItem(group, 0)
	updated := group
	updated.Mode = model.GroupModeFailover
	ResetRouteStateForGroup(updated)
	select {
	case <-oldSelection.Changed:
	default:
		t.Fatal("模式变更未通知旧手动请求")
	}
	stale := pickGroupItem(group, 0)
	if !stale.Wait || stale.WaitUntil.IsZero() {
		t.Fatal("旧手动快照没有进入短间隔重读")
	}
	routeMu.Lock()
	route := routes[group.ID]
	routeMu.Unlock()
	if route != nil {
		t.Fatal("旧手动快照重建了路由")
	}
	fresh := pickGroupItem(updated, 0)
	recordRouteFailure(group, 91, 100, oldSelection.Generation, 0)
	recordRouteSuccess(group, 91, oldSelection.Generation, 0)
	state := RouteStateOf(updated)
	if fresh.Generation == oldSelection.Generation || state.CurrentItemID != 91 || len(state.Cooldowns) != 0 {
		t.Fatalf("旧手动结果影响了新故障转移路由: %+v", state)
	}
}
