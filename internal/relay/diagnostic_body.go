package relay

import (
	"sync"

	"github.com/bestruirui/octopus/internal/privacy"
)

// Each request retains at most one result per body. The redactor revision is
// part of the cache key, including after the request reaches a terminal state.
type diagnosticBodyCache struct {
	mu       sync.Mutex
	redactor *privacy.Redactor
	source   string
	result   string
	revision uint64
	valid    bool
}

func (cache *diagnosticBodyCache) read(redactor *privacy.Redactor, body string) string {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	revision := redactor.Revision()
	if cache.valid && cache.redactor == redactor && cache.source == body && cache.revision == revision {
		return cache.result
	}
	result := redactor.Body(body)
	// Body itself can discover credentials, and relay attempts may add more in
	// parallel. Such a result must not be tagged with the newer revision: the
	// next read needs another pass using all credentials known at that time.
	cache.valid = revision == redactor.Revision()
	cache.redactor, cache.source, cache.result, cache.revision = redactor, body, result, revision
	return result
}

func readDiagnosticBody(id uint64, response bool) string {
	mu.Lock()
	request := requests[id]
	if request == nil {
		mu.Unlock()
		return ""
	}
	body, slot := request.requestBody, &request.requestBodyCache
	if response {
		body, slot = request.responseBody, &request.responseBodyCache
	}
	if *slot == nil {
		*slot = &diagnosticBodyCache{}
	}
	cache, redactor := *slot, request.redactor
	mu.Unlock()
	// JSON parsing, matching and waiting for another reader of this body never
	// hold the relay-wide state mutex.
	return cache.read(redactor, body)
}
