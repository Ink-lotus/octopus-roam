package middleware

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/server/resp"
	"github.com/gin-gonic/gin"
)

func TestRecoveryPreservesDiagnosticsAndOriginalResponseScope(t *testing.T) {
	previousMode, previousWriter := gin.Mode(), gin.DefaultErrorWriter
	var output bytes.Buffer
	gin.SetMode(gin.DebugMode)
	gin.DefaultErrorWriter = &output
	defer func() { gin.SetMode(previousMode); gin.DefaultErrorWriter = previousWriter }()
	engine := gin.New()
	engine.Use(Recovery())
	engine.POST("/panic", func(c *gin.Context) {
		panic("fixture failure " + c.GetHeader("X-Private-Custom"))
	})
	request := httptest.NewRequest(http.MethodPost, "/panic?api_key=fixture-query-value", strings.NewReader("fixture-uncollected-body"))
	request.Header.Set("X-API-Key", "fixture-header-value")
	request.Header.Set("X-Private-Custom", "fixture-custom-value")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	var envelope resp.ResponseStruct
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusInternalServerError || envelope.Message != resp.ErrInternalServer {
		t.Fatal("panic recovery response changed")
	}
	for _, text := range []string{"fixture-query-value", "fixture-header-value", "fixture-custom-value", "panic recovered"} {
		if !strings.Contains(output.String(), text) {
			t.Fatal("panic diagnostic lost original content")
		}
		if strings.Contains(response.Body.String(), text) {
			t.Fatal("panic response acquired internal diagnostic content")
		}
	}
	if strings.Contains(output.String(), "fixture-uncollected-body") || strings.Contains(output.String(), "[redacted:") {
		t.Fatal("panic diagnostics expanded collection or replaced content")
	}
}
