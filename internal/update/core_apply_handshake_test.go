package update

import (
	"encoding/json"
	"testing"
)

func TestReadyFileParsesWithAndWithoutBOM(t *testing.T) {
	cases := []struct {
		name  string
		data  []byte
		ready bool
		valid bool
	}{
		{name: "bom true", data: append([]byte{0xef, 0xbb, 0xbf}, []byte(`{"ready":true}`)...), ready: true, valid: true},
		{name: "utf8 true", data: []byte(`{"ready":true}`), ready: true, valid: true},
		{name: "bom false", data: append([]byte{0xef, 0xbb, 0xbf}, []byte(`{"ready":false}`)...), valid: true},
		{name: "truncated", data: []byte(`{"ready":tr`), valid: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var result struct {
				Ready bool `json:"ready"`
			}
			err := json.Unmarshal(trimJSONBOM(tc.data), &result)
			if (err == nil) != tc.valid || (err == nil && result.Ready != tc.ready) {
				t.Fatalf("data=%q ready=%v err=%v", tc.data, result.Ready, err)
			}
		})
	}
}
