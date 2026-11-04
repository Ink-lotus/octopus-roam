package model

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestGroupCategoryNames(t *testing.T) {
	for _, tc := range []struct {
		name, input, display, key string
		valid                     bool
	}{
		{"trim", "  OpenAI\u3000", "OpenAI", "openai", true},
		{"one", "类", "类", "类", true},
		{"unicode limit", strings.Repeat("类", 64), strings.Repeat("类", 64), strings.Repeat("类", 64), true},
		{"internal spaces", "Foo  Bar", "Foo  Bar", "foo  bar", true},
		{"full width", "ＡＢＣ", "ＡＢＣ", "ａｂｃ", true},
		{"virtual label is ordinary name", "未分类", "未分类", "未分类", true},
		{"empty", "", "", "", false},
		{"whitespace", " \u3000\t", "", "", false},
		{"too long", strings.Repeat("类", 65), "", "", false},
		{"nul", "a\x00b", "", "", false},
		{"control", "a\nb", "", "", false},
		{"del", "a\x7fb", "", "", false},
		{"invalid utf8", string([]byte{0xff}), "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			display, key, err := NormalizeGroupCategoryName(tc.input)
			if !tc.valid {
				if !errors.Is(err, ErrInvalidGroupCategory) {
					t.Fatalf("invalid name accepted: %v", err)
				}
				return
			}
			if err != nil || display != tc.display || key != tc.key {
				t.Fatalf("normalization = %q / %q, err=%v", display, key, err)
			}
		})
	}
}

func TestGroupCategoryRequestThreeStates(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		present   bool
		id        int
	}{
		{"omitted", `{}`, false, 0},
		{"uncategorized", `{"category_id":null}`, true, 0},
		{"assigned", `{"category_id":71}`, true, 71},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var req GroupUpdateRequest
			if err := json.Unmarshal([]byte(tc.raw), &req); err != nil {
				t.Fatal(err)
			}
			if req.CategoryID.Present != tc.present || (req.CategoryID.ID == nil) != (tc.id == 0) || tc.id > 0 && *req.CategoryID.ID != tc.id {
				t.Fatalf("presence or category changed: %+v", req.CategoryID)
			}
			encoded, err := json.Marshal(req)
			if err != nil || string(encoded) != tc.raw {
				t.Fatalf("request round trip = %s, err=%v", encoded, err)
			}
		})
	}
	for _, raw := range []string{`0`, `-1`, `"71"`, `1.0`, `1e2`, `9223372036854775808`, `true`, `[]`, `{}`} {
		t.Run("reject "+raw, func(t *testing.T) {
			var req GroupUpdateRequest
			if err := json.Unmarshal([]byte(`{"category_id":`+raw+`}`), &req); !errors.Is(err, ErrInvalidGroupCategory) {
				t.Fatalf("invalid category_id accepted: %v", err)
			}
		})
	}
}

func TestGroupCategoryBackupWireExcludesRuntimeAndInternalKey(t *testing.T) {
	id := 71
	dump := DBDump{Product: "octopus-roam", Version: 1001,
		GroupCategories: []BackupGroupCategory{{ID: id, Name: "Fixture"}},
		Groups:          []Group{{ID: 501, Name: "fixture", CategoryID: &id, MetadataVersion: "1:99"}, {ID: 502, Name: "other"}},
	}
	if !dump.HasNonNullGroupCategoryReference() {
		t.Fatal("Go-built dump lost category references")
	}
	raw, err := json.Marshal(dump)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "metadata_version") || strings.Contains(string(raw), "name_key") || !strings.Contains(string(raw), `"category_id":null`) {
		t.Fatalf("incorrect backup wire: %s", raw)
	}
	var restored DBDump
	if err := json.Unmarshal(raw, &restored); err != nil || !restored.HasNonNullGroupCategoryReference() || *restored.Groups[0].CategoryID != id || restored.Groups[1].CategoryID != nil {
		t.Fatalf("category backup round trip failed: %v", err)
	}
	category, err := json.Marshal(GroupCategory{ID: id, Name: "Fixture", NameKey: "fixture"})
	if err != nil || string(category) != `{"id":71,"name":"Fixture"}` {
		t.Fatalf("category leaked internal fields: %s, %v", category, err)
	}
}
