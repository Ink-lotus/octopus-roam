package relay

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func registerDiagnosticRequest(t *testing.T, body string) *RequestState {
	t.Helper()
	request := &RequestState{ID: idSeq.Add(1), Status: StatusRunning, requestBody: body}
	mu.Lock()
	requests[request.ID] = request
	mu.Unlock()
	t.Cleanup(func() { mu.Lock(); delete(requests, request.ID); mu.Unlock() })
	return request
}

func TestDiagnosticBodiesRetainRawValuesAcrossUpdatesAndRemoval(t *testing.T) {
	body := `{"api_key":"fixture-body-key","content":"原始正文"}`
	request := registerDiagnosticRequest(t, body)
	if RequestBody(request.ID) != body || ResponseBody(request.ID) != "" {
		t.Fatal("initial diagnostic body is incorrect")
	}
	response := `{"content":"fixture-response-key","token":"fixture-token"}`
	mu.Lock()
	request.Status, request.responseBody = StatusSuccess, response
	mu.Unlock()
	if RequestBody(request.ID) != body || ResponseBody(request.ID) != response {
		t.Fatal("diagnostic values were changed")
	}
	snapshot, stream := OpenRequestStream()
	CloseRequestStream(stream)
	for _, item := range snapshot {
		if item.ID != request.ID {
			continue
		}
		raw, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{"requestBody", "responseBody", "fixture-body-key", "fixture-response-key"} {
			if strings.Contains(string(raw), text) {
				t.Fatal("state/SSE acquired on-demand body content")
			}
		}
	}
	mu.Lock()
	request.responseBody = "updated fixture-response-key"
	mu.Unlock()
	if ResponseBody(request.ID) != "updated fixture-response-key" {
		t.Fatal("on-demand detail returned an obsolete response")
	}
	mu.Lock()
	delete(requests, request.ID)
	mu.Unlock()
	if RequestBody(request.ID) != "" || ResponseBody(request.ID) != "" {
		t.Fatal("removed request remained accessible")
	}
}

func TestConcurrentDiagnosticReadsObserveCompleteBodies(t *testing.T) {
	body := "password=fixture-original-body"
	request := registerDiagnosticRequest(t, body)
	var workers sync.WaitGroup
	workers.Go(func() {
		for i := range 1000 {
			mu.Lock()
			if i%2 == 0 {
				request.responseBody = "response-a"
			} else {
				request.responseBody = "response-b"
			}
			mu.Unlock()
		}
	})
	for range 8 {
		workers.Go(func() {
			for range 1000 {
				if RequestBody(request.ID) != body {
					t.Error("request body changed")
				}
				got := ResponseBody(request.ID)
				if got != "" && got != "response-a" && got != "response-b" {
					t.Error("incomplete response observed")
				}
			}
		})
	}
	workers.Wait()
}
