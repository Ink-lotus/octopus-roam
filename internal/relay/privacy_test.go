package relay

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/privacy"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
)

func assertDiagnosticPrivate(t *testing.T, text string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if strings.Contains(text, secret) {
			t.Fatalf("diagnostic contains a fixture credential: %s", text)
		}
	}
}

func latestPrivacyRequest(t *testing.T, groupID int) RequestState {
	t.Helper()
	snapshot, stream := OpenRequestStream()
	CloseRequestStream(stream)
	for _, state := range snapshot {
		if state.GroupID == groupID {
			return state
		}
	}
	t.Fatal("request snapshot missing")
	return RequestState{}
}

func TestRelayErrorsUseSameRedactionAcrossProtocolsAndDiagnostics(t *testing.T) {
	for _, format := range []llm.APIFormat{llm.APIFormatOpenAIChatCompletion, llm.APIFormatOpenAIResponse, llm.APIFormatAnthropicMessage} {
		t.Run(string(format), func(t *testing.T) {
			const inboundSecret, customSecret, bodySecret = "fixture-inbound-key", "fixture-custom-value", "fixture-body-password"
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer test-upstream-credential" || r.Header.Get("X-Private-Custom") != customSecret {
					t.Error("redaction changed upstream credentials")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				message := "denied test-upstream-credential " + inboundSecret + " " + customSecret + " " + bodySecret + " at https://fixture-user:fixture-url-password@example.test/v1?api_key=fixture-query-key"
				json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": message, "type": "authentication_error"}})
			}))
			defer upstream.Close()
			grantID := newTestChannel(t, testName(t, "channel"), upstream.URL, "upstream-model")
			channelID := channelIDOfGrant(t, grantID)
			detail, err := op.ChannelDetailGet(channelID)
			if err != nil {
				t.Fatal(err)
			}
			detail.CustomHeader = []model.CustomHeader{{HeaderKey: "X-Private-Custom", HeaderValue: "{client_header:X-Custom-Input}"}}
			if _, err := op.ChannelUpdate(&detail, t.Context()); err != nil {
				t.Fatal(err)
			}
			config := testRelayConfig(1)
			config.ManualMaxAttempts = 1
			name := newTestGroup(t, model.GroupModeManual, config, grantID)
			group := manualWaitGroup(t, name)
			body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"ordinary prompt"}],"input":"ordinary prompt","max_tokens":64,"metadata":{"password":%q}}`, name, bodySecret)
			request := httptest.NewRequest(http.MethodPost, "/relay", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer "+inboundSecret)
			request.Header.Set("X-Custom-Input", customSecret)
			engine := gin.New()
			engine.POST("/relay", Forward(format))
			response := httptest.NewRecorder()
			serveWithTimeout(t, engine, response, request)
			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("unexpected error status: %d: %s", response.Code, response.Body)
			}
			state := latestPrivacyRequest(t, group.ID)
			encoded, _ := json.Marshal(state)
			runtime, _ := json.Marshal(RuntimeSnapshot())
			secrets := []string{"test-upstream-credential", inboundSecret, customSecret, bodySecret, "fixture-user", "fixture-url-password", "fixture-query-key"}
			for _, diagnostic := range []string{response.Body.String(), string(encoded), string(runtime), RequestBody(state.ID)} {
				assertDiagnosticPrivate(t, diagnostic, secrets...)
			}
			if !strings.Contains(state.LastError, "denied") || !strings.Contains(state.LastError, privacy.Mask("test-upstream-credential")) {
				t.Fatalf("error lost context/fingerprint: %s", state.LastError)
			}
			if !strings.Contains(response.Body.String(), privacy.Mask("test-upstream-credential")) {
				t.Fatal("response uses a different fingerprint")
			}
			if !strings.Contains(RequestBody(state.ID), "ordinary prompt") {
				t.Fatal("request body lost ordinary content")
			}
		})
	}
}

func TestDiagnosticBodyRedactionDoesNotChangeSuccessfulForwarding(t *testing.T) {
	const bodySecret = "fixture-success-body-key"
	responseBody := strings.Replace(chatCompletionBody, `"content":"pong"`, `"content":"pong test-upstream-credential","api_key":"fixture-response-key"`, 1)
	received := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- string(body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, responseBody)
	}))
	defer upstream.Close()
	grantID := newTestChannel(t, testName(t, "channel"), upstream.URL, "upstream-model")
	name := newTestGroup(t, model.GroupModeManual, testRelayConfig(1), grantID)
	group := manualWaitGroup(t, name)
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"ping"}],"metadata":{"api_key":%q}}`, name, bodySecret)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	serveWithTimeout(t, newRelayEngine(), response, request)
	if response.Code != http.StatusOK || response.Body.String() != responseBody {
		t.Fatal("successful forwarding payload was changed")
	}
	if !strings.Contains(<-received, bodySecret) {
		t.Fatal("upstream request body was redacted")
	}
	state := latestPrivacyRequest(t, group.ID)
	// Reading diagnostics after removing the channel must retain its captured secrets.
	if err := op.ChannelDel(channelIDOfGrant(t, grantID), t.Context()); err != nil {
		t.Fatal(err)
	}
	assertDiagnosticPrivate(t, RequestBody(state.ID), bodySecret)
	assertDiagnosticPrivate(t, ResponseBody(state.ID), "test-upstream-credential", "fixture-response-key")
	if !strings.Contains(ResponseBody(state.ID), "pong") {
		t.Fatal("response diagnostic lost ordinary output")
	}
}

func TestCommittedStreamErrorIsRedacted(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"model\":\"upstream\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\n")
		w.(http.Flusher).Flush()
		io.WriteString(w, "data: {\"error\":{\"message\":\"denied test-upstream-credential\",\"type\":\"server_error\"}}\n\n")
	}))
	defer upstream.Close()
	grantID := newTestChannel(t, testName(t, "channel"), upstream.URL, "upstream-model")
	name := newTestGroup(t, model.GroupModeManual, testRelayConfig(1), grantID)
	response := httptest.NewRecorder()
	serveWithTimeout(t, newRelayEngine(), response, chatRequest(t, name, true))
	if !strings.Contains(response.Body.String(), "hello") || !strings.Contains(response.Body.String(), "denied") {
		t.Fatalf("stream content/error missing: %s", response.Body)
	}
	assertDiagnosticPrivate(t, response.Body.String(), "test-upstream-credential")
}
