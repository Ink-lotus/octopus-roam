// Package codexcatalog reads public, versioned Codex catalogs without credentials.
// A Snapshot owns the original JSON bytes; filtering only replaces models.
package codexcatalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"unicode/utf8"
)

const (
	MaxBytes     = 1 << 20
	sourcePrefix = "https://raw.githubusercontent.com/openai/codex/rust-v"
	sourceSuffix = "/codex-rs/models-manager/models.json"
)

var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// ValidVersion accepts canonical release versions, independently of installed CLI versions.
func ValidVersion(version string) bool {
	return len(version) <= 32 && versionPattern.MatchString(version)
}

func SourceURL(version string) (string, error) {
	if !ValidVersion(version) {
		return "", failure(http.StatusBadRequest, "invalid_client_version")
	}
	return sourcePrefix + version + sourceSuffix, nil
}

// Error contains only a stable public classification, never transport diagnostics.
type Error struct {
	Status int
	Code   string
}

func (e *Error) Error() string { return e.Code }

func failure(status int, code string) *Error { return &Error{Status: status, Code: code} }

type entry struct {
	slug       string
	start, end int
}

// Snapshot is immutable and retains unknown fields, whitespace and JSON number spellings.
type Snapshot struct {
	raw                  []byte
	entries              []entry
	arrayStart, arrayEnd int
	version, url, digest string
}

func (s *Snapshot) Version() string { return s.version }
func (s *Snapshot) URL() string     { return s.url }
func (s *Snapshot) SHA256() string  { return s.digest }
func (s *Snapshot) ModelCount() int { return len(s.entries) }

// Filter intersects exact group names and the current key's permission scope.
// An empty key scope means unrestricted, matching the gateway's existing contract.
func (s *Snapshot) Filter(groups, allowed []string) ([]byte, int, error) {
	groupSet := make(map[string]bool, len(groups))
	for _, name := range groups {
		groupSet[name] = true
	}
	allowSet := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		allowSet[name] = true
	}
	kept := make([]entry, 0, len(s.entries))
	for _, model := range s.entries {
		if groupSet[model.slug] && (len(allowed) == 0 || allowSet[model.slug]) {
			kept = append(kept, model)
		}
	}
	if len(kept) == len(s.entries) {
		return bytes.Clone(s.raw), len(kept), nil
	}
	output := make([]byte, 0, len(s.raw))
	output = append(output, s.raw[:s.arrayStart]...)
	output = append(output, '[')
	for index, model := range kept {
		if index > 0 {
			output = append(output, ',')
		}
		output = append(output, s.raw[model.start:model.end]...)
	}
	output = append(output, ']')
	output = append(output, s.raw[s.arrayEnd:]...)
	if len(output) > MaxBytes {
		return nil, 0, failure(http.StatusBadGateway, "catalog_too_large")
	}
	return output, len(kept), nil
}

func parseSnapshot(raw []byte, version string) (*Snapshot, error) {
	invalid := failure(http.StatusBadGateway, "invalid_catalog")
	if len(raw) > MaxBytes {
		return nil, failure(http.StatusBadGateway, "catalog_too_large")
	}
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return nil, invalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, invalid
	}
	snapshot := &Snapshot{raw: raw, version: version}
	snapshot.url, err = SourceURL(version)
	if err != nil {
		return nil, err
	}
	seenModels := false
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, invalid
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, invalid
		}
		if key != "models" {
			continue
		}
		if seenModels || len(value) == 0 || value[0] != '[' {
			return nil, invalid
		}
		seenModels = true
		snapshot.arrayEnd = int(decoder.InputOffset())
		snapshot.arrayStart = snapshot.arrayEnd - len(value)
		models := json.NewDecoder(bytes.NewReader(value))
		if _, err := models.Token(); err != nil {
			return nil, invalid
		}
		seenSlugs := make(map[string]bool)
		for models.More() {
			var model json.RawMessage
			if err := models.Decode(&model); err != nil {
				return nil, invalid
			}
			slug, ok := modelSlug(model)
			if !ok || seenSlugs[slug] {
				return nil, invalid
			}
			seenSlugs[slug] = true
			end := snapshot.arrayStart + int(models.InputOffset())
			snapshot.entries = append(snapshot.entries, entry{slug: slug, start: end - len(model), end: end})
		}
	}
	if !seenModels {
		return nil, invalid
	}
	digest := sha256.Sum256(raw)
	snapshot.digest = hex.EncodeToString(digest[:])
	return snapshot, nil
}

func modelSlug(raw []byte) (string, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return "", false
	}
	slug, seen := "", false
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return "", false
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return "", false
		}
		if key == "slug" {
			if seen || json.Unmarshal(value, &slug) != nil || slug == "" {
				return "", false
			}
			seen = true
		}
	}
	return slug, seen
}
