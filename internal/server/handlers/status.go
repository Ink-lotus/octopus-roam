package handlers

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/relay"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-contrib/sse"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/status").
		Use(middleware.StatusToken()).
		AddRoute(
			router.NewRoute("/snapshot", http.MethodGet).
				Handle(getStatusSnapshot),
		).
		AddRoute(
			router.NewRoute("/events", http.MethodGet).
				Handle(streamStatusEvents),
		)
}

// StatusSnapshot is the public, credential-free view consumed by the companion.
// It intentionally contains only group and relay telemetry fields.
type StatusSnapshot struct {
	GroupID        int                 `json:"group_id"`
	GroupName      string              `json:"group_name"`
	Mode           model.GroupMode     `json:"mode"`
	RequestID      uint64              `json:"request_id"`
	Status         relay.Status        `json:"status"`
	CurrentChannel string              `json:"current_channel"`
	ChannelStatus  relay.ChannelStatus `json:"channel_status"`
	ErrorCount     int                 `json:"error_count"`
	RetryCount     int                 `json:"retry_count"`
	Attempt        int                 `json:"attempt"`
	AttemptLimit   int                 `json:"attempt_limit"`
	Retrying       bool                `json:"retrying"`
	NextRetryAt    int64               `json:"next_retry_at"`
	LastError      string              `json:"last_error"`
}

func getStatusSnapshot(c *gin.Context) {
	resp.Success(c, statusSnapshot())
}

func streamStatusEvents(c *gin.Context) {
	prepareSSE(c)
	_, runtimeUpdates := relay.OpenRuntimeStream()
	defer relay.CloseRuntimeStream(runtimeUpdates)
	routeUpdates := relay.OpenRouteStream()
	defer relay.CloseRouteStream(routeUpdates)
	// 订阅分组事件: 没有模型请求时, 分组的创建/修改/删除与手动切换成员都只走分组事件,
	// 不产生 Relay 运行时或路由增量, 需由此触发状态推送。
	groupEvents := openGroupEventStream()
	defer closeGroupEventStream(groupEvents)
	tokenID := middleware.StatusTokenID(c)
	revoked := op.OpenStatusTokenRevocationStream(tokenID)
	defer op.CloseStatusTokenRevocationStream(tokenID, revoked)
	if !op.StatusTokenValid(c.Request.Context(), tokenID) {
		return
	}

	sequence := uint64(0)
	if !writeStatusEvents(c, statusSnapshot(), &sequence) {
		return
	}
	c.Writer.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	tokenCheck := time.NewTicker(time.Second)
	defer tokenCheck.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-revoked:
			return
		case <-tokenCheck.C:
			if !op.StatusTokenValid(c.Request.Context(), tokenID) {
				return
			}
		case <-heartbeat.C:
			if _, err := c.Writer.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			c.Writer.Flush()
		case _, ok := <-runtimeUpdates:
			if !ok || !writeStatusEvents(c, statusSnapshot(), &sequence) {
				return
			}
			c.Writer.Flush()
		case _, ok := <-routeUpdates:
			if !ok || !writeStatusEvents(c, statusSnapshot(), &sequence) {
				return
			}
			c.Writer.Flush()
		case event, ok := <-groupEvents:
			if !ok || !writeStatusGroupEvent(c, event, &sequence) {
				return
			}
			c.Writer.Flush()
		}
	}
}

func statusSnapshot() []StatusSnapshot {
	groups := op.GroupList()
	runtimes := relay.RuntimeSnapshot()
	runtimeByGroup := make(map[int]relay.GroupRuntime, len(runtimes))
	for _, runtime := range runtimes {
		runtimeByGroup[runtime.GroupID] = runtime
	}

	snapshot := make([]StatusSnapshot, 0, len(groups))
	for _, group := range groups {
		state := relay.RouteStateOf(group)
		itemChannel := currentChannel(group, state.CurrentItemID)
		item := StatusSnapshot{
			GroupID:        group.ID,
			GroupName:      group.Name,
			Mode:           group.Mode,
			CurrentChannel: itemChannel,
			ChannelStatus:  relay.ChannelStatusWaiting,
		}
		if len(state.Cooldowns) > 0 {
			item.ChannelStatus = relay.ChannelStatusError
			item.Retrying = true
		}
		if runtime, ok := runtimeByGroup[group.ID]; ok && telemetryReflectsRoute(group, state, runtime.CurrentChannel) {
			item.RequestID = runtime.RequestID
			item.Status = runtime.Status
			item.CurrentChannel = runtime.CurrentChannel
			item.ChannelStatus = runtime.ChannelStatus
			item.ErrorCount = runtime.ErrorCount
			item.RetryCount = runtime.RetryCount
			item.Attempt = runtime.Attempt
			item.AttemptLimit = runtime.AttemptLimit
			item.Retrying = runtime.Retrying
			item.NextRetryAt = runtime.NextRetryAt
			item.LastError = sanitizeStatusError(runtime.LastError)
		}
		snapshot = append(snapshot, item)
	}
	return snapshot
}

// telemetryReflectsRoute 判定运行遥测是否仍描述分组的当前渠道:
// 遥测取自历史请求(含已结束的), 渠道被删除, 禁用或切换后, 旧请求不得再覆盖当前渠道与健康状态。
// 规则: 当前成员必须存在且可用; 仅故障转移模式等待/冷却时, 才按任一可用成员匹配遥测。
func telemetryReflectsRoute(group model.Group, state relay.RouteState, telemetryChannel string) bool {
	if telemetryChannel == "" {
		return false
	}
	if state.CurrentItemID != 0 {
		return telemetryChannel == currentAvailableChannel(group, state.CurrentItemID)
	}
	return group.Mode == model.GroupModeFailover && groupHasAvailableChannel(group, telemetryChannel)
}

// groupHasAvailableChannel 判定分组内是否仍有一个可用成员使用该渠道名称。
func groupHasAvailableChannel(group model.Group, channelName string) bool {
	for _, item := range group.Items {
		if item.Available && item.ChannelName == channelName {
			return true
		}
	}
	return false
}

func currentChannel(group model.Group, itemID int) string {
	for _, item := range group.Items {
		if item.ID == itemID {
			return item.ChannelName
		}
	}
	return ""
}

func currentAvailableChannel(group model.Group, itemID int) string {
	for _, item := range group.Items {
		if item.ID == itemID && item.Available {
			return item.ChannelName
		}
	}
	return ""
}

func writeStatusEvents(c *gin.Context, snapshot []StatusSnapshot, sequence *uint64) bool {
	if len(snapshot) == 0 {
		_, err := c.Writer.Write([]byte(": connected\n\n"))
		return err == nil
	}
	for _, item := range snapshot {
		(*sequence)++
		if err := sse.Encode(c.Writer, sse.Event{
			Id:    strconv.FormatUint(*sequence, 10),
			Event: "status",
			Data:  item,
		}); err != nil {
			return false
		}
	}
	return true
}

// statusRemoved 是分组删除的推送载荷, 让客户端明确移除该分组, 而不必依赖它从后续快照中"消失"来推断。
type statusRemoved struct {
	GroupID int `json:"group_id"`
}

// writeStatusGroupEvent 将分组事件转换为状态流事件:
// "deleted" 明确通知客户端移除该分组; 其余(创建/修改/手动切换等 "changed")统一重推一份脱敏后的快照。
// 分组事件本身携带管理面数据, 此处一律不转发其载荷, 只作为触发重新计算 statusSnapshot 的信号。
func writeStatusGroupEvent(c *gin.Context, event groupEvent, sequence *uint64) bool {
	if event.Name == "deleted" {
		if groupID, ok := event.Data.(int); ok {
			(*sequence)++
			return sse.Encode(c.Writer, sse.Event{
				Id:    strconv.FormatUint(*sequence, 10),
				Event: "status_removed",
				Data:  statusRemoved{GroupID: groupID},
			}) == nil
		}
		// 删除载荷异常时退回全量快照, 让客户端自行对齐。
		return writeStatusEvents(c, statusSnapshot(), sequence)
	}
	return writeStatusEvents(c, statusSnapshot(), sequence)
}

var (
	// Authorization 字段整体屏蔽, 覆盖任意认证方案及 Digest 的多个参数。
	authorizationFieldPattern = regexp.MustCompile(`(?i)(^|[^a-z0-9])(["']?authorization["']?\s*[:=]\s*)`)
	credentialFieldPattern    = regexp.MustCompile(`(?i)(^|[^a-z0-9])(["']?(?:api[-_ ]?key|token|secret|password)["']?\s*[:=]\s*)`)
	standaloneAuthPattern     = regexp.MustCompile(`(?i)\b(basic|bearer)\s+[^\s"',;}]+`)
	// querySecretPattern 屏蔽 URL 查询串中的凭据。
	querySecretPattern = regexp.MustCompile(`(?i)([?&](?:api[-_]?key|key|token|secret|password)=)[^&#\s]+`)
)

func sanitizeStatusError(value string) string {
	value = redactStatusFields(value, authorizationFieldPattern)
	value = redactStatusFields(value, credentialFieldPattern)
	value = standaloneAuthPattern.ReplaceAllString(value, "$1 [redacted]")
	value = querySecretPattern.ReplaceAllString(value, "$1[redacted]")
	value = strings.TrimSpace(value)
	if len(value) > 512 {
		return value[:512] + "..."
	}
	return value
}

func redactStatusFields(value string, fieldPattern *regexp.Regexp) string {
	matches := fieldPattern.FindAllStringSubmatchIndex(value, -1)
	if len(matches) == 0 {
		return value
	}

	var result strings.Builder
	result.Grow(len(value))
	cursor := 0
	for _, match := range matches {
		valueStart := match[1]
		if match[0] < cursor || valueStart >= len(value) {
			continue
		}
		result.WriteString(value[cursor:valueStart])

		quote := value[valueStart]
		if quote == '"' || quote == '\'' {
			if valueEnd, closed := quotedStatusValueEnd(value, valueStart, quote); closed {
				result.WriteByte(quote)
				result.WriteString("[redacted]")
				result.WriteByte(quote)
				cursor = valueEnd + 1
				continue
			}
			result.WriteByte(quote)
		}

		valueEnd := statusFieldLineEnd(value, valueStart)
		result.WriteString("[redacted]")
		cursor = valueEnd
	}
	result.WriteString(value[cursor:])
	return result.String()
}

func quotedStatusValueEnd(value string, start int, quote byte) (int, bool) {
	for index := start + 1; index < len(value); index++ {
		switch value[index] {
		case '\\':
			index++
		case quote:
			return index, true
		case '\r', '\n':
			return 0, false
		}
	}
	return 0, false
}

func statusFieldLineEnd(value string, start int) int {
	for index := start; index < len(value); index++ {
		if value[index] == '\r' || value[index] == '\n' {
			return index
		}
	}
	return len(value)
}
