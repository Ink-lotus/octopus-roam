package middleware

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequestLogPreservesOriginalFieldsWithoutCollectingHeadersOrBody(t *testing.T) {
	request := httptest.NewRequest("POST", "/v1/models?api_key=fixture-query-value&room=ops", strings.NewReader("fixture-uncollected-body"))
	request.Header.Set("Authorization", "Bearer fixture-uncollected-header")
	message := "failed password=fixture-error-value"
	got := formatRequestLog(gin.LogFormatterParams{Request: request, Path: request.URL.RequestURI(), ErrorMessage: message, StatusCode: 502})
	for _, text := range []string{request.URL.RequestURI(), message, "502"} {
		if !strings.Contains(got, text) {
			t.Fatalf("access log lost a supplied field: %s", got)
		}
	}
	for _, text := range []string{"fixture-uncollected-header", "fixture-uncollected-body", "[redacted:"} {
		if strings.Contains(got, text) {
			t.Fatal("access log expanded collection or replaced content")
		}
	}
}
