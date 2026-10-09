package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/privacy"
	"github.com/gin-gonic/gin"
)

func TestRecoveryRedactsPanicAndDebugRequestDump(t *testing.T) {
	previousPrivacy := privacy.DiagnosticsEnabled()
	privacy.SetDiagnosticsEnabled(true)
	t.Cleanup(func() { privacy.SetDiagnosticsEnabled(previousPrivacy) })
	previousMode, previousWriter := gin.Mode(), gin.DefaultErrorWriter
	var output bytes.Buffer
	gin.SetMode(gin.DebugMode)
	gin.DefaultErrorWriter = &output
	defer func() { gin.SetMode(previousMode); gin.DefaultErrorWriter = previousWriter }()
	engine := gin.New()
	engine.Use(Recovery())
	engine.GET("/panic", func(c *gin.Context) {
		secret := c.GetHeader("X-Private-Custom")
		privacy.FromContext(c.Request.Context()).Add(secret)
		panic("fixture failure " + secret)
	})
	request := httptest.NewRequest(http.MethodGet, "/panic?api_key=fixture-query-value", nil)
	request.Header.Set("X-API-Key", "fixture-header-value")
	request.Header.Set("X-Private-Custom", "fixture-custom-value")
	request.Header.Set("X-Secret-Id", "fixture-secret-id-value")
	request.Header.Set("X-Token-Value", "fixture-token-value")
	request.URL.RawQuery += "&credential_name=fixture-query-name-value"
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("recovery status: %d", response.Code)
	}
	for _, secret := range []string{"fixture-query-value", "fixture-header-value", "fixture-custom-value", "fixture-secret-id-value", "fixture-token-value", "fixture-query-name-value"} {
		if strings.Contains(output.String(), secret) || strings.Contains(response.Body.String(), secret) {
			t.Fatal("panic diagnostics leaked a fixture credential")
		}
	}
	if !strings.Contains(output.String(), "fixture failure") || !strings.Contains(output.String(), "panic recovered") {
		t.Fatal("panic diagnostics missing")
	}
}
