package relay

import (
	"testing"
	"time"
)

func TestRuntimeSnapshotAndStreamUseNewestActiveRequest(t *testing.T) {
	groupID := -int(idSeq.Add(1))
	otherGroupID := groupID - 1
	activeOlder := &RequestState{
		ID:            idSeq.Add(1),
		Status:        StatusRunning,
		GroupID:       groupID,
		Model:         "runtime-group",
		TargetChannel: "older-channel",
		ChannelStatus: ChannelStatusNormal,
		Attempt:       1,
	}
	activeNewest := &RequestState{
		ID:            idSeq.Add(1),
		Status:        StatusRunning,
		GroupID:       groupID,
		Model:         "runtime-group",
		TargetChannel: "newest-channel",
		ChannelStatus: ChannelStatusWaiting,
		ErrorCount:    2,
		RetryCount:    1,
		Attempt:       2,
		AttemptLimit:  3,
		Retrying:      true,
		LastError:     "upstream unavailable",
		NextRetryAt:   time.Now().Add(time.Second).UnixMilli(),
	}
	finishedNewest := &RequestState{
		ID:            idSeq.Add(1),
		Status:        StatusFailed,
		GroupID:       groupID,
		Model:         "runtime-group",
		TargetChannel: "finished-channel",
		ChannelStatus: ChannelStatusError,
	}
	otherGroup := &RequestState{
		ID:            idSeq.Add(1),
		Status:        StatusSuccess,
		GroupID:       otherGroupID,
		Model:         "other-group",
		TargetChannel: "other-channel",
		ChannelStatus: ChannelStatusNormal,
	}

	requestsToRestore := []*RequestState{activeOlder, activeNewest, finishedNewest, otherGroup}
	mu.Lock()
	for _, request := range requestsToRestore {
		requests[request.ID] = request
	}
	mu.Unlock()
	defer func() {
		mu.Lock()
		defer mu.Unlock()
		for _, request := range requestsToRestore {
			delete(requests, request.ID)
		}
	}()

	initial, updates := OpenRuntimeStream()
	defer CloseRuntimeStream(updates)
	if got := RuntimeSnapshot(); !equalRuntimeSnapshots(got, initial) {
		t.Fatalf("runtime 流初始快照与读取接口不一致: snapshot=%+v stream=%+v", got, initial)
	}
	selected, ok := findRuntime(initial, groupID)
	if !ok {
		t.Fatalf("未找到分组 %d 的运行状态: %+v", groupID, initial)
	}
	if selected.RequestID != activeNewest.ID || selected.CurrentChannel != "newest-channel" {
		t.Fatalf("应优先选择最新的活跃请求, 并覆盖较新的终态请求: %+v", selected)
	}
	if selected.ErrorCount != 2 || selected.RetryCount != 1 || selected.Attempt != 2 || selected.AttemptLimit != 3 ||
		!selected.Retrying || selected.LastError != "upstream unavailable" || selected.NextRetryAt == 0 ||
		selected.ChannelStatus != ChannelStatusWaiting {
		t.Fatalf("汇总遥测字段不完整: %+v", selected)
	}
	if initial[0].GroupID != otherGroupID || initial[1].GroupID != groupID {
		t.Fatalf("快照应按分组 ID 升序排列: %+v", initial)
	}

	mu.Lock()
	activeNewest.TargetChannel = "updated-channel"
	activeNewest.Retrying = false
	activeNewest.NextRetryAt = 0
	publishRequestLocked(activeNewest)
	publishRuntimeLocked()
	mu.Unlock()

	select {
	case update, ok := <-updates:
		if !ok {
			t.Fatal("runtime 更新流在收到更新前关闭")
		}
		streamed, found := findRuntime(update, groupID)
		if !found || streamed.CurrentChannel != "updated-channel" || streamed.Retrying || streamed.NextRetryAt != 0 {
			t.Fatalf("流式更新没有复用当前聚合结果: %+v", update)
		}
		if got := RuntimeSnapshot(); !equalRuntimeSnapshots(got, update) {
			t.Fatalf("运行时快照与流式更新不一致: snapshot=%+v stream=%+v", got, update)
		}
	case <-time.After(time.Second):
		t.Fatal("等待 runtime 流更新超时")
	}
}

func findRuntime(snapshot []GroupRuntime, groupID int) (GroupRuntime, bool) {
	for _, runtime := range snapshot {
		if runtime.GroupID == groupID {
			return runtime, true
		}
	}
	return GroupRuntime{}, false
}

func equalRuntimeSnapshots(left, right []GroupRuntime) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
