package codexcatalog

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestVersionMappingHasNoRuntimeWhitelist(t *testing.T) {
	for _, version := range []string{"0.0.0", "0.169.0", "1.200.345", strings.Repeat("9", 28) + ".1.1"} {
		got, err := SourceURL(version)
		if err != nil || got != "https://raw.githubusercontent.com/openai/codex/rust-v"+version+"/codex-rs/models-manager/models.json" {
			t.Fatalf("version mapping: %q %q %v", version, got, err)
		}
	}
	for _, version := range []string{"", "0.169", "v0.169.0", "01.2.3", "1.02.3", "1.2.03", "-1.2.3",
		"1.2.3-alpha", "1.2.3+build", "1.2.3\n", " 1.2.3", "１.2.3", "1.2.3/../../main", strings.Repeat("9", 29) + ".1.1"} {
		if _, err := SourceURL(version); err == nil {
			t.Fatalf("invalid version accepted: %q", version)
		}
	}
}

func TestFilterRetainsOriginalModelAndUnknownBytes(t *testing.T) {
	const main = `{ "slug": "main", "auto_review_model_override":null, "instructions":"x\n\u003c", "context_window":9007199254740993, "future":{"tools":[{"x":true}]} }`
	const luna = `{"slug":"gpt-5.6-luna","supported_in_api":true, "auto_review_model_override":"official-other-reviewer","reasoning":[{"effort":"low"}]}`
	const absent = `{"slug":"absent-override","unknown":[null,1.2500,{"deep":"\u2028"}]}`
	prefix, suffix := " \n{ \"unknown\": {\"wide\":123456789012345678901234567890}, \"models\" : ", ", \"tail\": [true, false] }\n"
	raw := []byte(prefix + "[\n" + main + ",\n" + luna + ",\n" + absent + "\n]" + suffix)
	snapshot, err := parseSnapshot(raw, "0.169.0")
	if err != nil {
		t.Fatal(err)
	}
	all, count, err := snapshot.Filter([]string{"main", "gpt-5.6-luna", "absent-override"}, nil)
	if err != nil || count != 3 || !bytes.Equal(all, raw) {
		t.Fatal("unfiltered response changed original JSON")
	}
	all[0] = 'x'
	for _, test := range []struct {
		groups, allowed []string
		want            string
		count           int
	}{
		{[]string{"main", "gpt-5.6-luna", "absent-override"}, []string{"main", "gpt-5.6-luna"}, prefix + "[" + main + "," + luna + "]" + suffix, 2},
		{[]string{"main", "absent-override"}, nil, prefix + "[" + main + "," + absent + "]" + suffix, 2},
		{[]string{"MAIN", "main-prefix", "unknown"}, nil, prefix + "[]" + suffix, 0},
		{[]string{"main"}, []string{"gpt-5.6-luna"}, prefix + "[]" + suffix, 0},
		{nil, nil, prefix + "[]" + suffix, 0},
	} {
		body, count, err := snapshot.Filter(test.groups, test.allowed)
		if err != nil || count != test.count || string(body) != test.want {
			t.Fatalf("filter changed metadata: %s (%v)", body, err)
		}
		if !json.Valid(body) || bytes.Contains(body, []byte(`"origin"`)) {
			t.Fatal("invalid JSON or synthesized origin")
		}
	}
	if snapshot.SHA256() == "" || snapshot.ModelCount() != 3 {
		t.Fatal("missing snapshot identity")
	}
}

func TestAllEntriesAreValidatedBeforeFiltering(t *testing.T) {
	for _, body := range []string{
		`not-json`, `[]`, `null`, `{"models":null}`, `{"models":{}}`, `{}`,
		`{"models":[null]}`, `{"models":[[]]}`, `{"models":[1]}`, `{"models":[{}]}`,
		`{"models":[{"slug":""}]}`, `{"models":[{"slug":null}]}`, `{"models":[{"slug":3}]}`,
		`{"models":[{"slug":"x"},{"slug":"x"}]}`, `{"models":[],"models":[]}`,
		`{"models":[{"slug":"x","slug":"y"}]}`, "{\"models\":[{\"slug\":\"\xff\"}]}",
		`{"models":[]} {}`,
	} {
		if _, err := parseSnapshot([]byte(body), "0.169.0"); err == nil {
			t.Fatalf("invalid catalog accepted: %q", body)
		}
	}
	snapshot, err := parseSnapshot([]byte(`{"models":[]}`), "0.169.0")
	if err != nil {
		t.Fatal(err)
	}
	empty, count, err := snapshot.Filter([]string{"gpt-5.6-luna"}, nil)
	if err != nil || count != 0 || string(empty) != `{"models":[]}` {
		t.Fatal("empty catalog was changed or Luna synthesized")
	}
}

func TestDecodedAndFinalByteLimit(t *testing.T) {
	raw := []byte(`{"models":[{"slug":"main","large":"` + strings.Repeat("x", MaxBytes-40) + `"}]}`)
	raw = append(raw, bytes.Repeat([]byte{' '}, MaxBytes-len(raw))...)
	if len(raw) != MaxBytes {
		t.Fatal("fixture size")
	}
	snapshot, err := parseSnapshot(raw, "0.169.0")
	if err != nil {
		t.Fatal(err)
	}
	full, _, err := snapshot.Filter([]string{"main"}, nil)
	if err != nil || len(full) != MaxBytes || !bytes.Equal(full, raw) {
		t.Fatal("boundary-sized body changed")
	}
	if _, err := parseSnapshot(append(bytes.Clone(raw), ' '), "0.169.0"); err == nil {
		t.Fatal("oversized body accepted")
	}
}
