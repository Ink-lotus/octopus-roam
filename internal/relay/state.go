package relay

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/Ink-lotus/octopus-roam/internal/privacy"
	"github.com/looplj/axonhub/llm"
)

// 客户端请求在转发过程中的当前状态。
type Status string

const (
	StatusRunning   Status = "running"   // 循环中: 正在选目标, 等待或请求上游。
	StatusCommitted Status = "committed" // 首字节已写出客户端, 此后不可再重试。
	StatusSuccess   Status = "success"   // 响应已完整交付客户端。
	StatusFailed    Status = "failed"    // 请求以错误结束。
	StatusCanceled  Status = "canceled"  // 客户端提前断开或取消。
)

// ChannelStatus 是请求当前目标渠道的运行状态。
type ChannelStatus string

const (
	ChannelStatusWaiting ChannelStatus = "waiting"
	ChannelStatusNormal  ChannelStatus = "normal"
	ChannelStatusError   ChannelStatus = "error"
)

// 客户端请求的完整进程内状态, 同时作为状态流的消息形状; 上半部分在请求到达时写入并在结束时定稿, 下半部分每轮循环覆盖。
type RequestState struct {
	ID                 uint64         `json:"id"`                   // 请求在当前进程内的唯一标识。
	Status             Status         `json:"status"`               // 请求当前状态。
	StartedAt          time.Time      `json:"started_at"`           // 请求到达时间。
	Duration           time.Duration  `json:"duration"`             // 请求从到达到结束的总耗时, 未结束时为零。
	FirstTokenDuration time.Duration  `json:"first_token_duration"` // 流式正确响应轮次开始到首字节提交的耗时, 非流式响应为零。
	StreamDuration     time.Duration  `json:"stream_duration"`      // 流式响应从首字节提交到响应结束的耗时, 非流式响应为零。
	ResponseDuration   time.Duration  `json:"response_duration"`    // 非流式正确响应轮次开始到完整响应提交的耗时, 流式响应为零。
	Model              string         `json:"model"`                // 客户端请求的模型名称, 即分组名称。
	ReasoningEffort    string         `json:"reasoning_effort"`     // 客户端请求的思考等级, 由入站转换器归一, 未指定或解析失败时为空。
	Protocol           model.Protocol `json:"protocol"`             // 客户端请求使用的协议, 由入站格式定出, 单个协议位而非掩码组合。
	GroupID            int            `json:"group_id"`             // 承载本请求的分组 ID, 供界面按主键直接定位分组而不必按名称回查。
	APIKeyName         string         `json:"api_key_name"`         // 发起请求时的 API Key 名称。
	Usage              llm.Usage      `json:"usage"`                // 请求结束时写入的展示用量。
	Cost               float64        `json:"cost"`                 // 请求结束时写入的累计费用。
	OutputChars        int            `json:"output_chars"`         // 流式过程中按事件数量估算并实时累计的输出字符数, 仅用于界面展示, 不参与结算。

	Round          int            `json:"round"`            // 最新一轮循环的递增序号, 人工中止按此匹配以免误杀下一轮。
	RoundStartedAt time.Time      `json:"round_started_at"` // 最新一轮上游请求的开始时间。
	TargetChannel  string         `json:"target_channel"`   // 最新一轮选中的渠道名称。
	TargetModel    string         `json:"target_model"`     // 最新一轮实际请求上游的模型名称。
	TargetProtocol model.Protocol `json:"target_protocol"`  // 最新一轮实际请求上游的协议, 与 Protocol 不同即本轮做了跨协议转换; 0 表示尚未选出。
	Sending        bool           `json:"sending"`          // 最新一轮是否仍在等待上游响应。
	Error          string         `json:"error,omitempty"`  // 最新一轮的失败原因, 请求结束后即为最终错误。
	RetryCount     int            `json:"retry_count"`      // 当前成员已经发起的重试次数, 等于 Attempt - 1。
	Attempt        int            `json:"attempt"`          // 当前成员已经发起的上游请求次数。
	AttemptLimit   int            `json:"attempt_limit"`    // 当前成员的尝试上限, 0 表示不限次数。
	Retrying       bool           `json:"retrying"`         // 当前请求是否正在等待重试或可用成员。
	ErrorCount     int            `json:"error_count"`      // 本请求累计遇到的失败次数。
	LastError      string         `json:"last_error"`       // 本请求最近一次失败原因, 成功后仍保留供诊断。
	NextRetryAt    int64          `json:"next_retry_at"`    // 下一次重试的 Unix 毫秒时间, 无计划时为 0。
	ChannelStatus  ChannelStatus  `json:"channel_status"`   // waiting、normal 或 error。

	requestBody       string               // 首次脱敏后的请求体, 后续读取还需使用最新凭据补充脱敏。
	responseBody      string               // 聚合后的完整最终响应体, 同样按需拉取。
	apiKeyID          int                  // 发起请求的 API Key ID, 用于请求完成后的归属统计。
	redactor          *privacy.Redactor    // 仅供诊断副本脱敏, 不序列化或持久化。
	requestBodyCache  *diagnosticBodyCache // 独立缓存锁, 不能复制到公开状态中作为值锁。
	responseBodyCache *diagnosticBodyCache

	requestCtx    context.Context    // 请求级上下文, 同时约束等待、当前轮次和后续重试。
	requestCancel context.CancelFunc // 中止整个请求, 同时打断等待、当前轮次和后续重试。
	roundCancel   context.CancelFunc // 中止最新一轮上游请求, 仅在该轮等待响应期间非空。
	lastPublish   time.Time          // 上次向状态流发布快照的时间, 输出字符数按此节流发布。
	streamStarted time.Time          // 流式响应首字节提交时间, 用于计算实际流式传输耗时。
	attemptItemID int                // Attempt 和 RetryCount 当前归属的分组成员。
	errorRound    int                // 最近已计入失败的轮次，按身份去重而非错误文案。
}

const streamBuffer = 16                              // 单个状态流连接的非阻塞消息缓冲容量。
const maxFinished = 50                               // 进程内最多保留的已结束请求数量。
const outputPublishInterval = 500 * time.Millisecond // 输出字符数实时推送的最短发布间隔。

func (r *RequestState) retryCount() int {
	mu.Lock()
	defer mu.Unlock()
	return r.RetryCount
}

var (
	idSeq           atomic.Uint64                          // 进程内严格递增的请求 ID。
	mu              sync.Mutex                             // 全部共享状态的互斥锁。
	requests        = make(map[uint64]*RequestState)       // 按请求 ID 保存的全部请求状态。
	watchers        = make(map[chan RequestState]struct{}) // 全部状态流 SSE 连接。
	runtimeWatchers = make(map[chan []GroupRuntime]struct{})
)

// newRequestState 分配请求 ID 并登记初始运行状态; 返回的记录是本请求后续全部状态写入的入口。
func newRequestState(ctx context.Context, modelName, reasoningEffort string, groupID int, protocol model.Protocol, body string, apiKeyID int) *RequestState {
	redactor := privacy.FromContext(ctx)
	body = redactor.Body(body)
	requestCtx, requestCancel := context.WithCancel(ctx)
	mu.Lock()
	defer mu.Unlock()

	request := &RequestState{
		ID:              idSeq.Add(1),
		Status:          StatusRunning,
		ChannelStatus:   ChannelStatusWaiting,
		StartedAt:       time.Now(),
		Model:           modelName,
		ReasoningEffort: reasoningEffort,
		Protocol:        protocol,
		GroupID:         groupID,
		requestBody:     body,
		redactor:        redactor,
		apiKeyID:        apiKeyID,
		requestCtx:      requestCtx,
		requestCancel:   requestCancel,
	}
	// 登记时保存名称快照, 查询失败时留空。
	if apiKey, err := op.APIKeyGet(apiKeyID, ctx); err == nil {
		redactor.Add(apiKey.APIKey)
		request.APIKeyName = apiKey.Name
	}
	requests[request.ID] = request
	publishRequestLocked(request)
	publishRuntimeLocked()
	return request
}

// selectMember 发布本轮选出的成员; 新成员或路由新一代恢复探测会重置该成员的尝试预算。
func (r *RequestState) selectMember(itemID int, channel, modelName string, protocol model.Protocol, attemptLimit int, resetAttempts bool) {
	mu.Lock()
	defer mu.Unlock()

	if r.attemptItemID != itemID || resetAttempts {
		r.Attempt = 0
		r.RetryCount = 0
		r.attemptItemID = itemID
	}
	r.TargetChannel = channel
	r.TargetModel = modelName
	r.TargetProtocol = protocol
	r.AttemptLimit = attemptLimit
	r.ChannelStatus = ChannelStatusNormal
	r.Retrying = false
	r.NextRetryAt = 0
	r.Error = ""
	publishRequestLocked(r)
	publishRuntimeLocked()
}

// startRound 记录本轮选中的目标并进入上游请求, cancel 供人工中止本轮, 返回递增的轮次序号。
func (r *RequestState) startRound(cancel context.CancelFunc, channel, modelName string, protocol model.Protocol, startsAttempt bool) int {
	mu.Lock()
	defer mu.Unlock()

	r.Round++
	if startsAttempt {
		r.Attempt++
		r.RetryCount = max(0, r.Attempt-1)
	}
	r.RoundStartedAt = time.Now()
	r.OutputChars = 0 // 新一轮从头计数, 避免累计上一轮未提交的输出。
	r.lastPublish = time.Time{}
	r.TargetChannel = channel
	r.TargetModel = modelName
	r.TargetProtocol = protocol
	r.Sending = true
	r.Error = ""
	r.ChannelStatus = ChannelStatusNormal
	r.Retrying = false
	r.NextRetryAt = 0
	r.roundCancel = cancel
	publishRequestLocked(r)
	publishRuntimeLocked()
	return r.Round
}

// discardUnsentAttempt 修正请求构造失败等未发出上游请求的轮次计数; 后续 finishRound 会发布修正快照。
func (r *RequestState) discardUnsentAttempt() {
	mu.Lock()
	defer mu.Unlock()

	if r.Attempt > 0 {
		r.Attempt--
	}
	r.RetryCount = max(0, r.Attempt-1)
}

// finishRound 记录本轮上游结果, errText 为空表示已取得可提交响应。
func (r *RequestState) finishRound(errText string, countError bool) {
	mu.Lock()
	defer mu.Unlock()

	r.Sending = false
	r.Error = r.redactor.Text(errText)
	if errText == "" {
		r.ChannelStatus = ChannelStatusNormal
		r.Retrying = false
		r.NextRetryAt = 0
	} else if countError {
		if r.errorRound != r.Round || r.Round == 0 {
			r.ErrorCount++
			r.errorRound = r.Round
		}
		r.LastError = r.Error
		r.ChannelStatus = ChannelStatusError
	}
	r.roundCancel = nil
	publishRequestLocked(r)
	publishRuntimeLocked()
}

// markRetrying 发布退避或路由等待的截止时间; 0 表示由路由变化通知唤醒, 暂无明确截止时间。
func (r *RequestState) markRetrying(delay time.Duration) {
	r.markRetryingAt(time.Now().Add(delay))
}

func (r *RequestState) markRetryingAt(next time.Time) {
	mu.Lock()
	defer mu.Unlock()

	r.Retrying = true
	r.ChannelStatus = ChannelStatusWaiting
	r.NextRetryAt = 0
	if !next.IsZero() {
		r.NextRetryAt = next.UnixMilli()
	}
	publishRequestLocked(r)
	publishRuntimeLocked()
}

// recordFailure 发布未进入上游请求的成员错误, 例如成员引用的授权或渠道已失效。
func (r *RequestState) recordFailure(errText string) {
	mu.Lock()
	defer mu.Unlock()

	r.Error = r.redactor.Text(errText)
	r.LastError = r.Error
	r.ErrorCount++
	r.ChannelStatus = ChannelStatusError
	publishRequestLocked(r)
	publishRuntimeLocked()
}

// addOutput 每个转发事件累加一个输出字符并按节流间隔发布快照; 距上次发布不足阈值时只累加不出流。
func (r *RequestState) addOutput() {
	mu.Lock()
	defer mu.Unlock()

	r.OutputChars++
	if time.Since(r.lastPublish) >= outputPublishInterval {
		r.lastPublish = time.Now()
		if !r.streamStarted.IsZero() {
			r.StreamDuration = time.Since(r.streamStarted)
		}
		publishRequestLocked(r)
	}
}

// Interrupt 中止指定请求仍在等待响应且轮次匹配的上游请求; 轮次不匹配说明该轮已结束, 不影响后续轮次。
func Interrupt(id uint64, round int) {
	mu.Lock()
	request := requests[id]
	if request == nil || request.Round != round || request.roundCancel == nil {
		mu.Unlock()
		return
	}
	cancel := request.roundCancel
	request.roundCancel = nil
	mu.Unlock()

	cancel()
}

// CancelRequest 取消指定的完整请求; 已结束请求不会被重新改写状态。
func CancelRequest(id uint64) {
	mu.Lock()
	request := requests[id]
	if request == nil {
		mu.Unlock()
		return
	}
	cancel := request.requestCancel
	request.requestCancel = nil
	mu.Unlock()

	if cancel != nil {
		cancel()
	}
}

// wait 在重新选择目标之前退避 seconds 秒; 客户端在退避期间断开时以取消终态定稿并返回 false。
// 换算一律走 secondsToDuration: 过大的秒数直接乘 time.Second 会在 int64 乘法里回绕成非正数,
// time.After 随即到期, 这些路径(发送前配置错误, 无可用成员等)的退避就退化成无等待的快速重轮。
func (r *RequestState) wait(ctx context.Context, seconds int) bool {
	return r.waitDuration(ctx, secondsToDuration(seconds))
}

// waitDuration 与 wait 相同, 但等待时长由调用方按重试策略算出, 供退避等待使用。
func (r *RequestState) waitDuration(ctx context.Context, delay time.Duration) bool {
	r.markRetrying(delay)
	select {
	case <-ctx.Done():
		r.markCanceled(ctx.Err(), "", nil)
		return false
	case <-time.After(delay):
		return true
	}
}

// waitRetry 在同成员退避期间也监听路由变化; 选路版本失效时立即回到循环读取新状态。
func (r *RequestState) waitRetry(ctx context.Context, delay time.Duration, changed <-chan struct{}) bool {
	r.markRetrying(delay)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		r.markCanceled(ctx.Err(), "", nil)
		return false
	case <-changed:
		return true
	case <-timer.C:
		return true
	}
}

// markCommitted 标记响应已提交, 并按响应方式记录最终正确轮次的首字或完整响应耗时。
func (r *RequestState) markCommitted(streaming bool) {
	mu.Lock()
	defer mu.Unlock()

	now := time.Now()
	r.Status = StatusCommitted
	r.ChannelStatus = ChannelStatusNormal
	r.Retrying = false
	r.NextRetryAt = 0
	if streaming {
		r.FirstTokenDuration = now.Sub(r.RoundStartedAt)
		r.streamStarted = now
	} else {
		r.ResponseDuration = now.Sub(r.RoundStartedAt)
	}
	publishRequestLocked(r)
	publishRuntimeLocked()
}

// finishStream 记录首字节提交至流式响应实际结束的耗时。
func (r *RequestState) finishStream() {
	mu.Lock()
	defer mu.Unlock()

	if !r.streamStarted.IsZero() {
		r.StreamDuration = time.Since(r.streamStarted)
	}
}

// markSucceeded 以成功终态定稿请求。
func (r *RequestState) markSucceeded(responseBody string, usage *llm.Usage) {
	mu.Lock()
	defer mu.Unlock()

	r.Status = StatusSuccess
	r.Error = ""
	r.ChannelStatus = ChannelStatusNormal
	r.Retrying = false
	r.NextRetryAt = 0
	r.responseBody = r.redactor.Body(responseBody)
	r.finishLocked(usage)
}

// markFailed 以失败终态定稿请求, 最终错误取自本次失败原因。
func (r *RequestState) markFailed(err error, responseBody string, usage *llm.Usage) {
	mu.Lock()
	defer mu.Unlock()

	if r.requestCtx.Err() != nil {
		r.Status = StatusCanceled
		r.Error = r.requestCtx.Err().Error()
		r.ChannelStatus = ChannelStatusNormal
	} else {
		r.Status = StatusFailed
		r.Error = r.redactor.Text(err.Error())
		if r.errorRound != r.Round || r.Round == 0 {
			r.ErrorCount++
			r.errorRound = r.Round
		}
		r.LastError = r.Error
		r.ChannelStatus = ChannelStatusError
	}
	if responseBody != "" {
		r.responseBody = r.redactor.Body(responseBody)
	}
	r.finishLocked(usage)
}

// markCanceled 以取消终态定稿请求, 用于客户端提前断开或主动取消。
func (r *RequestState) markCanceled(err error, responseBody string, usage *llm.Usage) {
	mu.Lock()
	defer mu.Unlock()

	r.Status = StatusCanceled
	r.Error = r.redactor.Text(err.Error())
	r.ChannelStatus = ChannelStatusNormal
	r.Retrying = false
	r.NextRetryAt = 0
	if responseBody != "" {
		r.responseBody = r.redactor.Body(responseBody)
	}
	r.finishLocked(usage)
}

// finishLocked 写入用量和费用, 发布终态, 更新请求级统计并裁剪历史; 调用方必须持有锁。
func (r *RequestState) finishLocked(usage *llm.Usage) {
	r.Sending = false
	r.Retrying = false
	r.NextRetryAt = 0
	r.roundCancel = nil
	if r.requestCancel != nil {
		r.requestCancel()
	}
	r.requestCancel = nil
	if usage != nil {
		r.Usage = *usage
	}
	metrics := usageMetrics(r.TargetModel, usage)
	r.Cost = metrics.InputCost + metrics.OutputCost
	r.Duration = time.Since(r.StartedAt)
	metrics.WaitTime = r.Duration.Milliseconds()
	if r.Status == StatusSuccess {
		metrics.RequestSuccess = 1
	} else {
		metrics.RequestFailed = 1
	}
	_ = op.StatsTotalUpdate(metrics)
	_ = op.StatsHourlyUpdate(metrics)
	_ = op.StatsDailyUpdate(context.Background(), metrics)
	if r.apiKeyID > 0 {
		_ = op.StatsAPIKeyUpdate(r.apiKeyID, metrics)
	}

	finished := 0
	oldest := uint64(0)
	for id, request := range requests {
		if request.Status == StatusRunning || request.Status == StatusCommitted {
			continue
		}
		finished++
		if oldest == 0 || id < oldest {
			oldest = id
		}
	}
	if finished > maxFinished {
		delete(requests, oldest)
	}
	publishRequestLocked(r)
	publishRuntimeLocked()
}

// usageMetrics 将统一用量按模型单价转换为 Token 与费用统计; 无用量或价格时对应费用为零。
func usageMetrics(modelName string, usage *llm.Usage) model.StatsMetrics {
	if usage == nil {
		return model.StatsMetrics{}
	}
	metrics := model.StatsMetrics{InputToken: usage.PromptTokens, OutputToken: usage.CompletionTokens}
	price, err := op.LLMGet(modelName)
	if err != nil {
		return metrics
	}
	cachedTokens, writeCachedTokens := int64(0), int64(0)
	if usage.PromptTokensDetails != nil {
		cachedTokens = usage.PromptTokensDetails.CachedTokens
		writeCachedTokens = usage.PromptTokensDetails.WriteCachedTokens
	}
	inputTokens := max(int64(0), usage.PromptTokens-cachedTokens-writeCachedTokens)
	metrics.InputCost = (float64(inputTokens)*price.Input + float64(cachedTokens)*price.CacheRead + float64(writeCachedTokens)*price.CacheWrite) / 1_000_000
	metrics.OutputCost = float64(usage.CompletionTokens) * price.Output / 1_000_000
	return metrics
}

// publishRequestLocked 非阻塞发布最新请求状态, 连接拥塞时关闭它并交给客户端重连获取全量快照; 调用方必须持有锁。
func publishRequestLocked(request *RequestState) {
	for stream := range watchers {
		select {
		case stream <- *request:
		default:
			delete(watchers, stream)
			close(stream)
		}
	}
}

// OpenRequestStream 注册请求状态流连接, 返回按请求 ID 倒序的全部快照和后续增量通道。
// 日志页不提供排序开关, 而 requests 是 map, 遍历顺序随机, 故顺序须由此处定稿。
func OpenRequestStream() ([]RequestState, chan RequestState) {
	mu.Lock()
	defer mu.Unlock()

	stream := make(chan RequestState, streamBuffer)
	watchers[stream] = struct{}{}

	snapshot := make([]RequestState, 0, len(requests))
	for _, request := range requests {
		snapshot = append(snapshot, *request)
	}
	sort.Slice(snapshot, func(i, j int) bool { return snapshot[i].ID > snapshot[j].ID })
	return snapshot, stream
}

// CloseRequestStream 注销并关闭指定请求状态流连接。
func CloseRequestStream(stream chan RequestState) {
	mu.Lock()
	defer mu.Unlock()

	if _, exists := watchers[stream]; exists {
		delete(watchers, stream)
		close(stream)
	}
}

// RequestBody 返回使用当前已知凭据脱敏的请求体, 记录不存在时返回空串。
func RequestBody(id uint64) string {
	return readDiagnosticBody(id, false)
}

// ResponseBody 返回指定请求当前保存的响应体, 记录不存在或响应未完成时返回空串。
func ResponseBody(id uint64) string {
	return readDiagnosticBody(id, true)
}

// Clear 删除全部已结束的请求记录。
func Clear() {
	mu.Lock()
	defer mu.Unlock()

	cleared := false
	for id, request := range requests {
		if request.Status != StatusRunning && request.Status != StatusCommitted {
			delete(requests, id)
			cleared = true
		}
	}
	if cleared {
		publishRuntimeLocked()
	}
}
