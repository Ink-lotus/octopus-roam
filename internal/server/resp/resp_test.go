package resp

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestErrorPreservesOriginalMessageAndEnvelope(t *testing.T) {
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest("POST", "/?key=fixture-uncollected-query", strings.NewReader("fixture-uncollected-body"))
	c.Request.Header.Set("Authorization", "Bearer fixture-uncollected-header")
	message := "failed password=fixture-error-value at https://fixture-user:fixture-password@example.test/?api_key=fixture-key"
	Error(c, 502, message)
	var body ResponseStruct
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != 502 || body.Code != 502 || body.Message != message || body.Data != nil || !c.IsAborted() {
		t.Fatalf("error response changed: %+v", body)
	}
	for _, text := range []string{"fixture-uncollected-query", "fixture-uncollected-header", "fixture-uncollected-body"} {
		if strings.Contains(response.Body.String(), text) {
			t.Fatal("error response collected additional request content")
		}
	}
}
