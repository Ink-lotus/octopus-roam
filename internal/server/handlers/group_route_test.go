package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/relay"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)

	dir, err := os.MkdirTemp("", "octopus-handler-test")
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

var channelEventTestID atomic.Int64

func TestRouteConfigChangedTracksPolicyAndMembersButIgnoresAffinity(t *testing.T) {
	base := model.Group{
		Name:        "group",
		Mode:        model.GroupModeFailover,
		RelayConfig: model.DefaultGroupRelayConfig(),
		Items: []model.GroupItem{
			{ID: 1, ChannelGrantID: 101, Priority: 1},
			{ID: 2, ChannelGrantID: 102, Priority: 2},
		},
	}

	tests := []struct {
		name string
		edit func(*model.Group)
		want bool
	}{
		{name: "unchanged", want: false},
		{name: "mode", edit: func(group *model.Group) { group.Mode = model.GroupModeManual }, want: true},
		{name: "member order", edit: func(group *model.Group) { group.Items[0], group.Items[1] = group.Items[1], group.Items[0] }, want: true},
		{name: "relay policy", edit: func(group *model.Group) { group.RelayConfig.MemberCooldownSeconds++ }, want: true},
		{name: "manual selection", edit: func(group *model.Group) {
			group.Mode = model.GroupModeManual
			group.ActiveItemID = 1
		}, want: true},
		{name: "legacy affinity", edit: func(group *model.Group) { group.RelayConfig.MemberAffinitySeconds++ }, want: false},
		{name: "name only", edit: func(group *model.Group) { group.Name = "renamed" }, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			updated := base
			updated.Items = append([]model.GroupItem(nil), base.Items...)
			if test.edit != nil {
				test.edit(&updated)
			}
			if got := routeConfigChanged(base, updated); got != test.want {
				t.Fatalf("routeConfigChanged() = %v, 期望 %v", got, test.want)
			}
		})
	}
}

func TestChannelMutationsPublishCurrentGroupSnapshotToEverySSESubscriber(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, int)
		check  func(*testing.T, groupResponse, int)
	}{
		{
			name: "enable",
			mutate: func(t *testing.T, channelID int) {
				request := httptest.NewRequest(http.MethodPost, "/enable", strings.NewReader(fmt.Sprintf(`{"id":%d,"enabled":false}`, channelID)))
				request.Header.Set("Content-Type", "application/json")
				serveHandler(t, http.MethodPost, "/enable", enableChannel, request)
			},
			check: assertUnavailableMemberSnapshot,
		},
		{
			name: "update",
			mutate: func(t *testing.T, channelID int) {
				detail, err := op.ChannelDetailGet(channelID)
				if err != nil {
					t.Fatalf("读取测试渠道失败: %v", err)
				}
				detail.ChannelConfig.Enabled = false
				body, err := json.Marshal(detail)
				if err != nil {
					t.Fatalf("编码测试渠道失败: %v", err)
				}
				request := httptest.NewRequest(http.MethodPost, "/update", bytes.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				serveHandler(t, http.MethodPost, "/update", updateChannel, request)
			},
			check: assertUnavailableMemberSnapshot,
		},
		{
			name: "delete",
			mutate: func(t *testing.T, channelID int) {
				request := httptest.NewRequest(http.MethodDelete, "/delete/"+strconv.Itoa(channelID), nil)
				serveHandler(t, http.MethodDelete, "/delete/:id", deleteChannel, request)
			},
			check: func(t *testing.T, snapshot groupResponse, _ int) {
				if len(snapshot.Items) != 0 {
					t.Fatalf("删除渠道后的成员仍存在: %+v", snapshot.Items)
				}
				if snapshot.Runtime.CurrentItemID != 0 {
					t.Fatalf("删除渠道后当前成员仍指向已删除成员: %+v", snapshot.Runtime)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			channelID, group, itemID := newChannelRouteFixture(t)
			if state := relay.RouteStateOf(group); state.CurrentItemID != itemID {
				t.Fatalf("测试前未建立当前路由: %+v", state)
			}

			firstSubscriber := subscribeGroupEventForTest(t)
			secondSubscriber := subscribeGroupEventForTest(t)
			test.mutate(t, channelID)

			for index, subscriber := range []chan groupEvent{firstSubscriber, secondSubscriber} {
				select {
				case event := <-subscriber:
					if event.Name != "changed" {
						t.Fatalf("订阅者 %d 收到事件 %q, 期望 changed", index+1, event.Name)
					}
					snapshot, ok := event.Data.(groupResponse)
					if !ok {
						t.Fatalf("订阅者 %d 收到的事件不是完整分组快照: %T", index+1, event.Data)
					}
					if snapshot.ID != group.ID {
						t.Fatalf("订阅者 %d 收到分组 ID %d, 期望 %d", index+1, snapshot.ID, group.ID)
					}
					test.check(t, snapshot, itemID)
					if current := relay.RouteStateOf(snapshot.Group); !sameRouteState(snapshot.Runtime, current) {
						t.Fatalf("订阅者 %d 快照运行状态与当前 Relay 状态不一致: event=%+v current=%+v", index+1, snapshot.Runtime, current)
					}
					for _, item := range snapshot.Items {
						if item.ID == snapshot.Runtime.CurrentItemID && !item.Available {
							t.Fatalf("订阅者 %d 收到不可用成员仍为当前路由: member=%+v runtime=%+v", index+1, item, snapshot.Runtime)
						}
					}
				case <-time.After(2 * time.Second):
					t.Fatalf("订阅者 %d 未收到渠道变更后的分组快照", index+1)
				}
			}
		})
	}
}

func newChannelRouteFixture(t *testing.T) (channelID int, group model.Group, itemID int) {
	t.Helper()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-test","object":"chat.completion","created":1,"model":"upstream","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	t.Cleanup(upstream.Close)

	name := fmt.Sprintf("sse-channel-%d", channelEventTestID.Add(1))
	detail := &model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{
			Name:                     name,
			Enabled:                  true,
			BaseURL:                  upstream.URL,
			OpenAIChatCompletionPath: "/v1/chat/completions",
			OpenAIResponsePath:       "/v1/responses",
			AnthropicMessagePath:     "/v1/messages",
		},
		Keys:   []model.ChannelKeyConfig{{Name: "key", Key: "test-credential", Enabled: true}},
		Models: []string{"upstream-model"},
		Grants: []model.ChannelGrantConfig{{ModelName: "upstream-model", KeyName: "key", Protocols: model.ProtocolOpenAIChatCompletion}},
	}
	created, err := op.ChannelCreate(detail, t.Context())
	if err != nil {
		t.Fatalf("建立测试渠道失败: %v", err)
	}
	channelID = created.ID
	var grantID int
	for _, candidate := range op.ChannelGrantCandidates() {
		if candidate.ChannelID == channelID && candidate.ModelName == "upstream-model" {
			grantID = candidate.ID
			break
		}
	}
	if grantID == 0 {
		t.Fatal("测试渠道未产生可用授权")
	}

	groupName := fmt.Sprintf("sse-group-%d", channelEventTestID.Add(1))
	config := model.DefaultGroupRelayConfig()
	config.MemberMaxAttempts = 1
	createdGroup, err := op.GroupCreate(&model.GroupCreateRequest{
		Name:        groupName,
		Mode:        model.GroupModeFailover,
		RelayConfig: config,
		Items:       []model.GroupItemInput{{ChannelGrantID: grantID}},
	}, t.Context())
	if err != nil {
		t.Fatalf("建立测试分组失败: %v", err)
	}
	group = *createdGroup
	itemID = group.Items[0].ID
	relay.SetRouteConfig(group)
	t.Cleanup(func() { relay.ResetRouteState(group.ID) })

	engine := gin.New()
	engine.POST("/forward", relay.Forward(llm.APIFormatOpenAIChatCompletion))
	requestBody := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"ping"}]}`, group.Name)
	request := httptest.NewRequest(http.MethodPost, "/forward", strings.NewReader(requestBody))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		engine.ServeHTTP(response, request)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("建立测试当前路由超时")
	}
	if response.Code != http.StatusOK {
		t.Fatalf("建立测试当前路由返回 %d: %s", response.Code, response.Body.String())
	}
	return channelID, group, itemID
}

func subscribeGroupEventForTest(t *testing.T) chan groupEvent {
	t.Helper()
	stream := make(chan groupEvent, groupEventBuffer)
	groupEventMu.Lock()
	groupEventStreams[stream] = struct{}{}
	groupEventMu.Unlock()
	t.Cleanup(func() {
		groupEventMu.Lock()
		defer groupEventMu.Unlock()
		if _, exists := groupEventStreams[stream]; exists {
			delete(groupEventStreams, stream)
			close(stream)
		}
	})
	return stream
}

func serveHandler(t *testing.T, method, path string, handler gin.HandlerFunc, request *http.Request) {
	t.Helper()
	engine := gin.New()
	engine.Handle(method, path, handler)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("处理器返回 %d: %s", response.Code, response.Body.String())
	}
}

func assertUnavailableMemberSnapshot(t *testing.T, snapshot groupResponse, itemID int) {
	t.Helper()
	if len(snapshot.Items) != 1 || snapshot.Items[0].ID != itemID || snapshot.Items[0].Available {
		t.Fatalf("渠道不可用后的成员快照不匹配: %+v", snapshot.Items)
	}
	if snapshot.Runtime.CurrentItemID != 0 {
		t.Fatalf("渠道不可用后当前路由仍指向该成员: %+v", snapshot.Runtime)
	}
}

func sameRouteState(left, right relay.RouteState) bool {
	if left.GroupID != right.GroupID || left.CurrentItemID != right.CurrentItemID || left.ProbeItemID != right.ProbeItemID || left.AffinityUntil != right.AffinityUntil || len(left.Cooldowns) != len(right.Cooldowns) {
		return false
	}
	for id, deadline := range left.Cooldowns {
		if right.Cooldowns[id] != deadline {
			return false
		}
	}
	return true
}
