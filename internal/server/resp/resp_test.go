package resp

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/privacy"
	"github.com/gin-gonic/gin"
)

func TestErrorRedactsCredentialEchoes(t *testing.T) {
	previous := privacy.DiagnosticsEnabled()
	t.Cleanup(func() { privacy.SetDiagnosticsEnabled(previous) })
	privacy.SetDiagnosticsEnabled(true)

	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest("GET", "/?key=fixture-query-secret", nil)
	c.Request.Header.Set("Authorization", "Bearer fixture-inbound-secret")
	c.Request.Header.Set("X-Secret-Id", "fixture-suffix-value")
	c.Request.Header.Set("X-Token-Value", "fixture-token-value")
	Error(c, 502, "failed fixture-query-secret fixture-inbound-secret fixture-suffix-value fixture-token-value https://user:fixture-password@example.test/v1?api_key_id=fixture-upstream-secret")
	for _, secret := range []string{"fixture-query-secret", "fixture-inbound-secret", "fixture-password", "fixture-upstream-secret", "fixture-suffix-value", "fixture-token-value"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("error leaked a fixture credential: %s", response.Body)
		}
	}
}

func TestErrorPreservesDiagnosticsWhenPrivacyDisabled(t *testing.T) {
	previous := privacy.DiagnosticsEnabled()
	t.Cleanup(func() { privacy.SetDiagnosticsEnabled(previous) })
	privacy.SetDiagnosticsEnabled(false)

	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest("GET", "/?key=fixture-query-secret", nil)
	c.Request.Header.Set("Authorization", "Bearer fixture-inbound-secret")
	message := "failed fixture-query-secret fixture-inbound-secret"
	Error(c, 502, message)
	var body ResponseStruct
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != 502 || body.Code != 502 || body.Message != message {
		t.Fatalf("disabled privacy changed the error response: %+v", body)
	}
}
