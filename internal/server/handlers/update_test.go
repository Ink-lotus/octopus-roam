package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	serverauth "github.com/Ink-lotus/octopus-roam/internal/server/auth"
	"github.com/Ink-lotus/octopus-roam/internal/server/middleware"
	"github.com/Ink-lotus/octopus-roam/internal/update"
	"github.com/gin-gonic/gin"
)

func updateRequestForTest(t *testing.T, body string, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()
	engine := gin.New()
	engine.POST("/api/v1/update", middleware.Auth(), updateFunc)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/update", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if authenticated {
		token, _, err := serverauth.GenerateJWTToken(60)
		if err != nil {
			t.Fatal("could not generate synthetic authentication")
		}
		request.AddCookie(&http.Cookie{Name: "auth", Value: token})
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func TestUpdateHTTPRejectsMalformedTargetsBeforeUpdater(t *testing.T) {
	original := runCoreUpdate
	t.Cleanup(func() { runCoreUpdate = original })
	calls := 0
	runCoreUpdate = func(target string) (*update.UpdateReceipt, error) {
		calls++
		return &update.UpdateReceipt{Status: update.StatusUpdatePending, TargetVersion: target}, nil
	}
	for _, body := range []string{
		"", " ", "null", "[]", "{", `{}`, `{"target_version":`,
		`{"target_version":""}`, `{"target_version":null}`, `{"target_version":12}`,
		`{"target_version":true}`, `{"target_version":[]}`, `{"target_version":{}}`,
		`{"target_version":"v0.1.0-rc.2"} {}`, `{"target_version":"v0.1.0-rc.2"} trailing`,
	} {
		response := updateRequestForTest(t, body, true)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("malformed request returned %d", response.Code)
		}
		var envelope struct {
			Code    int             `json:"code"`
			Message string          `json:"message"`
			Data    json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || envelope.Code != 400 || envelope.Message != update.ErrInvalidUpdateTarget.Error() || len(envelope.Data) != 0 {
			t.Fatal("invalid input did not return a sanitized error envelope")
		}
	}
	if calls != 0 {
		t.Fatal("malformed target reached the updater")
	}
}

func TestUpdateHTTPRejectsInvalidVersionThroughRealUpdater(t *testing.T) {
	original := runCoreUpdate
	t.Cleanup(func() { runCoreUpdate = original })
	runCoreUpdate = update.UpdateCore
	for _, target := range []string{" ", " v0.1.0-rc.2", "v0.1.0-rc.2\n", "v0.1.0-local.1", "v0.1.0-beta.1", "v0.1.0-rc.0", "synthetic-private-target"} {
		body, err := json.Marshal(map[string]string{"target_version": target})
		if err != nil {
			t.Fatal(err)
		}
		response := updateRequestForTest(t, string(body), true)
		if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), "synthetic-private") {
			t.Fatalf("invalid version was accepted or exposed: status=%d", response.Code)
		}
	}
}

func TestUpdateHTTPPassesExactTargetAndReturnsReceipt(t *testing.T) {
	original := runCoreUpdate
	t.Cleanup(func() { runCoreUpdate = original })
	for _, target := range []string{"v0.1.0-rc.2", "v0.1.0"} {
		calls := 0
		runCoreUpdate = func(actual string) (*update.UpdateReceipt, error) {
			calls++
			if actual != target {
				t.Fatal("handler changed the submitted target")
			}
			return &update.UpdateReceipt{Status: update.StatusUpdatePending, TargetVersion: actual}, nil
		}
		response := updateRequestForTest(t, `{"target_version":"`+target+`"}`, true)
		var envelope struct {
			Code    int                  `json:"code"`
			Message string               `json:"message"`
			Data    update.UpdateReceipt `json:"data"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &envelope) != nil || calls != 1 || envelope.Code != 200 || envelope.Message != "success" || envelope.Data.Status != update.StatusUpdatePending || envelope.Data.TargetVersion != target {
			t.Fatal("accepted target receipt does not match the HTTP contract")
		}
	}
}

func TestUpdateHTTPPreservesErrorMapping(t *testing.T) {
	original := runCoreUpdate
	t.Cleanup(func() { runCoreUpdate = original })
	for _, tc := range []struct {
		err    error
		status int
	}{
		{update.ErrInvalidUpdateTarget, 400},
		{update.ErrUpdateTargetChanged, 409},
		{update.ErrAlreadyLatest, 409},
		{update.ErrDevelopmentBuild, 409},
		{update.ErrNoIndependentRelease, 409},
		{update.ErrUnsupportedPlatform, 409},
		{update.ErrContainerUpdate, 409},
		{update.ErrUpdateBusy, 429},
		{update.ErrReleaseCheck, 503},
		{update.ErrReleaseDownload, 502},
		{update.ErrReleaseVerification, 502},
		{fmt.Errorf("cannot prepare the update"), 502},
	} {
		runCoreUpdate = func(string) (*update.UpdateReceipt, error) {
			return nil, fmt.Errorf("update rejected: %w", tc.err)
		}
		response := updateRequestForTest(t, `{"target_version":"v0.1.0-rc.2"}`, true)
		var envelope struct {
			Code int             `json:"code"`
			Data json.RawMessage `json:"data"`
		}
		if response.Code != tc.status || json.Unmarshal(response.Body.Bytes(), &envelope) != nil || envelope.Code != tc.status || len(envelope.Data) != 0 {
			t.Fatalf("wrong error contract: status=%d want=%d err=%v", response.Code, tc.status, tc.err)
		}
	}
}

func TestUpdateHTTPStillRequiresAuthentication(t *testing.T) {
	original := runCoreUpdate
	t.Cleanup(func() { runCoreUpdate = original })
	calls := 0
	runCoreUpdate = func(string) (*update.UpdateReceipt, error) { calls++; return nil, update.ErrUpdateTargetChanged }
	response := updateRequestForTest(t, `{"target_version":"v0.1.0-rc.2"}`, false)
	if response.Code != http.StatusUnauthorized || calls != 0 {
		t.Fatal("unauthenticated update reached the updater")
	}
}
