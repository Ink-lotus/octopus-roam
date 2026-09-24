package relay

import "sort"

// GroupRuntime 是按分组汇总后选出的当前 Relay 遥测状态。
type GroupRuntime struct {
	GroupID        int           `json:"group_id"`
	GroupName      string        `json:"group_name"`
	RequestID      uint64        `json:"request_id"`
	Status         Status        `json:"status"`
	CurrentChannel string        `json:"current_channel"`
	ChannelStatus  ChannelStatus `json:"channel_status"`
	ErrorCount     int           `json:"error_count"`
	RetryCount     int           `json:"retry_count"`
	Attempt        int           `json:"attempt"`
	AttemptLimit   int           `json:"attempt_limit"`
	Retrying       bool          `json:"retrying"`
	LastError      string        `json:"last_error"`
	NextRetryAt    int64         `json:"next_retry_at"`
}

const runtimeStreamBuffer = 16 // 单个运行状态 SSE 连接的非阻塞消息缓冲容量。

// RuntimeSnapshot 返回当前 Relay 遥测汇总, 按分组 ID 升序排列。
func RuntimeSnapshot() []GroupRuntime {
	mu.Lock()
	defer mu.Unlock()
	return runtimeSnapshotLocked()
}

// OpenRuntimeStream 注册状态流, 并原子地返回初始快照与后续更新通道。
func OpenRuntimeStream() ([]GroupRuntime, chan []GroupRuntime) {
	mu.Lock()
	defer mu.Unlock()

	stream := make(chan []GroupRuntime, runtimeStreamBuffer)
	runtimeWatchers[stream] = struct{}{}
	return runtimeSnapshotLocked(), stream
}

// CloseRuntimeStream 注销并关闭运行状态更新通道。
func CloseRuntimeStream(stream chan []GroupRuntime) {
	mu.Lock()
	defer mu.Unlock()

	if _, exists := runtimeWatchers[stream]; exists {
		delete(runtimeWatchers, stream)
		close(stream)
	}
}

// runtimeSnapshotLocked 为每个分组选出一条请求: 活跃请求优先, 同类中请求 ID 最大者优先。
func runtimeSnapshotLocked() []GroupRuntime {
	selected := make(map[int]*RequestState)
	for _, request := range requests {
		current := selected[request.GroupID]
		if current == nil || preferRuntimeRequest(request, current) {
			selected[request.GroupID] = request
		}
	}

	groupIDs := make([]int, 0, len(selected))
	for groupID := range selected {
		groupIDs = append(groupIDs, groupID)
	}
	sort.Ints(groupIDs)

	snapshot := make([]GroupRuntime, 0, len(groupIDs))
	for _, groupID := range groupIDs {
		request := selected[groupID]
		snapshot = append(snapshot, GroupRuntime{
			GroupID:        request.GroupID,
			GroupName:      request.Model,
			RequestID:      request.ID,
			Status:         request.Status,
			CurrentChannel: request.TargetChannel,
			ChannelStatus:  request.ChannelStatus,
			ErrorCount:     request.ErrorCount,
			RetryCount:     request.RetryCount,
			Attempt:        request.Attempt,
			AttemptLimit:   request.AttemptLimit,
			Retrying:       request.Retrying,
			LastError:      request.LastError,
			NextRetryAt:    request.NextRetryAt,
		})
	}
	return snapshot
}

func preferRuntimeRequest(candidate, current *RequestState) bool {
	candidateActive := candidate.Status == StatusRunning || candidate.Status == StatusCommitted
	currentActive := current.Status == StatusRunning || current.Status == StatusCommitted
	if candidateActive != currentActive {
		return candidateActive
	}
	return candidate.ID > current.ID
}

// publishRuntimeLocked 向订阅者发送 RuntimeSnapshot 使用的同一汇总结果;
// 慢连接会被关闭, 客户端重连后可取得新的完整快照。
func publishRuntimeLocked() {
	if len(runtimeWatchers) == 0 {
		return
	}
	snapshot := runtimeSnapshotLocked()
	for stream := range runtimeWatchers {
		select {
		case stream <- snapshot:
		default:
			delete(runtimeWatchers, stream)
			close(stream)
		}
	}
}
