package middleware

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequestLogRedactsQueryAndHeaderCredentials(t *testing.T) {
	request := httptest.NewRequest("GET", "/v1/models?api_key=fixture-query-secret&room=ops", nil)
	request.Header.Set("Authorization", "Bearer fixture-inbound-secret")
	request.Header.Set("X-Secret-Id", "fixture-header-suffix-value")
	request.URL.RawQuery += "&api_key_id=fixture-query-suffix-value"
	got := formatRequestLog(gin.LogFormatterParams{Request: request, Path: request.URL.RequestURI(), ErrorMessage: "failed fixture-inbound-secret fixture-header-suffix-value", StatusCode: 502})
	for _, secret := range []string{"fixture-query-secret", "fixture-inbound-secret", "fixture-header-suffix-value", "fixture-query-suffix-value"} {
		if strings.Contains(got, secret) {
			t.Fatalf("access log leaked credential: %s", got)
		}
	}
	if !strings.Contains(got, "/v1/models") || !strings.Contains(got, "room=ops") || !strings.Contains(got, "502") {
		t.Fatalf("access log lost diagnostics: %s", got)
	}
}
