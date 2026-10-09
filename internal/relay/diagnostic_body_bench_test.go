package relay

import (
	"strings"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/privacy"
)

func BenchmarkDiagnosticBodyRead(b *testing.B) {
	for _, response := range []bool{false, true} {
		name := "request"
		read := RequestBody
		if response {
			name, read = "response", ResponseBody
		}
		b.Run(name, func(b *testing.B) {
			body := `{"messages":[` + strings.TrimSuffix(strings.Repeat(`{"content":"ordinary prompt fixture-benchmark-credential"},`, 100), ",") + `]}`
			r := privacy.New("fixture-benchmark-credential")
			safe := r.Body(body)
			id := idSeq.Add(1)
			mu.Lock()
			requests[id] = &RequestState{ID: id, redactor: r, requestBody: safe, responseBody: safe}
			mu.Unlock()
			b.Cleanup(func() { mu.Lock(); delete(requests, id); mu.Unlock() })
			b.SetBytes(int64(len(safe)))
			b.ReportAllocs()
			for b.Loop() {
				read(id)
			}
		})
	}
}
