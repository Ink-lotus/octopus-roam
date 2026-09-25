package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/relay"
	serverauth "github.com/bestruirui/octopus/internal/server/auth"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func TestSanitizeStatusErrorRemovesCredentialValues(t *testing.T) {
	input := `request failed: Authorization: Bearer sk-secret-token api_key=private-key https://upstream.test/?api_key=query-secret`
	got := sanitizeStatusError(input)
	for _, secret := range []string{"sk-secret-token", "private-key", "query-secret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("status error leaked %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, "[redacted]") {
		t.Fatalf("status error was not redacted: %s", got)
	}
}

func TestSanitizeStatusErrorHandlesQuotedCredentials(t *testing.T) {
	input := `{"authorization":"Bearer quoted-secret","api_key":"json-secret","password":"pw-secret"}`
	got := sanitizeStatusError(input)
	for _, secret := range []string{"quoted-secret", "json-secret", "pw-secret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("quoted status error leaked %q: %s", secret, got)
		}
	}
}

func TestSanitizeStatusErrorRedactsAuthSchemeCredentials(t *testing.T) {
	const secret = "dXNlcjpwYXNzd29yZA=="
	cases := []struct {
		name  string
		input string
	}{
		{"basic header", "request failed: Authorization: Basic " + secret},
		{"basic json", `{"Authorization":"Basic ` + secret + `"}`},
		{"token header", "upstream rejected: Authorization: Token " + secret},
		{"scheme-less authorization", "authorization=" + secret},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeStatusError(tc.input)
			if strings.Contains(got, secret) {
				t.Fatalf("凭据泄露: %q -> %q", tc.input, got)
			}
			if !strings.Contains(got, "[redacted]") {
				t.Fatalf("未屏蔽凭据: %q -> %q", tc.input, got)
			}
		})
	}
}

func TestSanitizeStatusErrorRedactsStandaloneAuthCredentials(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		secret string
	}{
		{"bearer prose", "upstream rejected Bearer test-secret", "test-secret"},
		{"bearer json message", `{"message":"upstream rejected Bearer test-secret; retry later"}`, "test-secret"},
		{"basic prose", "upstream rejected Basic dXNlcjpwYXNz", "dXNlcjpwYXNz"},
		{"basic json message", `{"message":"upstream rejected Basic dXNlcjpwYXNz; retry later"}`, "dXNlcjpwYXNz"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeStatusError(tc.input)
			if strings.Contains(got, tc.secret) {
				t.Fatalf("独立认证凭据泄露: %q -> %q", tc.input, got)
			}
			if !strings.Contains(got, "[redacted]") {
				t.Fatalf("独立认证凭据未屏蔽: %q -> %q", tc.input, got)
			}
		})
	}
}

func TestSanitizeStatusErrorRedactsCompleteSensitiveFields(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		secrets []string
	}{
		{
			name:    "digest parameters",
			input:   `Authorization: Digest username="private-user", realm="private-realm", nonce="private-nonce", uri="/private", response="private-response", algorithm=MD5`,
			secrets: []string{"private-user", "private-realm", "private-nonce", "/private", "private-response", "MD5"},
		},
		{
			name:    "json password with spaces",
			input:   `{"password":"first-word remaining private words","message":"keep this"}`,
			secrets: []string{"first-word", "remaining private words"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeStatusError(tc.input)
			for _, secret := range tc.secrets {
				if strings.Contains(got, secret) {
					t.Fatalf("敏感字段泄露 %q: %q", secret, got)
				}
			}
			if !strings.Contains(got, "[redacted]") {
				t.Fatalf("敏感字段未屏蔽: %q", got)
			}
			if tc.name == "json password with spaces" && !strings.Contains(got, `"message":"keep this"`) {
				t.Fatalf("屏蔽密码时破坏了后续 JSON 字段: %q", got)
			}
		})
	}
}

// TestStatusSnapshotReconcilesStaleTelemetry 校验历史请求遥测不会覆盖当前渠道状态:
// 渠道被删除, 禁用或切换后, 快照须反映当前路由而非陈旧的历史渠道与健康状态。
func TestStatusSnapshotReconcilesStaleTelemetry(t *testing.T) {
	t.Run("channel deleted", func(t *testing.T) {
		channelID, group, _ := newChannelRouteFixture(t)
		channelName := group.Items[0].ChannelName
		if channelName == "" {
			t.Fatal("测试渠道名称为空")
		}
		assertStatusReflectsChannel(t, group.ID, channelName, relay.ChannelStatusNormal)

		request := httptest.NewRequest(http.MethodDelete, "/delete/"+strconv.Itoa(channelID), nil)
		serveHandler(t, http.MethodDelete, "/delete/:id", deleteChannel, request)

		after := findGroupStatus(t, group.ID)
		if after.CurrentChannel == channelName {
			t.Fatalf("删除渠道后快照仍显示旧渠道: %+v", after)
		}
		if after.ChannelStatus == relay.ChannelStatusNormal {
			t.Fatalf("删除渠道后快照仍显示旧渠道为 normal: %+v", after)
		}
	})

	t.Run("channel disabled", func(t *testing.T) {
		channelID, group, _ := newChannelRouteFixture(t)
		manualMode := model.GroupModeManual
		activeItemID := group.Items[0].ID
		manualGroup, err := op.GroupUpdate(group.ID, &model.GroupUpdateRequest{Mode: &manualMode, ActiveItemID: &activeItemID}, t.Context())
		if err != nil {
			t.Fatalf("切换为手动模式失败: %v", err)
		}
		relay.ResetRouteStateForGroup(*manualGroup)
		group = *manualGroup
		channelName := group.Items[0].ChannelName
		if channelName == "" {
			t.Fatal("测试渠道名称为空")
		}
		assertStatusReflectsChannel(t, group.ID, channelName, relay.ChannelStatusNormal)

		body := fmt.Sprintf(`{"id":%d,"enabled":false}`, channelID)
		request := httptest.NewRequest(http.MethodPost, "/enable", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		serveHandler(t, http.MethodPost, "/enable", enableChannel, request)

		after := findGroupStatus(t, group.ID)
		if after.ChannelStatus == relay.ChannelStatusNormal {
			t.Fatalf("禁用渠道后快照仍显示旧渠道为 normal: %+v", after)
		}
	})

	t.Run("manual selection cleared", func(t *testing.T) {
		_, group, _ := newChannelRouteFixture(t)
		manualMode := model.GroupModeManual
		activeItemID := group.Items[0].ID
		manualGroup, err := op.GroupUpdate(group.ID, &model.GroupUpdateRequest{Mode: &manualMode, ActiveItemID: &activeItemID}, t.Context())
		if err != nil {
			t.Fatalf("切换为手动模式失败: %v", err)
		}
		relay.ResetRouteStateForGroup(*manualGroup)
		group = *manualGroup
		channelName := group.Items[0].ChannelName
		assertStatusReflectsChannel(t, group.ID, channelName, relay.ChannelStatusNormal)

		noSelection := 0
		if _, err := op.GroupUpdate(group.ID, &model.GroupUpdateRequest{ActiveItemID: &noSelection}, t.Context()); err != nil {
			t.Fatalf("取消手动成员选择失败: %v", err)
		}

		after := findGroupStatus(t, group.ID)
		if after.CurrentChannel != "" || after.ChannelStatus == relay.ChannelStatusNormal {
			t.Fatalf("取消选择后快照仍显示历史渠道状态: %+v", after)
		}
	})
}

func findGroupStatus(t *testing.T, groupID int) StatusSnapshot {
	t.Helper()
	for _, item := range statusSnapshot() {
		if item.GroupID == groupID {
			return item
		}
	}
	t.Fatalf("状态快照缺少分组 %d", groupID)
	return StatusSnapshot{}
}

func assertStatusReflectsChannel(t *testing.T, groupID int, channel string, status relay.ChannelStatus) {
	t.Helper()
	item := findGroupStatus(t, groupID)
	if item.CurrentChannel != channel || item.ChannelStatus != status {
		t.Fatalf("快照未反映当前渠道: got=%+v want channel=%q status=%q", item, channel, status)
	}
}

func TestTelemetryReflectsRouteDistinguishesManualSelectionFromFailoverWait(t *testing.T) {
	group := model.Group{
		Mode: model.GroupModeManual,
		Items: []model.GroupItem{{
			ID:          7,
			ChannelName: "status-channel",
			Available:   false,
		}},
	}
	state := relay.RouteState{CurrentItemID: 7}
	if telemetryReflectsRoute(group, state, "status-channel") {
		t.Fatal("禁用的当前成员仍接受历史遥测")
	}

	group.Items[0].Available = true
	if !telemetryReflectsRoute(group, state, "status-channel") {
		t.Fatal("当前可用成员未接受匹配遥测")
	}

	state.CurrentItemID = 0
	if telemetryReflectsRoute(group, state, "status-channel") {
		t.Fatal("手动模式取消选择后仍接受历史遥测")
	}

	group.Mode = model.GroupModeFailover
	if !telemetryReflectsRoute(group, state, "status-channel") {
		t.Fatal("故障转移等待状态未接受可用成员的遥测")
	}
}

// TestStatusSnapshotReflectsManualChannelSwitch 覆盖遥测渠道与当前路由不一致的场景:
// 手动切换到另一个可用渠道后, 快照须指向新渠道, 而非停留在历史请求的旧渠道与 normal 状态。
func TestStatusSnapshotReflectsManualChannelSwitch(t *testing.T) {
	_, group, _ := newChannelRouteFixture(t)
	channel1 := group.Items[0].ChannelName
	grant1 := group.Items[0].ChannelGrantID
	assertStatusReflectsChannel(t, group.ID, channel1, relay.ChannelStatusNormal)

	_, grant2, _ := createStatusTestChannel(t)

	manual := model.GroupModeManual
	items := []model.GroupItemInput{{ChannelGrantID: grant1}, {ChannelGrantID: grant2}}
	reshaped, err := op.GroupUpdate(group.ID, &model.GroupUpdateRequest{Mode: &manual, Items: &items}, t.Context())
	if err != nil {
		t.Fatalf("切换为手动模式失败: %v", err)
	}
	var member2 int
	var channel2 string
	for _, item := range reshaped.Items {
		if item.ChannelGrantID == grant2 {
			member2 = item.ID
			channel2 = item.ChannelName
		}
	}
	if member2 == 0 || channel2 == "" {
		t.Fatalf("第二成员未建立: %+v", reshaped.Items)
	}

	activated, err := op.GroupUpdate(group.ID, &model.GroupUpdateRequest{ActiveItemID: &member2}, t.Context())
	if err != nil {
		t.Fatalf("手动切换成员失败: %v", err)
	}
	relay.ResetRouteStateForGroup(*activated)

	after := findGroupStatus(t, group.ID)
	if after.CurrentChannel != channel2 {
		t.Fatalf("手动切换后快照未指向新渠道: got=%+v want channel=%q", after, channel2)
	}
	if after.CurrentChannel == channel1 || after.ChannelStatus == relay.ChannelStatusNormal {
		t.Fatalf("手动切换后快照仍显示旧渠道状态: %+v", after)
	}
}

// createStatusTestChannel 建立一个可用但无需上游可达的测试渠道, 仅用于作为分组成员参与选路展示。
func createStatusTestChannel(t *testing.T) (channelID int, grantID int, channelName string) {
	t.Helper()
	channelName = fmt.Sprintf("status-channel-%d", channelEventTestID.Add(1))
	detail := &model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{
			Name:                     channelName,
			Enabled:                  true,
			BaseURL:                  "http://127.0.0.1:1",
			OpenAIChatCompletionPath: "/v1/chat/completions",
		},
		Keys:   []model.ChannelKeyConfig{{Name: "key", Key: "test-credential", Enabled: true}},
		Models: []string{"upstream-model"},
		Grants: []model.ChannelGrantConfig{{ModelName: "upstream-model", KeyName: "key", Protocols: model.ProtocolOpenAIChatCompletion}},
	}
	created, err := op.ChannelCreate(detail, t.Context())
	if err != nil {
		t.Fatalf("建立测试渠道失败: %v", err)
	}
	for _, candidate := range op.ChannelGrantCandidates() {
		if candidate.ChannelID == created.ID && candidate.ModelName == "upstream-model" {
			grantID = candidate.ID
			break
		}
	}
	if grantID == 0 {
		t.Fatal("测试渠道未产生可用授权")
	}
	return created.ID, grantID, channelName
}

type statusFrame struct {
	event string
	data  string
}

// startStatusStreamReader 持续解析 SSE 帧并投递到通道; 连接关闭时关闭 done。
func startStatusStreamReader(reader *bufio.Reader) (<-chan statusFrame, <-chan struct{}) {
	frames := make(chan statusFrame, 64)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(frames)
		var event, data string
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			switch {
			case line == "":
				if event != "" || data != "" {
					frames <- statusFrame{event: event, data: data}
					event, data = "", ""
				}
			case strings.HasPrefix(line, "event:"):
				event = strings.TrimSpace(line[len("event:"):])
			case strings.HasPrefix(line, "data:"):
				data = strings.TrimSpace(line[len("data:"):])
			}
		}
	}()
	return frames, done
}

func awaitStatusGroup(t *testing.T, frames <-chan statusFrame, groupID int, name string, timeout time.Duration) StatusSnapshot {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case frame, ok := <-frames:
			if !ok {
				t.Fatalf("SSE 连接在收到分组 %d 状态前关闭", groupID)
			}
			if frame.event != "status" {
				continue
			}
			var item StatusSnapshot
			if err := json.Unmarshal([]byte(frame.data), &item); err != nil {
				continue
			}
			if item.GroupID == groupID && (name == "" || item.GroupName == name) {
				return item
			}
		case <-deadline:
			t.Fatalf("等待分组 %d(name=%q) 的 status 推送超时", groupID, name)
		}
	}
}

func awaitStatusRemoved(t *testing.T, frames <-chan statusFrame, groupID int, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case frame, ok := <-frames:
			if !ok {
				t.Fatalf("SSE 连接在收到分组 %d 删除通知前关闭", groupID)
			}
			if frame.event != "status_removed" {
				continue
			}
			var removed struct {
				GroupID int `json:"group_id"`
			}
			if err := json.Unmarshal([]byte(frame.data), &removed); err != nil {
				continue
			}
			if removed.GroupID == groupID {
				return
			}
		case <-deadline:
			t.Fatalf("等待分组 %d 的 status_removed 推送超时", groupID)
		}
	}
}

// TestStatusEventsPushGroupLifecycle 验证在没有模型请求时, 分组的创建, 修改与删除都能通过 SSE 及时推送,
// 且客户端能通过 status_removed 明确获知分组删除。
func TestStatusEventsPushGroupLifecycle(t *testing.T) {
	server, client, adminToken, statusPlaintext := newStatusStreamServer(t, 0)

	streamCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	frames, _ := openStatusEventStream(t, streamCtx, client, server.URL, statusPlaintext)

	groupName := fmt.Sprintf("status-lifecycle-%d", channelEventTestID.Add(1))
	createBody := fmt.Sprintf(`{"name":%q,"mode":%q}`, groupName, model.GroupModeFailover)
	groupID := createGroupViaHTTP(t, client, server.URL, adminToken, createBody)
	awaitStatusGroup(t, frames, groupID, groupName, 5*time.Second)

	renamed := groupName + "-renamed"
	groupMutation(t, client, server.URL, adminToken, http.MethodPost, "/api/v1/group/update/"+strconv.Itoa(groupID), fmt.Sprintf(`{"name":%q}`, renamed), http.StatusOK)
	awaitStatusGroup(t, frames, groupID, renamed, 5*time.Second)

	groupMutation(t, client, server.URL, adminToken, http.MethodDelete, "/api/v1/group/delete/"+strconv.Itoa(groupID), "", http.StatusOK)
	awaitStatusRemoved(t, frames, groupID, 5*time.Second)
}

// newStatusTestEngine 直接注册本测试所需的路由与中间件。
// 不走 router.RegisterAll: 它会在首次调用后清空全局路由注册表, 多个测试各自调用时只有第一个能拿到路由。
func newStatusTestEngine() *gin.Engine {
	engine := gin.New()
	statusRoutes := engine.Group("/api/v1/status", middleware.StatusToken())
	statusRoutes.GET("/events", streamStatusEvents)
	statusRoutes.GET("/snapshot", getStatusSnapshot)
	groupRoutes := engine.Group("/api/v1/group", middleware.Auth(), middleware.RequireJSON())
	groupRoutes.POST("/create", createGroup)
	groupRoutes.POST("/update/:id", updateGroup)
	groupRoutes.DELETE("/delete/:id", deleteGroup)
	return engine
}

// newStatusStreamServer 启动测试服务器, 并返回管理令牌与一个有效期为 expiresAt 的 status token 明文。
func newStatusStreamServer(t *testing.T, expiresAt int64) (*httptest.Server, *http.Client, string, string) {
	t.Helper()
	server := httptest.NewServer(newStatusTestEngine())
	t.Cleanup(server.Close)

	adminToken, _, err := serverauth.GenerateJWTToken(60)
	if err != nil {
		t.Fatalf("生成管理令牌失败: %v", err)
	}
	token, plaintext, err := op.StatusTokenCreate(t.Context(), "status stream test", expiresAt)
	if err != nil {
		t.Fatalf("创建 status token 失败: %v", err)
	}
	t.Cleanup(func() { _ = op.StatusTokenRevoke(context.Background(), token.ID) })
	return server, &http.Client{}, adminToken, plaintext
}

func openStatusEventStream(t *testing.T, ctx context.Context, client *http.Client, serverURL, plaintext string) (<-chan statusFrame, <-chan struct{}) {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL+"/api/v1/status/events", nil)
	if err != nil {
		t.Fatalf("构造 status 事件流请求失败: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+plaintext)
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("请求 status 事件流失败: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		t.Fatalf("status 事件流返回 %d, 期望 %d", response.StatusCode, http.StatusOK)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return startStatusStreamReader(bufio.NewReader(response.Body))
}

func createGroupViaHTTP(t *testing.T, client *http.Client, serverURL, adminToken, body string) int {
	t.Helper()
	response := groupMutation(t, client, serverURL, adminToken, http.MethodPost, "/api/v1/group/create", body, http.StatusOK)
	defer response.Body.Close()
	var envelope struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatalf("解析创建分组响应失败: %v", err)
	}
	if envelope.Data.ID == 0 {
		t.Fatal("创建分组响应缺少分组 ID")
	}
	return envelope.Data.ID
}

func groupMutation(t *testing.T, client *http.Client, serverURL, adminToken, method, path, body string, wantStatus int) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request, err := http.NewRequest(method, serverURL+path, reader)
	if err != nil {
		t.Fatalf("构造分组请求失败 (%s %s): %v", method, path, err)
	}
	request.AddCookie(&http.Cookie{Name: "auth", Value: adminToken})
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("发送分组请求失败 (%s %s): %v", method, path, err)
	}
	if response.StatusCode != wantStatus {
		payload, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		t.Fatalf("分组请求 %s %s 返回 %d, 期望 %d: %s", method, path, response.StatusCode, wantStatus, payload)
	}
	return response
}

// TestStatusEventsCloseOnTokenRevocationAndExpiry 确认 Token 撤销与到期都会及时关闭 SSE 连接,
// 新增的分组事件订阅不应影响这两条退出路径。
func TestStatusEventsCloseOnTokenRevocationAndExpiry(t *testing.T) {
	server := httptest.NewServer(newStatusTestEngine())
	defer server.Close()
	client := &http.Client{}

	t.Run("revocation", func(t *testing.T) {
		token, plaintext, err := op.StatusTokenCreate(t.Context(), "revoke close", 0)
		if err != nil {
			t.Fatalf("创建 status token 失败: %v", err)
		}
		t.Cleanup(func() { _ = op.StatusTokenRevoke(context.Background(), token.ID) })

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_, done := openStatusEventStream(t, ctx, client, server.URL, plaintext)

		if err := op.StatusTokenRevoke(t.Context(), token.ID); err != nil {
			t.Fatalf("撤销 status token 失败: %v", err)
		}
		awaitStreamClosed(t, done, 3*time.Second)
	})

	t.Run("expiry", func(t *testing.T) {
		token, plaintext, err := op.StatusTokenCreate(t.Context(), "expiry close", time.Now().Add(2*time.Second).Unix())
		if err != nil {
			t.Fatalf("创建到期 status token 失败: %v", err)
		}
		t.Cleanup(func() { _ = op.StatusTokenRevoke(context.Background(), token.ID) })

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_, done := openStatusEventStream(t, ctx, client, server.URL, plaintext)

		awaitStreamClosed(t, done, 5*time.Second)
	})
}

func awaitStreamClosed(t *testing.T, done <-chan struct{}, timeout time.Duration) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatal("SSE 连接未按预期关闭")
	}
}
