package privacy

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func BenchmarkReplaceKnown(b *testing.B) {
	for _, short := range []bool{false, true} {
		b.Run(fmt.Sprintf("short=%t", short), func(b *testing.B) {
			r := New()
			for i := range 12 {
				r.Add(fmt.Sprintf("fixture-credential-%02d-value", i))
			}
			if short {
				r.Add("xy")
			}
			input := strings.Repeat("ordinary diagnostic fixture-credential-03-value xy ", 8)
			b.SetBytes(int64(len(input)))
			b.ReportAllocs()
			for b.Loop() {
				r.replaceKnown(input)
			}
		})
	}
}

func BenchmarkRedactorBody(b *testing.B) {
	r := New()
	for i := range 12 {
		r.Add(fmt.Sprintf("fixture-credential-%02d-value", i))
	}
	messages := make([]map[string]string, 100)
	for i := range messages {
		messages[i] = map[string]string{"role": "user", "content": "ordinary prompt with fixture-credential-03-value and diagnostic context"}
	}
	raw, _ := json.Marshal(map[string]any{"messages": messages, "max_tokens": 4096, "metadata": map[string]string{"api_key": "fixture-credential-01-value"}})
	body := string(raw)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	for b.Loop() {
		r.Body(body)
	}
}
