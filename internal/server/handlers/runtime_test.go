package handlers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/relay"
	serverauth "github.com/bestruirui/octopus/internal/server/auth"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/bestruirui/octopus/internal/webhook"
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
	unauthorized, err = http.Get(server.URL + "/api/v1/webhook/list")
	if err != nil {
		t.Fatalf("请求未认证的 webhook 列表失败: %v", err)
	}
	_ = unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未认证 webhook 列表返回 %d, 期望 %d", unauthorized.StatusCode, http.StatusUnauthorized)
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
	beforeTest := relay.RuntimeSnapshot()
	testWebhookCRUDAndDelivery(t, client, server.URL, token)
	if afterTest := relay.RuntimeSnapshot(); !reflect.DeepEqual(beforeTest, afterTest) {
		t.Fatalf("webhook test send changed relay runtime state: before=%+v after=%+v", beforeTest, afterTest)
	}
}

func testWebhookCRUDAndDelivery(t *testing.T, client *http.Client, serverURL, token string) {
	t.Helper()
	type receivedCall struct {
		body          []byte
		authorization string
		secretHeader  string
	}
	received := make(chan receivedCall, 1)
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		received <- receivedCall{body: body, authorization: request.Header.Get("Authorization"), secretHeader: request.Header.Get("X-Notify-Token")}
		writer.WriteHeader(http.StatusAccepted)
	}))
	defer target.Close()

	endpoint := model.WebhookEndpoint{
		Name: "test destination", Enabled: true,
		URL: target.URL + "/notify?token=private-url-token", Method: http.MethodPost,
		ContentType:  "application/json",
		Headers:      map[string]string{"Authorization": "Bearer private-header-secret", "X-Notify-Token": "private-custom-secret"},
		BodyTemplate: webhook.DefaultBodyTemplate, TimeoutSeconds: 2,
	}
	createResponse := webhookAPIRequest(t, client, serverURL, token, http.MethodPost, "/api/v1/webhook/create", endpoint)
	if createResponse.StatusCode != http.StatusOK {
		t.Fatalf("create webhook returned %d", createResponse.StatusCode)
	}
	created := decodeWebhookEndpoint(t, createResponse)
	for _, secret := range []string{"private-url-token", "private-header-secret", "private-custom-secret"} {
		if strings.Contains(created.URL, secret) || strings.Contains(strings.Join(mapValues(created.Headers), " "), secret) {
			t.Fatalf("create response leaked %q: %+v", secret, created)
		}
	}
	masked := webhook.MaskEndpoint(endpoint)
	if created.URL != masked.URL || created.Headers["Authorization"] != masked.Headers["Authorization"] || created.Headers["X-Notify-Token"] != masked.Headers["X-Notify-Token"] {
		t.Fatalf("create response did not mask sensitive values: %+v", created)
	}

	listResponse := webhookAPIRequest(t, client, serverURL, token, http.MethodGet, "/api/v1/webhook/list", nil)
	if listResponse.StatusCode != http.StatusOK {
		t.Fatalf("list webhook returned %d", listResponse.StatusCode)
	}
	var listEnvelope struct {
		Data []model.WebhookEndpoint `json:"data"`
	}
	if err := json.NewDecoder(listResponse.Body).Decode(&listEnvelope); err != nil {
		_ = listResponse.Body.Close()
		t.Fatalf("decode webhook list: %v", err)
	}
	_ = listResponse.Body.Close()
	if len(listEnvelope.Data) != 1 || listEnvelope.Data[0].ID != created.ID {
		t.Fatalf("webhook list = %+v", listEnvelope.Data)
	}

	created.Name = "updated destination"
	updateResponse := webhookAPIRequest(t, client, serverURL, token, http.MethodPost, "/api/v1/webhook/update/"+fmt.Sprint(created.ID), created)
	if updateResponse.StatusCode != http.StatusOK {
		t.Fatalf("update webhook returned %d", updateResponse.StatusCode)
	}
	updated := decodeWebhookEndpoint(t, updateResponse)
	if updated.Name != "updated destination" || updated.URL != created.URL {
		t.Fatalf("update response = %+v", updated)
	}

	testResponse := webhookAPIRequest(t, client, serverURL, token, http.MethodPost, "/api/v1/webhook/test/"+fmt.Sprint(created.ID), struct{}{})
	if testResponse.StatusCode != http.StatusOK {
		t.Fatalf("test webhook returned %d", testResponse.StatusCode)
	}
	var testEnvelope struct {
		Data webhook.SendResult `json:"data"`
	}
	if err := json.NewDecoder(testResponse.Body).Decode(&testEnvelope); err != nil {
		_ = testResponse.Body.Close()
		t.Fatalf("decode webhook test result: %v", err)
	}
	_ = testResponse.Body.Close()
	if !testEnvelope.Data.Success || testEnvelope.Data.StatusCode != http.StatusAccepted {
		t.Fatalf("webhook test result = %+v", testEnvelope.Data)
	}
	select {
	case call := <-received:
		if call.authorization != "Bearer private-header-secret" || call.secretHeader != "private-custom-secret" {
			t.Fatalf("test delivery headers = %+v", call)
		}
		var body map[string]any
		if err := json.Unmarshal(call.body, &body); err != nil || body["event"] != "test" {
			t.Fatalf("test delivery body = %s, err = %v", call.body, err)
		}
	default:
		t.Fatal("test webhook was not delivered")
	}

	deleteResponse := webhookAPIRequest(t, client, serverURL, token, http.MethodDelete, "/api/v1/webhook/delete/"+fmt.Sprint(created.ID), nil)
	_ = deleteResponse.Body.Close()
	if deleteResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("delete webhook returned %d, want %d", deleteResponse.StatusCode, http.StatusNoContent)
	}
}

func webhookAPIRequest(t *testing.T, client *http.Client, serverURL, token, method, path string, body any) *http.Response {
	t.Helper()
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("encode webhook API body: %v", err)
		}
	}
	request, err := http.NewRequest(method, serverURL+path, bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("create webhook API request: %v", err)
	}
	request.AddCookie(&http.Cookie{Name: "auth", Value: token})
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("send webhook API request: %v", err)
	}
	return response
}

func decodeWebhookEndpoint(t *testing.T, response *http.Response) model.WebhookEndpoint {
	t.Helper()
	defer response.Body.Close()
	var envelope struct {
		Data model.WebhookEndpoint `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode webhook endpoint: %v", err)
	}
	return envelope.Data
}

func mapValues(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
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
