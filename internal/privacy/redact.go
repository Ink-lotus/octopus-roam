// Package privacy redacts diagnostic copies. It must not be used to rewrite
// credentials in configuration, backups, or requests sent to an upstream.
package privacy

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
)

// Diagnostics are process-local. A random HMAC key makes fingerprints stable
// across their outputs without publishing a dictionary-testable credential hash.
var fingerprintKey = []byte(rand.Text())

// Experimental diagnostic redaction is opt-in. Each new request snapshots the
// setting so changing it does not mix redacted and original data in one request.
var diagnosticsEnabled atomic.Bool

func SetDiagnosticsEnabled(enabled bool) { diagnosticsEnabled.Store(enabled) }

func DiagnosticsEnabled() bool { return diagnosticsEnabled.Load() }

func NewDiagnostic(values ...string) *Redactor {
	if !DiagnosticsEnabled() {
		return &Redactor{disabled: true}
	}
	return New(values...)
}

var (
	markerRE = regexp.MustCompile(`\[redacted:[0-9a-f]{24}\]`)
	urlRE    = regexp.MustCompile(`(?i)\b(?:https?|socks5h?)://[^\s"'<>]+`)
	authRE   = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+([a-z0-9._~+/=-]+)`)
	fieldRE  = regexp.MustCompile(`(?i)(["']?([a-z][a-z0-9_.-]*)["']?\s*[:=]\s*)("(?:\\.|[^"\\])*"|'[^']*'|[^\s,;&}\]\r\n]+)`)
)

// Mask is idempotent so a value crossing several diagnostic boundaries retains
// the same fingerprint. Empty values and the legacy editing sentinel stay empty/masked.
func Mask(value string) string {
	if value == "" || value == "***" || isMarker(value) {
		return value
	}
	hash := hmac.New(sha256.New, fingerprintKey)
	hash.Write([]byte(value))
	return "[redacted:" + hex.EncodeToString(hash.Sum(nil)[:12]) + "]"
}

func isMarker(value string) bool {
	return markerRE.FindString(value) == value && value != ""
}

// ContainsMask lets editing APIs reject stale diagnostic fingerprints instead
// of accidentally persisting them as real credentials after a restart.
func ContainsMask(value string) bool {
	decoded, err := url.QueryUnescape(value)
	if err == nil {
		value = decoded
	}
	return value == "***" || markerRE.MatchString(value)
}

// IsSensitiveName is conservative for headers, URL queries and unstructured
// errors. A credential word is never exempted by an id/name suffix.
func IsSensitiveName(name string) bool {
	return sensitiveNormalizedName(normalizeName(name))
}

func normalizeName(name string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, name)
}

func sensitiveNormalizedName(name string) bool {
	for _, part := range []string{"authorization", "apikey", "password", "passwd", "secret", "credential", "cookie", "signature", "privatekey", "subscriptionkey", "token", "accesskey"} {
		if strings.Contains(name, part) {
			return true
		}
	}
	return name == "key" || name == "auth" || name == "pwd"
}

// Only structured body fields have metadata exceptions. Header/query fields
// with these same names still use IsSensitiveName. Values are always checked
// against known credentials even when their field name is metadata.
func isSensitiveBodyName(name string) bool {
	name = normalizeName(name)
	switch name {
	case "apikeyid", "apikeyname", "credentialname",
		"maxtokens", "maxoutputtokens", "maxcompletiontokens",
		"inputtokens", "outputtokens", "prompttokens", "completiontokens", "totaltokens",
		"cachedtokens", "reasoningtokens", "audiotokens", "texttokens", "imagetokens",
		"cachecreationinputtokens", "cachereadinputtokens", "acceptedpredictiontokens", "rejectedpredictiontokens",
		"inputtokensdetails", "outputtokensdetails", "prompttokensdetails", "completiontokensdetails", "tokencount":
		return false
	default:
		return sensitiveNormalizedName(name)
	}
}

// Redactor remembers credentials observed during a request, even if a channel
// is later changed or deleted. Its synchronization also permits body reads
// while the relay moves to the next member.
type Redactor struct {
	disabled bool // Immutable after construction; New remains available for mandatory credential masking.
	mu       sync.RWMutex
	secrets  map[string]string
	revision uint64
	matcher  *secretMatcher // immutable snapshot, invalidated only by changed mappings
}

type secretMatcher struct {
	long  *strings.Replacer
	short []shortSecretMatcher
}

type shortSecretMatcher struct {
	value, masked string
	pattern       *regexp.Regexp
}

type contextKey struct{}

func WithRedactor(ctx context.Context, r *Redactor) context.Context {
	return context.WithValue(ctx, contextKey{}, r)
}

func FromContext(ctx context.Context) *Redactor {
	if r, ok := ctx.Value(contextKey{}).(*Redactor); ok && r != nil {
		return r
	}
	return NewDiagnostic()
}

// New always redacts, including credentials in configuration editing APIs.
func New(values ...string) *Redactor {
	r := &Redactor{secrets: make(map[string]string)}
	r.Add(values...)
	return r
}

func (r *Redactor) Add(values ...string) {
	if r == nil || r.disabled {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.secrets == nil {
		r.secrets = make(map[string]string)
	}
	changed := false
	set := func(value, masked string) {
		if r.secrets[value] != masked {
			r.secrets[value] = masked
			changed = true
		}
	}
	for _, value := range values {
		if value == "" || value == "***" || isMarker(value) {
			continue
		}
		masked := Mask(value)
		set(value, masked)
		// HTTP errors and JSON bodies may quote/escape the same known value.
		for _, encoded := range []string{url.QueryEscape(value), url.PathEscape(value)} {
			set(encoded, masked)
		}
		encoded, _ := json.Marshal(value)
		set(string(encoded[1:len(encoded)-1]), masked)
		if fields := strings.Fields(value); len(fields) == 2 && (strings.EqualFold(fields[0], "Bearer") || strings.EqualFold(fields[0], "Basic")) {
			set(fields[1], Mask(fields[1]))
		}
	}
	if changed {
		r.revision++
		r.matcher = nil
	}
}

// Revision lets diagnostic caches detect additional credentials. Repeated
// registration of unchanged mappings does not invalidate an existing cache.
func (r *Redactor) Revision() uint64 {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.revision
}

func (r *Redactor) AddHeaders(headers http.Header) {
	if r != nil && r.disabled {
		return
	}
	for name, values := range headers {
		if IsSensitiveName(name) {
			r.Add(values...)
			for _, value := range values {
				if strings.Contains(strings.ToLower(name), "cookie") {
					for _, pair := range strings.Split(value, ";") {
						if _, secret, ok := strings.Cut(pair, "="); ok {
							r.Add(strings.TrimSpace(secret))
						}
					}
				}
				parts := strings.Fields(value)
				if len(parts) == 2 && strings.EqualFold(parts[0], "Basic") {
					if decoded, err := base64.StdEncoding.DecodeString(parts[1]); err == nil {
						username, password, _ := strings.Cut(string(decoded), ":")
						r.Add(username, password)
					}
				}
			}
		}
	}
}

func (r *Redactor) AddURL(raw string) {
	if r != nil && r.disabled {
		return
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		r.Add(raw)
		return
	}
	if parsed.User != nil {
		password, _ := parsed.User.Password()
		r.Add(parsed.User.Username(), password, base64.StdEncoding.EncodeToString([]byte(parsed.User.Username()+":"+password)))
	}
	for key, values := range parsed.Query() {
		if IsSensitiveName(key) {
			r.Add(values...)
		}
	}
}

// URL retains the host/path and ordinary query parameters needed for diagnosis.
func (r *Redactor) URL(raw string) string {
	if r != nil && r.disabled {
		return raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return Mask(raw)
	}
	if parsed.User != nil {
		username := Mask(parsed.User.Username())
		if password, ok := parsed.User.Password(); ok {
			parsed.User = url.UserPassword(username, Mask(password))
		} else {
			parsed.User = url.User(username)
		}
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		parsed.RawQuery = "redacted=" + url.QueryEscape(Mask(parsed.RawQuery))
	} else {
		for key, values := range query {
			for i, value := range values {
				if IsSensitiveName(key) {
					query[key][i] = Mask(value)
				} else {
					query[key][i] = r.replaceKnown(value)
				}
			}
		}
		parsed.RawQuery = query.Encode()
	}
	parsed.Fragment = r.replaceKnown(parsed.Fragment)
	return parsed.String()
}

func (r *Redactor) Text(value string) string {
	if r != nil && r.disabled {
		return value
	}
	var output strings.Builder
	start := 0
	for _, match := range urlRE.FindAllStringIndex(value, -1) {
		output.WriteString(r.plainText(value[start:match[0]]))
		raw := value[match[0]:match[1]]
		trimmed := strings.TrimRight(raw, "),;]}")
		r.AddURL(trimmed)
		output.WriteString(r.URL(trimmed))
		output.WriteString(raw[len(trimmed):])
		start = match[1]
	}
	output.WriteString(r.plainText(value[start:]))
	return r.replaceKnown(output.String())
}

func (r *Redactor) plainText(value string) string {
	// Replace known compound header values before field matching can split them.
	value = r.replaceKnown(value)
	value = authRE.ReplaceAllStringFunc(value, func(raw string) string {
		parts := authRE.FindStringSubmatch(raw)
		r.Add(parts[2])
		return parts[1] + " " + Mask(parts[2])
	})
	value = fieldRE.ReplaceAllStringFunc(value, func(raw string) string {
		parts := fieldRE.FindStringSubmatch(raw)
		if !IsSensitiveName(parts[2]) {
			return raw
		}
		content := parts[3]
		if strings.Contains(strings.ToLower(parts[2]), "authorization") && (strings.EqualFold(content, "Bearer") || strings.EqualFold(content, "Basic")) {
			return raw
		}
		if strings.HasPrefix(content, "\"") {
			var decoded string
			if json.Unmarshal([]byte(content), &decoded) == nil {
				r.Add(decoded)
				encoded, _ := json.Marshal(Mask(decoded))
				return parts[1] + string(encoded)
			}
		}
		if strings.HasPrefix(content, "'") {
			r.Add(strings.Trim(content, "'"))
			return parts[1] + "'" + Mask(strings.Trim(content, "'")) + "'"
		}
		if strings.HasPrefix(content, "[redacted:") {
			return raw
		}
		r.Add(content)
		return parts[1] + Mask(content)
	})
	return r.replaceKnown(value)
}

// Body redacts structured credential fields recursively and known credentials
// in ordinary text. It preserves ordinary prompts/responses; it does not claim
// to identify arbitrary personal information inside free-form conversation.
func (r *Redactor) Body(body string) string {
	if r != nil && r.disabled {
		return body
	}
	// Keep encoding/json's nesting limit before the duplicate-aware token walk.
	if !json.Valid([]byte(body)) {
		return r.Text(body)
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	duplicates := false
	value, err := r.decodeBodyValue(decoder, &duplicates)
	if err != nil {
		return r.Text(body)
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return r.Text(body)
	}
	before, _ := json.Marshal(value)
	revision := r.Revision()
	value = r.scrub(value)
	if r.Revision() != revision {
		// A later free-text value may reveal a credential echoed by an earlier
		// element. Revisit the scrubbed tree with those newly learned values.
		value = r.scrub(value)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return r.Text(body)
	}
	if !duplicates && string(before) == string(encoded) {
		return body
	}
	return string(encoded)
}

// Decode every object entry before duplicate keys can overwrite it. Otherwise
// an earlier credential could survive in the original body or in a later echo.
func (r *Redactor) decodeBodyValue(decoder *json.Decoder, duplicates *bool) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		node := make(map[string]any)
		for decoder.More() {
			token, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := token.(string)
			if !ok {
				return nil, fmt.Errorf("invalid JSON object key")
			}
			child, err := r.decodeBodyValue(decoder, duplicates)
			if err != nil {
				return nil, err
			}
			if _, exists := node[key]; exists {
				*duplicates = true
			}
			if isSensitiveBodyName(key) {
				if secret, ok := child.(string); ok {
					r.Add(secret)
				}
			}
			node[key] = child
		}
		_, err := decoder.Token()
		return node, err
	case json.Delim('['):
		node := make([]any, 0)
		for decoder.More() {
			child, err := r.decodeBodyValue(decoder, duplicates)
			if err != nil {
				return nil, err
			}
			node = append(node, child)
		}
		_, err := decoder.Token()
		return node, err
	default:
		return token, nil
	}
}

func (r *Redactor) scrub(value any) any {
	switch node := value.(type) {
	case map[string]any:
		for key, child := range node {
			if isSensitiveBodyName(key) && child != nil {
				if secret, ok := child.(string); ok {
					node[key] = Mask(secret)
				} else {
					raw, _ := json.Marshal(child)
					node[key] = Mask(string(raw))
				}
			} else {
				node[key] = r.scrub(child)
			}
		}
	case []any:
		for i, child := range node {
			node[i] = r.scrub(child)
		}
	case string:
		return r.Text(node)
	}
	return value
}

func (r *Redactor) matcherSnapshot() *secretMatcher {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	matcher := r.matcher
	r.mu.RUnlock()
	if matcher != nil {
		return matcher
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.matcher != nil {
		return r.matcher
	}
	matcher = &secretMatcher{}
	keys := make([]string, 0, len(r.secrets))
	for key := range r.secrets {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) == len(keys[j]) {
			return keys[i] < keys[j]
		}
		return len(keys[i]) > len(keys[j])
	})
	pairs := make([]string, 0, len(keys)*2)
	for _, key := range keys {
		if len(key) < 4 {
			matcher.short = append(matcher.short, shortSecretMatcher{
				value: key, masked: r.secrets[key],
				pattern: regexp.MustCompile(`(^|[^a-zA-Z0-9_])` + regexp.QuoteMeta(key) + `($|[^a-zA-Z0-9_])`),
			})
		} else {
			pairs = append(pairs, key, r.secrets[key])
		}
	}
	if len(pairs) > 0 {
		matcher.long = strings.NewReplacer(pairs...)
	}
	r.matcher = matcher
	return matcher
}

func (r *Redactor) replaceKnown(text string) string {
	matcher := r.matcherSnapshot()
	if matcher == nil || (matcher.long == nil && len(matcher.short) == 0) {
		return text
	}
	// Already redacted values must not be damaged by short credentials or by
	// a later pass after a different channel was selected.
	var output strings.Builder
	start := 0
	redact := func(part string) string {
		if matcher.long != nil {
			part = matcher.long.Replace(part)
		}
		for _, short := range matcher.short {
			for {
				next := short.pattern.ReplaceAllStringFunc(part, func(match string) string { return strings.Replace(match, short.value, short.masked, 1) })
				if next == part {
					break
				}
				part = next
			}
		}
		return part
	}
	for _, match := range markerRE.FindAllStringIndex(text, -1) {
		output.WriteString(redact(text[start:match[0]]))
		output.WriteString(text[match[0]:match[1]])
		start = match[1]
	}
	output.WriteString(redact(text[start:]))
	return output.String()
}
