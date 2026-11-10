package relay

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
)

func TestCustomHeaderModelOnlyRelayProtocols(t *testing.T) {
	for _, client := range startupProtocols() {
		for _, upstream := range startupProtocols() {
			for _, streaming := range []bool{false, true} {
				for _, modelOnly := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s-to-%s/stream-%t/model-only-%t", client.name, upstream.name, streaming, modelOnly), func(t *testing.T) {
						var calls atomic.Int32
						engine, group := compatRelay(t, upstream.protocol, client.format, func(w http.ResponseWriter, r *http.Request) {
							calls.Add(1)
							wantUA, wantType, wantAdded := "fixture-discovery/1", "configured", "discovery-header"
							if modelOnly {
								wantUA, wantType, wantAdded = "fixture-client/2", "client", ""
							}
							if r.Header.Get("User-Agent") != wantUA || r.Header.Get("X-Client-Type") != wantType || r.Header.Get("X-Discovery-Fixture") != wantAdded {
								t.Error("actual upstream headers did not follow the channel switch")
							}
							if upstream.protocol == model.ProtocolAnthropicMessage {
								if r.Header.Get("X-Api-Key") != "test-upstream-credential" {
									t.Error("channel authentication changed")
								}
							} else if r.Header.Get("Authorization") != "Bearer test-upstream-credential" {
								t.Error("channel authentication changed")
							}
							var body struct {
								Temperature float64
								Stream      bool
							}
							if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Temperature != 0.4 || body.Stream != streaming {
								t.Errorf("header switch affected parameter override or stream mode: %v", err)
							}
							if streaming {
								w.Header().Set("Content-Type", "text/event-stream")
								events := append(append([]string{}, upstream.prelude...), upstream.content)
								events = append(events, upstream.ending...)
								_, _ = io.WriteString(w, compatSSE(events...))
								return
							}
							w.Header().Set("Content-Type", "application/json")
							switch upstream.protocol {
							case model.ProtocolOpenAIChatCompletion:
								_, _ = io.WriteString(w, chatCompletionBody)
							case model.ProtocolOpenAIResponse:
								_, _ = io.WriteString(w, `{"id":"resp-fixture","object":"response","created_at":1,"status":"completed","model":"upstream-model","output":[{"id":"msg-fixture","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"pong","annotations":[]}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
							case model.ProtocolAnthropicMessage:
								_, _ = io.WriteString(w, `{"id":"msg-fixture","type":"message","role":"assistant","model":"upstream-model","content":[{"type":"text","text":"pong"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
							}
						})
						detail, err := op.ChannelDetailGet(channelIDOfGrant(t, group.Items[0].ChannelGrantID))
						if err != nil {
							t.Fatal(err)
						}
						detail.CustomHeaderModelOnly = modelOnly
						detail.CustomHeader = []model.CustomHeader{
							{HeaderKey: "User-Agent", HeaderValue: "fixture-discovery/1"},
							{HeaderKey: "X-Client-Type", HeaderValue: "configured"},
							{HeaderKey: "X-Discovery-Fixture", HeaderValue: "discovery-header"},
						}
						detail.ParamOverride = `{"temperature":0.4}`
						if _, err := op.ChannelUpdateWithOptions(&detail, model.ChannelSaveOptions{CustomHeaderModelOnlyPresent: true}, t.Context()); err != nil {
							t.Fatal(err)
						}
						request := compatRequest(t, group, client.format, streaming, nil)
						request.Header.Set("User-Agent", "fixture-client/2")
						request.Header.Set("X-Client-Type", "client")
						response := httptest.NewRecorder()
						serveWithTimeout(t, engine, response, request)
						if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "pong") || calls.Load() != 1 {
							t.Fatalf("relay failed: status=%d calls=%d body=%s", response.Code, calls.Load(), response.Body.String())
						}
					})
				}
			}
		}
	}
}
