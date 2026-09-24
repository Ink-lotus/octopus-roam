package relay

// 本文件提供 internal/relay 测试共用的进程内环境与构造函数。
// 转发链路经 op 读取分组, 渠道与凭据, 故测试跑在真实 SQLite 库上而非替身之上;
// 上游一律指向 httptest.Server, 不连接真实外部服务, 也不使用真实凭据。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
)

// chatCompletionBody 是一份最小的非流式 Chat Completions 成功响应, 正文原样回给客户端。
const chatCompletionBody = `{"id":"chatcmpl-test","object":"chat.completion","created":1,"model":"upstream","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`

// chatCompletionStreamBody 是两段 SSE: 一个内容增量与结束标记。
const chatCompletionStreamBody = "data: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"upstream\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"pong\"},\"finish_reason\":null}]}\n\ndata: [DONE]\n\n"

// TestMain 为全包测试建立一个 SQLite 库并刷新 op 缓存, 结束后清理。
// 库与缓存在本进程内共用: 各测试按唯一名称建立自己的渠道与分组, 互不干扰。
func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)

	dir, err := os.MkdirTemp("", "octopus-relay-test")
	if err != nil {
		fmt.Fprintf(os.Stderr, "创建测试临时目录失败: %v\n", err)
		os.Exit(1)
	}
	if err := db.InitDB("sqlite", filepath.Join(dir, "octopus.db"), false); err != nil {
		fmt.Fprintf(os.Stderr, "初始化测试数据库失败: %v\n", err)
		os.Exit(1)
	}
	if err := op.InitCache(); err != nil {
		fmt.Fprintf(os.Stderr, "初始化测试缓存失败: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()
	_ = db.Close()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// upstreamCallLog 按到达顺序记录各上游收到的请求, 供断言成员调用顺序与次数。
type upstreamCallLog struct {
	mu    sync.Mutex
	calls []string
}

// add 记录一次上游请求到达。
func (l *upstreamCallLog) add(entry string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, entry)
}

// snapshot 返回当前调用序列的副本。
func (l *upstreamCallLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.calls...)
}

// count 返回调用记录中匹配给定前缀的请求次数。
func (l *upstreamCallLog) count(member string) int {
	total := 0
	for _, call := range l.snapshot() {
		if call == member || strings.HasPrefix(call, member+"/") {
			total++
		}
	}
	return total
}

// fakeUpstream 是一个按脚本响应的测试上游: 前 len(statuses) 次请求依次返回脚本中的状态码, 用尽后一律成功。
// 记录条目形如 "<成员名>/<上游模型名>", 既标识被调用的成员, 也证明转发时写入了该成员的上游模型名。
type fakeUpstream struct {
	name     string // 成员标识, 通常取渠道名。
	statuses []int  // 按到达顺序给出的失败状态码; 用尽后成功。
	log      *upstreamCallLog
	arrived  chan struct{} // 每次请求到达时投递一个信号, 供测试等待上游真的被调用。
	server   *httptest.Server
	requests atomic.Int64 // 已到达的请求总数。
}

// newFakeUpstream 启动一个按脚本响应的测试上游, 并在测试结束时关闭。
func newFakeUpstream(t *testing.T, name string, log *upstreamCallLog, statuses ...int) *fakeUpstream {
	t.Helper()

	upstream := &fakeUpstream{
		name:     name,
		statuses: statuses,
		log:      log,
		arrived:  make(chan struct{}, 1024),
	}
	upstream.server = httptest.NewServer(http.HandlerFunc(upstream.handle))
	t.Cleanup(upstream.server.Close)
	return upstream
}

// handle 按脚本应答一次上游请求。
func (u *fakeUpstream) handle(w http.ResponseWriter, r *http.Request) {
	attempt := int(u.requests.Add(1))
	select {
	case u.arrived <- struct{}{}:
	default:
	}

	var payload struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	_ = json.NewDecoder(r.Body).Decode(&payload)
	u.log.add(u.name + "/" + payload.Model)

	status := http.StatusOK
	if attempt <= len(u.statuses) {
		status = u.statuses[attempt-1]
	}
	if status != http.StatusOK {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"error":{"message":"scripted upstream failure","type":"server_error"}}`)
		return
	}

	if payload.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, chatCompletionStreamBody)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, chatCompletionBody)
}

// requestCount 返回该上游已收到的请求总数。
func (u *fakeUpstream) requestCount() int {
	return int(u.requests.Load())
}

// awaitRequests 等待该上游收到 want 次请求; 每次到达由一个信号表示, 故等待不依赖轮询或调度顺序。
func (u *fakeUpstream) awaitRequests(t *testing.T, want int, timeout time.Duration) {
	t.Helper()

	for i := 1; i <= want; i++ {
		select {
		case <-u.arrived:
		case <-time.After(timeout):
			t.Fatalf("等待上游 %s 第 %d 次请求到达超时, 当前共 %d 次", u.name, i, u.requestCount())
		}
	}
}

// failingStatuses 返回连续 n 次失败的脚本, 供"上游持续失败"场景使用。
func failingStatuses(status, n int) []int {
	statuses := make([]int, n)
	for i := range statuses {
		statuses[i] = status
	}
	return statuses
}

// testNameSeq 为同一测试的多次运行(go test -count)生成不同的名称。
var testNameSeq atomic.Int64

// testName 生成进程内唯一的测试名称, 使各测试建立的分组与渠道互不干扰。
func testName(t *testing.T, kind string) string {
	t.Helper()
	return fmt.Sprintf("%s-%s-%d", kind, strings.ReplaceAll(t.Name(), "/", "-"), testNameSeq.Add(1))
}

// testRelayConfig 返回测试用的 Relay 配置: 固定重试间隔取配置允许的最小非零值 1 秒,
// 使重试确实经过等待而不必久等; 亲和时间为零, 使故障转移的选路只由冷却决定。
// 新增字段一并显式写出, 使测试不依赖 NormalizeGroupRelayConfig 的回填: 手动上限 0 表示不限额。
func testRelayConfig(maxAttempts int) model.GroupRelayConfig {
	return model.GroupRelayConfig{
		MemberMaxAttempts:                     maxAttempts,
		MemberRetryIntervalSeconds:            1,
		MemberNonStreamResponseTimeoutSeconds: 30,
		MemberStreamFirstEventTimeoutSeconds:  30,
		MemberCooldownSeconds:                 60,
		MemberAffinitySeconds:                 0,
		RetryMode:                             model.RetryModeFixed,
		RetryInitialIntervalSeconds:           model.RetryInitialIntervalSecondsDefault,
		RetryMaxIntervalSeconds:               model.RetryMaxIntervalSecondsLimit,
		ManualMaxAttempts:                     0,
	}
}

// testRetryConfigWithMode 返回指定重试模式的测试配置: 两种模式的首次重试等待都是 1 秒,
// 差异只体现在指数模式的后续重试递增上, 故断言可以只靠耗时下界区分两者。
func testRetryConfigWithMode(mode model.RetryMode) model.GroupRelayConfig {
	config := testRelayConfig(2)
	config.RetryMode = mode
	return config
}

// newTestChannel 建立一条指向给定测试上游的渠道并注册一个模型与凭据, 返回其可加入分组的授权主键。
// 上游模型名随渠道给定, 转发时写入请求体, 由此可从上游侧确认本轮实际命中的成员。
func newTestChannel(t *testing.T, name, baseURL, upstreamModel string) int {
	t.Helper()

	detail := &model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{
			Name:                     name,
			Enabled:                  true,
			BaseURL:                  baseURL,
			OpenAIChatCompletionPath: "/v1/chat/completions",
			OpenAIResponsePath:       "/v1/responses",
			AnthropicMessagePath:     "/v1/messages",
		},
		Keys:   []model.ChannelKeyConfig{{Name: "key", Key: "test-upstream-credential", Enabled: true}},
		Models: []string{upstreamModel},
		Grants: []model.ChannelGrantConfig{{ModelName: upstreamModel, KeyName: "key", Protocols: model.ProtocolOpenAIChatCompletion}},
	}
	created, err := op.ChannelCreate(detail, context.Background())
	if err != nil {
		t.Fatalf("建立测试渠道 %s 失败: %v", name, err)
	}
	for _, candidate := range op.ChannelGrantCandidates() {
		if candidate.ChannelID == created.ID && candidate.ModelName == upstreamModel {
			return candidate.ID
		}
	}
	t.Fatalf("测试渠道 %s 未产生可用授权", name)
	return 0
}

// channelIDOfGrant 返回给定授权所属的渠道主键。
func channelIDOfGrant(t *testing.T, grantID int) int {
	t.Helper()

	for _, candidate := range op.ChannelGrantCandidates() {
		if candidate.ID == grantID {
			return candidate.ChannelID
		}
	}
	t.Fatalf("授权 %d 不存在", grantID)
	return 0
}

// breakChannelBaseURL 把渠道地址清空并刷新缓存, 用于构造"出站转换器无法构造"的成员。
// 经 op 的写入路径产生不了该状态: 创建与更新都要求地址非空, 故只能直接改库,
// 模拟人工改库或历史遗留的异常行——这类数据同样会流到转发链路上。
// 该状态下 buildOutbound 在请求上游之前就返回错误, 即本轮没有真正发出上游请求。
func breakChannelBaseURL(t *testing.T, grantID int) {
	t.Helper()

	channelID := channelIDOfGrant(t, grantID)
	if err := db.GetDB().Model(&model.Channel{}).Where("id = ?", channelID).Update("base_url", "").Error; err != nil {
		t.Fatalf("清空渠道 %d 的地址失败: %v", channelID, err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatalf("改库后刷新缓存失败: %v", err)
	}
}

// setChannelParamOverride 把渠道的参数覆盖配置设为给定值并刷新缓存。
// 设为非法 JSON 可让 applyChannelConfig 在构造上游请求时返回错误, 即本轮失败发生在发出上游请求之前。
func setChannelParamOverride(t *testing.T, grantID int, value string) {
	t.Helper()

	channelID := channelIDOfGrant(t, grantID)
	if err := db.GetDB().Model(&model.Channel{}).Where("id = ?", channelID).Update("param_override", value).Error; err != nil {
		t.Fatalf("设置渠道 %d 的参数覆盖失败: %v", channelID, err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatalf("改库后刷新缓存失败: %v", err)
	}
}

// newTestGroup 按给定模式, Relay 配置与成员顺序建立一个分组, 返回分组名。
// 分组名即客户端请求的模型名, 也是 Relay 定位分组的依据。
// 手动模式额外把首个成员指定为当前成员: 该模式没有进程内路由, 当前成员只能由分组配置人工指定;
// 未指定时选不出成员, 请求会一直等待重试, 直到有成员被选入为止——这是该模式既有的设计语义, 不是缺陷。
// 测试据此显式指定当前成员, 对应界面上"先选成员再使用"的操作。
func newTestGroup(t *testing.T, mode model.GroupMode, config model.GroupRelayConfig, grantIDs ...int) string {
	t.Helper()

	items := make([]model.GroupItemInput, len(grantIDs))
	for i, grantID := range grantIDs {
		items[i] = model.GroupItemInput{ChannelGrantID: grantID}
	}
	name := testName(t, "group")
	group, err := op.GroupCreate(&model.GroupCreateRequest{
		Name:        name,
		Mode:        mode,
		RelayConfig: config,
		Items:       items,
	}, context.Background())
	if err != nil {
		t.Fatalf("建立测试分组 %s 失败: %v", name, err)
	}

	if mode == model.GroupModeManual {
		if len(group.Items) == 0 {
			t.Fatalf("手动模式的测试分组 %s 至少需要一个成员", name)
		}
		activeItemID := group.Items[0].ID
		if _, err := op.GroupUpdate(group.ID, &model.GroupUpdateRequest{ActiveItemID: &activeItemID}, context.Background()); err != nil {
			t.Fatalf("指定测试分组 %s 的当前成员失败: %v", name, err)
		}
	}
	return name
}

// newRelayEngine 建立只挂载 Chat Completions 转发路由的测试引擎。
func newRelayEngine() *gin.Engine {
	engine := gin.New()
	engine.POST("/v1/chat/completions", Forward(llm.APIFormatOpenAIChatCompletion))
	return engine
}

// serveWithTimeout 在明确的超时边界内跑完一次转发; 转发含重试循环, 故任何测试都不应无界等待。
func serveWithTimeout(t *testing.T, engine *gin.Engine, recorder *httptest.ResponseRecorder, req *http.Request) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		defer close(done)
		engine.ServeHTTP(recorder, req)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatalf("转发未在 30 秒内结束, 疑为重试循环未按预期退出")
	}
}

// assertUpstreamFailure 断言转发以协议合法的上游错误结束: HTTP 503 与 upstream_error 类型。
// 正文必须是可解析的错误对象, 即重试耗尽后返回的仍是客户端协议认识的响应。
func assertUpstreamFailure(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("最终状态码 = %d, 期望 503; 响应正文 %s", recorder.Code, recorder.Body.String())
	}
	var failure struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &failure); err != nil {
		t.Fatalf("最终响应不是合法的错误对象: %v; 响应正文 %s", err, recorder.Body.String())
	}
	if failure.Error.Type != "upstream_error" {
		t.Fatalf("错误类型 = %q, 期望 upstream_error; 响应正文 %s", failure.Error.Type, recorder.Body.String())
	}
	if failure.Error.Message == "" {
		t.Fatalf("错误对象没有消息: %s", recorder.Body.String())
	}
	retryAfter := recorder.Header().Get("Retry-After")
	if seconds, err := strconv.ParseInt(retryAfter, 10, 64); err != nil || seconds < 1 || strconv.FormatInt(seconds, 10) != retryAfter {
		t.Fatalf("Retry-After = %q, 期望正整数秒", retryAfter)
	}
}

// assertRetryAfter 断言上游失败提示的下一次退避秒数。
func assertRetryAfter(t *testing.T, recorder *httptest.ResponseRecorder, want string) {
	t.Helper()
	if got := recorder.Header().Get("Retry-After"); got != want {
		t.Fatalf("Retry-After = %q, 期望 %q", got, want)
	}
}

// platformInt 把 int64 取值收进当前平台的 int 范围, 供测试书写"超出 time.Duration 可换算范围"的秒数。
// 32 位平台的 int 装不下 9223372037 这类字面量(编译期即报溢出错, 测试根本无法构建),
// 故先用 int64 承载再按位宽在运行时收口; 收口后的取值仍远大于任何配置上限, 覆盖同一分支。
func platformInt(v int64) int {
	if strconv.IntSize == 32 && v > math.MaxInt32 {
		return math.MaxInt32
	}
	return int(v)
}

// chatRequest 构造一个发往 Relay 的 Chat Completions 客户端请求, 请求体中的模型名即分组名。
func chatRequest(t *testing.T, groupName string, streaming bool) *http.Request {
	t.Helper()

	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"ping"}]}`, groupName)
	if streaming {
		body = fmt.Sprintf(`{"model":%q,"stream":true,"messages":[{"role":"user","content":"ping"}]}`, groupName)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}
