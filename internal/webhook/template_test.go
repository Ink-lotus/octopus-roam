package webhook

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/model"
)

func TestNormalizeAndValidateAppliesDefaultsAndRendersJSON(t *testing.T) {
	endpoint := model.WebhookEndpoint{
		Name: " alert ", URL: "https://example.com/hook?token=secret",
	}
	if err := NormalizeAndValidate(&endpoint); err != nil {
		t.Fatalf("NormalizeAndValidate() error = %v", err)
	}
	if endpoint.Name != "alert" || endpoint.Method != "POST" || endpoint.ContentType != "application/json" || endpoint.TimeoutSeconds != 10 {
		t.Fatalf("endpoint defaults not applied: %+v", endpoint)
	}
	if len(endpoint.Headers) != 0 {
		t.Fatalf("empty headers = %#v, want an empty map", endpoint.Headers)
	}
	for _, test := range []struct {
		name    string
		event   Event
		title   string
		content []string
	}{
		{
			name: "manual failure", title: "🚨 Octopus Roam 故障告警",
			event:   Event{Event: "failure", Mode: model.GroupModeManual, FailureStreak: 12, Threshold: 10, RetryCount: 11},
			content: []string{"事件：故障告警", "模式：手动模式", "连续失败：12 次", "失效成员：不适用", "通知阈值：10 次", "重试次数：11 次"},
		},
		{
			name: "manual recovery", title: "✅ Octopus Roam 恢复通知",
			event:   Event{Event: "recovery", Mode: model.GroupModeManual, Threshold: 10, RetryCount: 2},
			content: []string{"事件：恢复通知", "模式：手动模式", "连续失败：0 次", "失效成员：不适用", "通知阈值：10 次", "重试次数：2 次", "最近错误"},
		},
		{
			name: "failover failure", title: "🚨 Octopus Roam 故障告警",
			event:   Event{Event: "failure", Mode: model.GroupModeFailover, FailedChannels: 4, Threshold: 3, RetryCount: 7},
			content: []string{"模式：故障转移", "连续失败：不适用", "失效成员：4 个", "通知阈值：3 个", "重试次数：7 次"},
		},
		{
			name: "failover recovery", title: "✅ Octopus Roam 恢复通知",
			event:   Event{Event: "recovery", Mode: model.GroupModeFailover, FailedChannels: 2, Threshold: 3, RetryCount: 1},
			content: []string{"模式：故障转移", "连续失败：不适用", "失效成员：2 个", "通知阈值：3 个", "重试次数：1 次"},
		},
		{
			name: "test preview", title: "🧪 Octopus Roam 测试通知", event: Event{Event: "test"},
			content: []string{"测试预览", "示例数据", "事件：测试通知", "示例分组", "示例渠道", "示例错误"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			event := test.event
			event.Timestamp = "2026-01-01T00:00:00Z"
			if event.Event != "test" {
				event.GroupID, event.GroupName = 42, `model "one"`
				event.CurrentChannel, event.LastError = "channel one", "upstream \"unavailable\"\nretry later"
				test.content = append(test.content, event.GroupName, "ID：42", event.CurrentChannel, event.LastError)
			}
			body, err := Render(endpoint.BodyTemplate, event)
			if err != nil {
				t.Fatalf("render default template: %v", err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(body, &decoded); err != nil {
				t.Fatalf("default template produced invalid JSON: %s (%v)", body, err)
			}
			if decoded["title"] != test.title || decoded["from"] != "octopus-roam" {
				t.Fatalf("rendered notification metadata = %#v", decoded)
			}
			content, ok := decoded["content"].(string)
			if !ok {
				t.Fatalf("rendered content = %#v, want a string", decoded["content"])
			}
			for _, value := range append(test.content, event.Timestamp) {
				if !strings.Contains(content, value) {
					t.Fatalf("rendered content is missing %q: %q", value, content)
				}
			}
			if _, exists := decoded["channel"]; exists {
				t.Fatal("default template must not select a provider channel")
			}
		})
	}
}

func TestNormalizeAndValidateRejectsInvalidTemplatesAndHeaders(t *testing.T) {
	base := func(body, contentType string) model.WebhookEndpoint {
		return model.WebhookEndpoint{
			Name: "test", URL: "https://example.com/hook", Method: "POST",
			ContentType: contentType, TimeoutSeconds: 10, BodyTemplate: body,
		}
	}
	for _, test := range []struct {
		name     string
		endpoint model.WebhookEndpoint
	}{
		{name: "template syntax", endpoint: base(`{{ if }}`, "text/plain")},
		{name: "invalid JSON", endpoint: base(`{"event":}`, "application/json")},
		{name: "header injection", endpoint: func() model.WebhookEndpoint {
			endpoint := base("ok", "text/plain")
			endpoint.Headers = map[string]string{"X-Notify": "safe\r\nInjected: yes"}
			return endpoint
		}()},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := NormalizeAndValidate(&test.endpoint); err == nil {
				t.Fatal("NormalizeAndValidate() succeeded, want validation error")
			}
		})
	}
}

func TestEndpointAndTemplateUseLiteralConfigurationValues(t *testing.T) {
	for _, value := range []string{"fixture-current-key", "***", "[redacted:fixture]", ""} {
		endpoint := model.WebhookEndpoint{
			Name: "fixture", URL: "https://example.test/hook?token=fixture-query",
			Headers: map[string]string{"X-API-Key": value},
		}
		if err := NormalizeAndValidate(&endpoint); err != nil {
			t.Fatal(err)
		}
		if endpoint.Headers["X-Api-Key"] != value || endpoint.URL != "https://example.test/hook?token=fixture-query" {
			t.Fatal("configuration value was interpreted or replaced")
		}
		body, err := Render("{\"error\":{{json .LastError}}}", Event{LastError: "api_key=" + value})
		var decoded map[string]string
		if err != nil || json.Unmarshal(body, &decoded) != nil || decoded["error"] != "api_key="+value {
			t.Fatal("template changed the event value", err)
		}
	}
}
