package relay

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/bestruirui/octopus/internal/privacy"
)

func registerDiagnosticRequest(t *testing.T, redactor *privacy.Redactor, body string) *RequestState {
	t.Helper()
	request := &RequestState{ID: idSeq.Add(1), Status: StatusRunning, redactor: redactor, requestBody: redactor.Body(body)}
	mu.Lock()
	requests[request.ID] = request
	mu.Unlock()
	t.Cleanup(func() { mu.Lock(); delete(requests, request.ID); mu.Unlock() })
	return request
}

func TestDiagnosticCachesReloadAfterLateCredentialsAndResponseChanges(t *testing.T) {
	const first, second = "fixture-first-upstream-key", "fixture-second-upstream-key"
	body := `{"content":"ordinary prompt ` + first + ` ` + second + `"}`
	r := privacy.New()
	request := registerDiagnosticRequest(t, r, body)
	// Warm a cache before any upstream has been selected, and before a response exists.
	if RequestBody(request.ID) != body || ResponseBody(request.ID) != "" {
		t.Fatal("initial diagnostic body is incorrect")
	}
	r.Add(first)
	got := RequestBody(request.ID)
	if strings.Contains(got, first) || !strings.Contains(got, second) || !strings.Contains(got, "ordinary prompt") {
		t.Fatal("request cache ignored a credential learned after creation")
	}
	mu.Lock()
	request.Status = StatusSuccess
	request.responseBody = r.Body(body)
	mu.Unlock()
	if got := ResponseBody(request.ID); got == "" || strings.Contains(got, first) {
		t.Fatal("empty response cache survived completion")
	}
	// A terminal status is not a redactor freeze: both caches must still invalidate.
	r.Add(second)
	for _, read := range []func(uint64) string{RequestBody, ResponseBody} {
		got := read(request.ID)
		assertDiagnosticPrivate(t, got, first, second)
		if read(request.ID) != got || !strings.Contains(got, "ordinary prompt") {
			t.Fatal("stable cached read changed the diagnostic")
		}
	}
	mu.Lock()
	request.responseBody = `{"content":"updated ordinary response"}`
	mu.Unlock()
	if got := ResponseBody(request.ID); !strings.Contains(got, "updated ordinary response") {
		t.Fatal("changed body reused a result from the same credential revision")
	}
	mu.Lock()
	delete(requests, request.ID)
	mu.Unlock()
	if RequestBody(request.ID) != "" || ResponseBody(request.ID) != "" {
		t.Fatal("deleted request remained accessible through the cache")
	}
}

func TestDiagnosticCacheTracksRedactorIdentityAndDiscoveredCredentials(t *testing.T) {
	cache := &diagnosticBodyCache{}
	input := "fixture-first-value fixture-second-value"
	first := privacy.New("fixture-first-value")
	second := privacy.New("fixture-second-value")
	cache.read(first, input)
	if got := cache.read(second, input); strings.Contains(got, "fixture-second-value") || !strings.Contains(got, "fixture-first-value") {
		t.Fatal("cache reused a different redactor's result")
	}
	// Text credentials can be discovered during Body itself. Both the first
	// result and the subsequent cached read must reflect the discovered value.
	r := privacy.New()
	body := `["fixture-discovered-value","api_key=fixture-discovered-value"]`
	if got := cache.read(r, body); strings.Contains(got, "fixture-discovered-value") {
		t.Fatal("first diagnostic read missed a credential found in the body")
	}
	if got := cache.read(r, body); strings.Contains(got, "fixture-discovered-value") {
		t.Fatal("body discovery poisoned the cache with an obsolete result")
	}
}

func TestConcurrentDiagnosticReadsUseCredentialsRegisteredBeforeReading(t *testing.T) {
	var secrets []string
	for i := range 16 {
		secrets = append(secrets, fmt.Sprintf("fixture-concurrent-upstream-%02d", i))
	}
	body := `{"content":"` + strings.Join(secrets, " ") + `"}`
	policy, err := privacy.Compile(privacy.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	r := privacy.NewWithPolicy(policy)
	request := registerDiagnosticRequest(t, r, body)
	mu.Lock()
	request.responseBody = body
	mu.Unlock()
	RequestBody(request.ID)
	ResponseBody(request.ID)
	var readers sync.WaitGroup
	for _, secret := range secrets {
		r.Add(secret)
		for _, read := range []func(uint64) string{RequestBody, ResponseBody} {
			readers.Go(func() {
				if strings.Contains(read(request.ID), secret) {
					t.Error("concurrent read reused a cache from before credential registration")
				}
			})
		}
	}
	readers.Wait()
}
