package webhook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/textproto"
	"net/url"
	"regexp"
	"strings"
	"text/template"
	"time"
	"unicode/utf8"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/privacy"
)

const (
	DefaultTimeoutSeconds = 10
	MaxTimeoutSeconds     = 120
	redactedValue         = "***"
)

const DefaultBodyTemplate = `{
  "event": {{ .Event | json }},
  "timestamp": {{ .Timestamp | json }},
  "group_id": {{ .GroupID }},
  "group_name": {{ .GroupName | json }},
  "mode": {{ .Mode | json }},
  "current_channel": {{ .CurrentChannel | json }},
  "failure_streak": {{ .FailureStreak }},
  "failed_channels": {{ .FailedChannels }},
  "threshold": {{ .Threshold }},
  "retry_count": {{ .RetryCount }},
  "last_error": {{ .LastError | json }}
}`

type Event struct {
	Event          string          `json:"event"`
	Timestamp      string          `json:"timestamp"`
	GroupID        int             `json:"group_id,omitempty"`
	GroupName      string          `json:"group_name,omitempty"`
	Mode           model.GroupMode `json:"mode,omitempty"`
	CurrentChannel string          `json:"current_channel,omitempty"`
	FailureStreak  int             `json:"failure_streak,omitempty"`
	FailedChannels int             `json:"failed_channels,omitempty"`
	Threshold      int             `json:"threshold,omitempty"`
	RetryCount     int             `json:"retry_count,omitempty"`
	LastError      string          `json:"last_error,omitempty"`
}

type SendResult struct {
	Success    bool   `json:"success"`
	StatusCode int    `json:"status_code,omitempty"`
	Error      string `json:"error,omitempty"`
}

var headerNamePattern = regexp.MustCompile(`^[!#$%&'*+.^_\x60|~0-9A-Za-z-]+$`)

func NormalizeAndValidate(endpoint *model.WebhookEndpoint) error {
	endpoint.Name = strings.TrimSpace(endpoint.Name)
	if endpoint.Name == "" {
		return fmt.Errorf("name is required")
	}
	if utf8.RuneCountInString(endpoint.Name) > 120 {
		return fmt.Errorf("name must be at most 120 characters")
	}

	endpoint.URL = strings.TrimSpace(endpoint.URL)
	parsedURL, err := url.ParseRequestURI(endpoint.URL)
	if err != nil || parsedURL.Host == "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return fmt.Errorf("URL must be an absolute HTTP or HTTPS URL")
	}
	if strings.ContainsAny(endpoint.URL, "\r\n") {
		return fmt.Errorf("URL contains invalid characters")
	}
	if privacy.ContainsMask(endpoint.URL) {
		return fmt.Errorf("masked credential is stale; reload the endpoint or enter a new value")
	}

	endpoint.Method = strings.ToUpper(strings.TrimSpace(endpoint.Method))
	if endpoint.Method == "" {
		endpoint.Method = http.MethodPost
	}
	switch endpoint.Method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return fmt.Errorf("method must be GET, POST, PUT, PATCH, or DELETE")
	}

	endpoint.ContentType = strings.TrimSpace(endpoint.ContentType)
	if endpoint.ContentType == "" {
		endpoint.ContentType = "application/json"
	}
	if _, _, err := mime.ParseMediaType(endpoint.ContentType); err != nil {
		return fmt.Errorf("invalid content type: %w", err)
	}
	if endpoint.TimeoutSeconds == 0 {
		endpoint.TimeoutSeconds = DefaultTimeoutSeconds
	}
	if endpoint.TimeoutSeconds < 1 || endpoint.TimeoutSeconds > MaxTimeoutSeconds {
		return fmt.Errorf("timeout must be between 1 and %d seconds", MaxTimeoutSeconds)
	}
	if endpoint.Headers == nil {
		endpoint.Headers = map[string]string{}
	}
	canonicalHeaders := make(map[string]string, len(endpoint.Headers))
	for key, value := range endpoint.Headers {
		if IsSensitiveHeader(key) && privacy.ContainsMask(value) {
			return fmt.Errorf("masked credential is stale; reload the endpoint or enter a new value")
		}
		if !headerNamePattern.MatchString(key) {
			return fmt.Errorf("invalid header name %q", key)
		}
		canonical := textproto.CanonicalMIMEHeaderKey(key)
		if canonical == "Host" || canonical == "Content-Length" || canonical == "Transfer-Encoding" || canonical == "Connection" || canonical == "Content-Type" {
			return fmt.Errorf("header %q cannot be configured", key)
		}
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("header %q contains invalid characters", key)
		}
		if _, duplicate := canonicalHeaders[canonical]; duplicate {
			return fmt.Errorf("duplicate header name %q", key)
		}
		canonicalHeaders[canonical] = value
	}
	endpoint.Headers = canonicalHeaders
	if endpoint.BodyTemplate == "" {
		endpoint.BodyTemplate = DefaultBodyTemplate
	}
	body, err := Render(endpoint.BodyTemplate, Event{
		Event: "failure", Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
		GroupID: 1, GroupName: "model", Mode: model.GroupModeManual, CurrentChannel: "channel",
		FailureStreak: 10, Threshold: 10, RetryCount: 9, LastError: "upstream failure",
	})
	if err != nil {
		return err
	}
	return ValidateRenderedJSON(endpoint.ContentType, body)
}

func Render(bodyTemplate string, event Event) ([]byte, error) {
	event.LastError = privacy.New().Text(event.LastError)
	parsed, err := template.New("webhook").Option("missingkey=error").Funcs(template.FuncMap{
		"json": func(value any) (string, error) {
			encoded, err := json.Marshal(value)
			return string(encoded), err
		},
	}).Parse(bodyTemplate)
	if err != nil {
		return nil, fmt.Errorf("invalid body template: %w", err)
	}
	var body bytes.Buffer
	if err := parsed.Execute(&body, event); err != nil {
		return nil, fmt.Errorf("failed to render body template: %w", err)
	}
	return body.Bytes(), nil
}

func ValidateRenderedJSON(contentType string, body []byte) error {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return fmt.Errorf("invalid content type: %w", err)
	}
	if mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json") {
		return nil
	}
	if !json.Valid(body) {
		return fmt.Errorf("rendered body is not valid JSON")
	}
	return nil
}

func IsSensitiveHeader(name string) bool {
	return privacy.IsSensitiveName(name)
}

func MaskEndpoint(endpoint model.WebhookEndpoint) model.WebhookEndpoint {
	endpoint.URL = maskURL(endpoint.URL)
	endpoint.Headers = cloneHeaders(endpoint.Headers)
	for key, value := range endpoint.Headers {
		if IsSensitiveHeader(key) && value != "" {
			endpoint.Headers[key] = privacy.Mask(value)
		}
	}
	return endpoint
}

func PreserveMaskedSecrets(incoming *model.WebhookEndpoint, current model.WebhookEndpoint) {
	incoming.URL = preserveMaskedURL(incoming.URL, current.URL)
	for key, value := range incoming.Headers {
		if !IsSensitiveHeader(key) {
			continue
		}
		for oldKey, oldValue := range current.Headers {
			if strings.EqualFold(key, oldKey) && matchesMaskedValue(value, oldValue) {
				incoming.Headers[key] = oldValue
				break
			}
		}
	}
}

func maskURL(raw string) string {
	return privacy.New().URL(raw)
}

func matchesMaskedValue(value, current string) bool {
	return value == redactedValue || value == privacy.Mask(current)
}

func preserveMaskedURL(incoming, current string) string {
	if incoming == maskURL(current) {
		return current
	}
	newURL, newErr := url.Parse(incoming)
	oldURL, oldErr := url.Parse(current)
	if newErr != nil || oldErr != nil {
		return incoming
	}
	if newURL.User != nil && oldURL.User != nil {
		username := newURL.User.Username()
		if matchesMaskedValue(username, oldURL.User.Username()) {
			username = oldURL.User.Username()
		}
		password, hasPassword := newURL.User.Password()
		oldPassword, oldHasPassword := oldURL.User.Password()
		if matchesMaskedValue(password, oldPassword) || (!hasPassword && newURL.User.Username() == redactedValue) {
			password, hasPassword = oldPassword, oldHasPassword
		}
		if hasPassword {
			newURL.User = url.UserPassword(username, password)
		} else {
			newURL.User = url.User(username)
		}
	}
	newQuery := newURL.Query()
	oldQuery := oldURL.Query()
	for key, values := range newQuery {
		if !isSensitiveURLKey(key) {
			continue
		}
		if oldValues, exists := oldQuery[key]; exists {
			for i, value := range values {
				if i < len(oldValues) && matchesMaskedValue(value, oldValues[i]) {
					values[i] = oldValues[i]
				}
			}
		}
	}
	newURL.RawQuery = newQuery.Encode()
	return newURL.String()
}

func isSensitiveURLKey(key string) bool {
	return IsSensitiveHeader(key)
}

func cloneHeaders(headers map[string]string) map[string]string {
	clone := make(map[string]string, len(headers))
	for key, value := range headers {
		clone[key] = value
	}
	return clone
}
