package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/relay"
	serverauth "github.com/bestruirui/octopus/internal/server/auth"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

func TestRuntimeEndpointsRequireAuthAndShareSnapshotContract(t *testing.T) {
	engine := gin.New()
	if err := router.RegisterAll(engine); err != nil {
		t.Fatalf("注册 HTTP 路由失败: %v", err)
	}
	server := httptest.NewServer(engine)
	defer server.Close()

	unauthorized, err := http.Get(server.URL + "/api/v1/runtime")
	if err != nil {
		t.Fatalf("请求未认证的 runtime 快照失败: %v", err)
	}
	_ = unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未认证快照返回 %d, 期望 %d", unauthorized.StatusCode, http.StatusUnauthorized)
	}

	token, _, err := serverauth.GenerateJWTToken(60)
	if err != nil {
		t.Fatalf("生成测试认证令牌失败: %v", err)
	}
	client := &http.Client{}
	snapshotRequest, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/runtime", nil)
	if err != nil {
		t.Fatalf("构造 runtime 快照请求失败: %v", err)
	}
	snapshotRequest.AddCookie(&http.Cookie{Name: "auth", Value: token})
	snapshotResponse, err := client.Do(snapshotRequest)
	if err != nil {
		t.Fatalf("请求 runtime 快照失败: %v", err)
	}
	defer snapshotResponse.Body.Close()
	if snapshotResponse.StatusCode != http.StatusOK {
		t.Fatalf("runtime 快照返回 %d, 期望 %d", snapshotResponse.StatusCode, http.StatusOK)
	}
	var envelope struct {
		Data []relay.GroupRuntime `json:"data"`
	}
	if err := json.NewDecoder(snapshotResponse.Body).Decode(&envelope); err != nil {
		t.Fatalf("解析 runtime 快照失败: %v", err)
	}

	streamContext, cancelStream := context.WithCancel(context.Background())
	defer cancelStream()
	streamRequest, err := http.NewRequestWithContext(streamContext, http.MethodGet, server.URL+"/api/v1/runtime/stream", nil)
	if err != nil {
		t.Fatalf("构造 runtime 流请求失败: %v", err)
	}
	streamRequest.AddCookie(&http.Cookie{Name: "auth", Value: token})
	streamResponse, err := client.Do(streamRequest)
	if err != nil {
		t.Fatalf("请求 runtime 流失败: %v", err)
	}
	if streamResponse.StatusCode != http.StatusOK {
		_ = streamResponse.Body.Close()
		t.Fatalf("runtime 流返回 %d, 期望 %d", streamResponse.StatusCode, http.StatusOK)
	}
	if !strings.HasPrefix(streamResponse.Header.Get("Content-Type"), "text/event-stream") {
		_ = streamResponse.Body.Close()
		t.Fatalf("runtime 流 Content-Type = %q", streamResponse.Header.Get("Content-Type"))
	}
	eventName, data := readRuntimeSSEEvent(t, bufio.NewReader(streamResponse.Body))
	cancelStream()
	_ = streamResponse.Body.Close()
	if eventName != "runtime" {
		t.Fatalf("初始 SSE 事件名 = %q, 期望 runtime", eventName)
	}
	var streamed []relay.GroupRuntime
	if err := json.Unmarshal([]byte(data), &streamed); err != nil {
		t.Fatalf("解析 runtime SSE 初始快照失败: %v", err)
	}
	if len(streamed) != len(envelope.Data) {
		t.Fatalf("快照接口与 SSE 初始快照长度不一致: GET=%+v SSE=%+v", envelope.Data, streamed)
	}
	for i := range streamed {
		if streamed[i] != envelope.Data[i] {
			t.Fatalf("快照接口与 SSE 初始快照不一致: GET=%+v SSE=%+v", envelope.Data, streamed)
		}
	}
}

func readRuntimeSSEEvent(t *testing.T, reader *bufio.Reader) (string, string) {
	t.Helper()
	var eventName, data string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("读取 runtime SSE 事件失败: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			return eventName, data
		case strings.HasPrefix(line, "event:"):
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		}
	}
}
