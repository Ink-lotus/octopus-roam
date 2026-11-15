package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/looplj/axonhub/llm"
)

// A real downstream HTTP client detects framing errors that ResponseRecorder
// accepts. The upstream length is correct, but comments and field whitespace
// disappear when the gateway decodes and re-encodes the events.
func TestStreamingContentLengthUsesDownstreamFraming(t *testing.T) {
	for _, protocol := range startupProtocols() {
		t.Run(protocol.name, func(t *testing.T) {
			events := append([]string{}, protocol.prelude...)
			events = append(events, protocol.content)
			events = append(events, protocol.ending...)
			wire := ": synthetic framing padding " + strings.Repeat("x", 64) + "\n\n" + compatSSE(events...)
			var calls atomic.Int64
			engine, group := compatRelay(t, protocol.protocol, protocol.format, func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Content-Length", strconv.Itoa(len(wire)))
				w.Header().Set("X-Framing-Fixture", "preserved")
				_, _ = io.WriteString(w, wire)
			})
			relay := httptest.NewServer(engine)
			t.Cleanup(relay.Close)
			client := relay.Client()
			client.Timeout = 15 * time.Second
			request := compatRequest(t, group, protocol.format, true, nil)
			request.URL.Scheme = "http"
			request.URL.Host = relay.Listener.Addr().String()
			request.RequestURI = ""
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatalf("read downstream SSE: %v (declared=%d received=%d)", err, response.ContentLength, len(body))
			}
			if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
				t.Fatalf("unexpected response: status=%d headers=%v", response.StatusCode, response.Header)
			}
			if response.ContentLength != -1 || response.Header.Get("Content-Length") != "" {
				t.Fatalf("stale upstream length retained: %v", response.Header)
			}
			if response.Header.Get("X-Framing-Fixture") != "preserved" {
				t.Fatal("unrelated upstream header changed")
			}
			if len(body) >= len(wire) {
				t.Fatal("fixture did not exercise a shorter re-encoded body")
			}
			if !strings.Contains(string(body), protocol.content) || !strings.Contains(string(body), protocol.ending[len(protocol.ending)-1]) {
				t.Fatalf("missing content or terminal event: %s", body)
			}
			if calls.Load() != 1 {
				t.Fatalf("unexpected upstream replay: %d", calls.Load())
			}
		})
	}
}

func TestNonStreamingContentLengthPreservesExactBody(t *testing.T) {
	engine, group := compatRelay(t, model.ProtocolOpenAIChatCompletion, llm.APIFormatOpenAIChatCompletion, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(chatCompletionBody)))
		w.Header().Set("X-Framing-Fixture", "preserved")
		_, _ = io.WriteString(w, chatCompletionBody)
	})
	relay := httptest.NewServer(engine)
	t.Cleanup(relay.Close)
	client := relay.Client()
	client.Timeout = 15 * time.Second
	request := compatRequest(t, group, llm.APIFormatOpenAIChatCompletion, false, nil)
	request.URL.Scheme = "http"
	request.URL.Host = relay.Listener.Addr().String()
	request.RequestURI = ""
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || string(body) != chatCompletionBody || response.ContentLength != int64(len(body)) {
		t.Fatalf("ordinary response changed: status=%d declared=%d body=%s", response.StatusCode, response.ContentLength, body)
	}
	if response.Header.Get("X-Framing-Fixture") != "preserved" {
		t.Fatal("unrelated upstream header changed")
	}
}
