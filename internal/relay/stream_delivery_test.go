package relay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/gin-contrib/sse"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer/openai"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

type deliveryProbe struct {
	streams.Stream[*httpclient.StreamEvent]
	acks, boundaries, closes, waits int
	ackCheck, waitCheck             func()
	sourceComplete                  bool
	format                          llm.APIFormat
	current                         *httpclient.StreamEvent
}

func (p *deliveryProbe) Next() bool {
	if !p.Stream.Next() {
		return false
	}
	p.current = p.Stream.Current()
	last, err := inspectStreamEvent(p.format, p.current)
	p.sourceComplete = last && err == nil
	return true
}

func (p *deliveryProbe) Current() *httpclient.StreamEvent { return p.current }

func (p *deliveryProbe) AcknowledgeDelivery() {
	p.acks++
	if p.ackCheck != nil {
		p.ackCheck()
	}
}

func (p *deliveryProbe) DeliveryAcknowledged() bool { return p.acks > 0 }

func (p *deliveryProbe) Deliver(ctx context.Context, write func() (bool, error)) error {
	p.boundaries++
	if err := ctx.Err(); err != nil {
		return err
	}
	terminal, err := write()
	if err == nil && terminal {
		p.AcknowledgeDelivery()
	}
	return err
}

func (p *deliveryProbe) Close() error {
	p.closes++
	return p.Stream.Close()
}

func (p *deliveryProbe) WaitForFinalization() {
	p.waits++
	if p.waitCheck != nil {
		p.waitCheck()
	}
}

func TestStreamDeliveryOptionalCapabilities(t *testing.T) {
	for _, caps := range []string{"none", "ack", "boundary-and-waiter", "all"} {
		t.Run(caps, func(t *testing.T) {
			probe := &deliveryProbe{
				Stream: streams.SliceStream([]*httpclient.StreamEvent{&llm.DoneStreamEvent}),
				format: llm.APIFormatOpenAIChatCompletion,
			}
			var source streams.Stream[*httpclient.StreamEvent]
			switch caps {
			case "none":
				source = &struct {
					streams.Stream[*httpclient.StreamEvent]
				}{probe}
			case "ack":
				source = &struct {
					streams.Stream[*httpclient.StreamEvent]
					streams.DeliveryAcknowledger
				}{probe, probe}
			case "boundary-and-waiter":
				source = &struct {
					streams.Stream[*httpclient.StreamEvent]
					streams.DeliveryBoundary
					streams.FinalizationWaiter
				}{probe, probe, probe}
			default:
				source = probe
			}
			middleware := &conversionMiddleware{format: llm.APIFormatOpenAIChatCompletion}
			raw, err := middleware.OnOutboundRawStream(t.Context(), source)
			if err != nil {
				t.Fatal(err)
			}
			unified, err := middleware.OnOutboundLlmStream(t.Context(), streams.Map(raw, func(*httpclient.StreamEvent) *llm.Response { return llm.DoneResponse }))
			if err != nil {
				t.Fatal(err)
			}
			client, err := middleware.OnInboundRawStream(t.Context(), streams.Map(unified, func(*llm.Response) *httpclient.StreamEvent { return &llm.DoneStreamEvent }))
			if err != nil {
				t.Fatal(err)
			}
			for _, layer := range []any{raw, unified, client} {
				_, ack := layer.(streams.DeliveryAcknowledger)
				_, boundary := layer.(streams.DeliveryBoundary)
				_, waiter := layer.(streams.FinalizationWaiter)
				if ack != (caps == "ack" || caps == "all") || boundary != (caps == "boundary-and-waiter" || caps == "all") || waiter != boundary {
					t.Fatalf("capabilities changed at %T: ack=%t boundary=%t waiter=%t", layer, ack, boundary, waiter)
				}
			}
			if !client.Next() || !middleware.terminal.complete {
				t.Fatal("capability wrapper bypassed source terminal validation")
			}
			if err := deliverStreamEvent(t.Context(), client, true, func() error { return nil }); err != nil {
				t.Fatal(err)
			}
			if caps != "none" && probe.acks != 1 {
				t.Fatalf("acknowledgment count=%d", probe.acks)
			}
			if err := client.Close(); err != nil {
				t.Fatal(err)
			}
			waitForStreamFinalization(client)
			if probe.closes != 1 {
				t.Fatalf("Close delegated %d times", probe.closes)
			}
		})
	}
}

type deliveryWriter struct {
	*httptest.ResponseRecorder
	mode            string
	cancel          context.CancelFunc
	terminalWritten bool
	terminalFlushed bool
}

func (w *deliveryWriter) Write(data []byte) (int, error) {
	terminal := strings.Contains(string(data), "data: [DONE]") || strings.Contains(string(data), `"type":"response.completed"`)
	if terminal && w.mode == "short-terminal" {
		return w.ResponseRecorder.Write(data[:len(data)-1])
	}
	if terminal && w.mode == "write-error" {
		return 0, errors.New("synthetic client write error")
	}
	n, err := w.ResponseRecorder.Write(data)
	w.terminalWritten = terminal && err == nil && n == len(data)
	if w.mode == "cancel-before-terminal" && strings.Contains(string(data), "pong") {
		w.cancel()
	}
	return n, err
}

func (w *deliveryWriter) Flush() {
	w.ResponseRecorder.Flush()
	if w.terminalWritten {
		w.terminalFlushed = true
		if w.mode == "cancel-after-terminal" {
			w.cancel()
		}
	}
}

type deliveryExecutor struct {
	*httpclient.HttpClient
	wrap func(streams.Stream[*httpclient.StreamEvent]) streams.Stream[*httpclient.StreamEvent]
}

func (e *deliveryExecutor) DoStream(ctx context.Context, request *httpclient.Request) (streams.Stream[*httpclient.StreamEvent], error) {
	stream, err := e.HttpClient.DoStream(ctx, request)
	if err != nil {
		return nil, err
	}
	return e.wrap(stream), nil
}

func TestStreamDeliveryPipelineCapabilities(t *testing.T) {
	// The library's HTTP metadata Map drops decoder capabilities before the
	// pipeline sees them. Attach source-owned capabilities at the executor
	// boundary, where AxonHub's orchestrator supplies them. Keep real HTTP,
	// both real transformers and all three Relay middleware hooks in this test.
	for _, mode := range []string{"success", "ack-only", "short-terminal", "write-error", "cancel-before-terminal", "cancel-after-terminal", "wait-for-accounting"} {
		t.Run(mode, func(t *testing.T) {
			calls := &upstreamCallLog{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.add(r.URL.Path)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, compatSSE(compatResponseCreated,
					`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"pong"}`, compatResponseCompleted))
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			writer := &deliveryWriter{ResponseRecorder: httptest.NewRecorder(), mode: mode, cancel: cancel}
			var probe *deliveryProbe
			executor := &deliveryExecutor{HttpClient: httpclient.NewHttpClientWithClient(server.Client())}
			executor.wrap = func(source streams.Stream[*httpclient.StreamEvent]) streams.Stream[*httpclient.StreamEvent] {
				probe = &deliveryProbe{Stream: source, format: llm.APIFormatOpenAIResponse}
				probe.ackCheck = func() {
					if !writer.terminalFlushed {
						t.Error("acknowledged before complete terminal write/flush")
					}
				}
				if mode == "ack-only" {
					return &struct {
						streams.Stream[*httpclient.StreamEvent]
						streams.DeliveryAcknowledger
					}{probe, probe}
				}
				return probe
			}
			outbound, err := responses.NewOutboundTransformer(server.URL, "synthetic-credential")
			if err != nil {
				t.Fatal(err)
			}
			middleware := &conversionMiddleware{format: llm.APIFormatOpenAIResponse}
			processor := pipeline.NewFactory(executor).Pipeline(openai.NewInboundTransformer(), outbound, pipeline.WithRetry(1, 0, 0), pipeline.WithMiddlewares(middleware))
			result, err := processor.Process(ctx, &httpclient.Request{
				Method: http.MethodPost, URL: server.URL + "/v1/chat/completions",
				Headers: http.Header{"Content-Type": {"application/json"}},
				Body:    []byte(`{"model":"upstream-model","stream":true,"messages":[{"role":"user","content":"ping"}]}`),
			})
			if err != nil {
				t.Fatal(err)
			}
			stream := result.EventStream
			var writeErr error
			for stream.Next() {
				event := stream.Current()
				last, err := inspectStreamEvent(llm.APIFormatOpenAIChatCompletion, event)
				if err != nil {
					t.Fatal(err)
				}
				if last && probe.acks != 0 {
					t.Fatal("source completion was mistaken for client delivery")
				}
				var encoded bytes.Buffer
				if err := sse.Encode(&encoded, sse.Event{Event: event.Type, Data: event.Data}); err != nil {
					t.Fatal(err)
				}
				writeErr = deliverStreamEvent(ctx, stream, last, func() error {
					n, err := writer.Write(encoded.Bytes())
					if err == nil && n != encoded.Len() {
						err = io.ErrShortWrite
					}
					if err == nil {
						writer.Flush()
					}
					return err
				})
				if writeErr != nil || last {
					break
				}
			}
			if mode == "short-terminal" && !errors.Is(writeErr, io.ErrShortWrite) {
				t.Fatalf("short write was lost: %v", writeErr)
			}
			if err := stream.Close(); err != nil {
				t.Fatal(err)
			}
			cancel()
			waitEntered, finalize := make(chan struct{}), make(chan struct{})
			probe.waitCheck = func() {
				if probe.closes != 1 || ctx.Err() == nil {
					t.Error("accounting wait happened before close/cancel")
				}
				if mode == "wait-for-accounting" {
					close(waitEntered)
					<-finalize
				}
			}
			waitDone := make(chan struct{})
			go func() {
				defer close(waitDone)
				waitForStreamFinalization(stream)
			}()
			if mode == "wait-for-accounting" {
				select {
				case <-waitEntered:
				case <-time.After(time.Second):
					t.Fatal("source waiter was lost")
				}
				select {
				case <-waitDone:
					t.Fatal("wait returned before accounting finished")
				default:
				}
				close(finalize)
			}
			select {
			case <-waitDone:
			case <-time.After(time.Second):
				t.Fatal("completed source finalization remained blocked")
			}
			wantAck := 1
			if mode == "short-terminal" || mode == "write-error" || mode == "cancel-before-terminal" {
				wantAck = 0
			}
			wantWaits := 1
			if mode == "ack-only" {
				wantWaits = 0
			} else if probe.boundaries == 0 {
				t.Fatal("source DeliveryBoundary was lost")
			}
			if probe.acks != wantAck || probe.closes != 1 || probe.waits != wantWaits || calls.count("/v1/responses") != 1 {
				t.Fatalf("capabilities/calls: ack=%d close=%d wait=%d calls=%v", probe.acks, probe.closes, probe.waits, calls.snapshot())
			}
		})
	}
}

func TestStreamDeliveryThroughRelay(t *testing.T) {
	for _, mode := range []string{"success", "short-terminal", "write-error", "cancel-before-terminal", "cancel-after-terminal", "source-error", "source-eof", "early-client-terminal", "early-client-terminal-source-eof", "missing-client-terminal"} {
		t.Run(mode, func(t *testing.T) {
			format, protocol := llm.APIFormatOpenAIChatCompletion, model.ProtocolOpenAIResponse
			body := compatSSE(compatResponseCreated, `{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"pong"}`, compatResponseCompleted)
			switch mode {
			case "source-error":
				body = compatSSE(compatResponseCreated, `{"type":"error","message":"synthetic provider failure"}`)
			case "source-eof":
				body = compatSSE(compatResponseCreated)
			case "missing-client-terminal":
				format, protocol = llm.APIFormatOpenAIResponse, model.ProtocolOpenAIChatCompletion
				body = chatCompletionStreamBody
			case "early-client-terminal", "early-client-terminal-source-eof":
				format, protocol = llm.APIFormatOpenAIResponse, model.ProtocolOpenAIChatCompletion
				body = strings.TrimSuffix(chatCompletionStreamBody, "data: [DONE]\n\n") + compatSSE(compatChatFinish,
					`{"id":"chatcmpl-test","object":"chat.completion.chunk","model":"upstream-model","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}`)
				if mode == "early-client-terminal" {
					body += compatSSE("[DONE]")
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			writer := &deliveryWriter{ResponseRecorder: httptest.NewRecorder(), mode: mode, cancel: cancel}
			engine, group := compatRelay(t, protocol, format, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, body)
			})
			request := compatRequest(t, group, format, true, nil).WithContext(ctx)
			done := make(chan struct{})
			go func() {
				defer close(done)
				engine.ServeHTTP(writer, request)
			}()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("Relay did not finish and release its stream")
			}
			state := compatState(t, group)
			wantStatus, wantFailures := StatusSuccess, 0
			switch mode {
			case "short-terminal", "write-error", "missing-client-terminal":
				wantStatus = StatusFailed
			case "cancel-before-terminal":
				wantStatus = StatusCanceled
			case "source-error", "source-eof":
				wantStatus, wantFailures = StatusFailed, 1
			case "early-client-terminal-source-eof":
				// Client delivery can be complete while the provider is invalid.
				wantStatus, wantFailures = StatusFailed, 1
			}
			if state.Status != wantStatus {
				t.Fatalf("delivery result: state=%+v body=%s", state, writer.Body)
			}
			if (wantStatus == StatusSuccess || mode == "early-client-terminal-source-eof") && !writer.terminalFlushed {
				t.Fatal("expected client terminal was not delivered")
			}
			if h := healthOf(t, group.ID).Members[group.ActiveItemID]; h.FailureStreak != wantFailures {
				t.Fatalf("client failure confused with provider failure: %+v", h)
			}
			channelID := channelIDOfGrant(t, group.Items[0].ChannelGrantID)
			channel, err := op.ChannelGet(channelID)
			if err != nil {
				t.Fatal(err)
			}
			wantSuccess := int64(0)
			if wantStatus == StatusSuccess {
				wantSuccess = 1
				if state.Usage.PromptTokens != 3 || state.Usage.CompletionTokens != 5 || channel.InputToken != 3 || channel.OutputToken != 5 {
					t.Fatalf("usage was lost or counted twice: state=%+v channel=%+v", state.Usage, channel.StatsMetrics)
				}
			}
			if channel.RequestSuccess != wantSuccess || channel.RequestFailed != 1-wantSuccess || ChannelConcurrencyStats(channelID).Active != 0 {
				t.Fatalf("request counted twice or leaked capacity: stats=%+v capacity=%+v", channel.StatsMetrics, ChannelConcurrencyStats(channelID))
			}
			grant, err := op.ChannelGrantGet(group.Items[0].ChannelGrantID)
			if err != nil {
				t.Fatal(err)
			}
			for _, metrics := range []model.StatsMetrics{grant.ChannelModel.StatsMetrics, grant.ChannelKey.StatsMetrics} {
				if metrics.RequestSuccess != channel.RequestSuccess || metrics.RequestFailed != channel.RequestFailed || metrics.InputToken != channel.InputToken || metrics.OutputToken != channel.OutputToken {
					t.Fatalf("channel/model/key accounting differs: channel=%+v child=%+v", channel.StatsMetrics, metrics)
				}
			}
		})
	}
}

func TestStreamDeliveryReleasesDedicatedProxy(t *testing.T) {
	for _, mode := range []string{"success", "short-terminal"} {
		t.Run(mode, func(t *testing.T) {
			var connections, closes atomic.Int32
			closed := make(chan struct{}, 2)
			proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, chatCompletionStreamBody)
			}))
			proxy.Config.ConnState = func(_ net.Conn, state http.ConnState) {
				if state == http.StateNew {
					connections.Add(1)
				}
				if state == http.StateClosed {
					closes.Add(1)
					closed <- struct{}{}
				}
			}
			proxy.Start()
			defer proxy.Close()
			group := protocolHealthGroup(t, proxy.URL, model.ProtocolOpenAIChatCompletion, testRelayConfig(1))
			channelID := channelIDOfGrant(t, group.Items[0].ChannelGrantID)
			detail, err := op.ChannelDetailGet(channelID)
			if err != nil {
				t.Fatal(err)
			}
			detail.Proxy, detail.ChannelProxy, detail.MaxConcurrency = true, proxy.URL, 1
			if _, err := op.ChannelUpdate(&detail, t.Context()); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			writer := &deliveryWriter{ResponseRecorder: httptest.NewRecorder(), mode: mode, cancel: cancel}
			newRelayEngine().ServeHTTP(writer, chatRequest(t, group.Name, true).WithContext(ctx))
			select {
			case <-closed:
			case <-ctx.Done():
				t.Fatal("dedicated proxy connection remained open after Relay returned")
			}
			if connections.Load() != 1 || closes.Load() != 1 || ChannelConcurrencyStats(channelID).Active != 0 {
				t.Fatalf("resource counts: opened=%d closed=%d capacity=%+v", connections.Load(), closes.Load(), ChannelConcurrencyStats(channelID))
			}
		})
	}
}
