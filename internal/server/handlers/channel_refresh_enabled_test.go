package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/gin-gonic/gin"
)

func enabledOnlyHandlerRequest(engine *gin.Engine, id, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/refresh-model/"+id, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}

func assertEnabledOnlyHandlerSkip(t *testing.T, response *httptest.ResponseRecorder, reason model.ChannelModelRefreshSkipReason) {
	t.Helper()
	var envelope struct {
		Code int                             `json:"code"`
		Data model.ChannelModelRefreshResult `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	result := envelope.Data
	if response.Code != http.StatusOK || envelope.Code != http.StatusOK || !result.Skipped || result.Success || result.SkipReason != reason || result.Error != "" {
		t.Fatalf("unexpected skip response: HTTP %d %s", response.Code, response.Body.String())
	}
	if result.AddedModels == nil || result.RemovedModels == nil || result.KeyResults == nil || len(result.KeyResults) != 0 {
		t.Fatal("skip arrays are missing or contain partial key results")
	}
}

func newEnabledOnlyHandlerChannel(t *testing.T, url string, enabled bool) int {
	t.Helper()
	detail, err := op.ChannelCreate(&model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{
			Name:    fmt.Sprintf("enabled-only-handler-%d", channelHandlerFixtureID.Add(1)),
			Enabled: enabled, BaseURL: url, AllowedProtocols: model.ProtocolOpenAIResponse,
		},
		Keys:   []model.ChannelKeyConfig{{Name: "primary", Key: "fixture-key", Enabled: true}},
		Models: []string{}, Grants: []model.ChannelGrantConfig{},
	}, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !enabled {
		if err := op.ChannelEnabled(detail.ID, false, t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	return detail.ID
}

func TestRefreshChannelModelsEnabledOnlyHandlerValidatesRequest(t *testing.T) {
	engine := gin.New()
	engine.POST("/refresh-model/:id", refreshChannelModels)
	for _, value := range []string{"null", `"true"`, "0", "1", "[]", "{}"} {
		response := enabledOnlyHandlerRequest(engine, "999999999", `{"enabled_only":`+value+`}`)
		if response.Code != http.StatusBadRequest {
			t.Errorf("enabled_only=%s returned %d", value, response.Code)
		}
	}
	for _, id := range []string{"bad", "0", "-1", "999999999999999999999999"} {
		response := enabledOnlyHandlerRequest(engine, id, `{"enabled_only":true}`)
		if response.Code != http.StatusBadRequest {
			t.Errorf("id=%s returned %d", id, response.Code)
		}
	}
	for _, body := range []string{`{}`, `{"enabled_only":false}`} {
		if response := enabledOnlyHandlerRequest(engine, "999999999", body); response.Code != http.StatusNotFound {
			t.Errorf("explicit deleted channel returned %d", response.Code)
		}
	}
	assertEnabledOnlyHandlerSkip(t, enabledOnlyHandlerRequest(engine, "999999999", `{"enabled_only":true}`), model.ChannelRefreshSkipDeleted)
}

func TestRefreshChannelModelsEnabledOnlyHandlerKeepsExplicitDisabledRefresh(t *testing.T) {
	var sends atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sends.Add(1)
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer upstream.Close()
	id := newEnabledOnlyHandlerChannel(t, upstream.URL, false)
	engine := gin.New()
	engine.POST("/refresh-model/:id", refreshChannelModels)
	assertEnabledOnlyHandlerSkip(t, enabledOnlyHandlerRequest(engine, fmt.Sprint(id), `{"enabled_only":true}`), model.ChannelRefreshSkipDisabled)
	if sends.Load() != 0 || op.ChannelRefreshStatus(id).Last != nil {
		t.Fatal("disabled batch request sent a probe or saved a diagnostic")
	}
	for _, body := range []string{`{}`, `{"enabled_only":false}`} {
		response := enabledOnlyHandlerRequest(engine, fmt.Sprint(id), body)
		var envelope struct {
			Data model.ChannelModelRefreshResult `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusOK || !envelope.Data.Success || !envelope.Data.ZeroModels || envelope.Data.Skipped {
			t.Fatalf("explicit disabled refresh changed: %d %s", response.Code, response.Body.String())
		}
	}
	if sends.Load() != 2 {
		t.Fatalf("actual explicit sends=%d, want 2", sends.Load())
	}
	if err := op.ChannelEnabled(id, true, t.Context()); err != nil {
		t.Fatal(err)
	}
	response := enabledOnlyHandlerRequest(engine, fmt.Sprint(id), `{"enabled_only":true,"automatic":true}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"success":true`) || sends.Load() != 3 {
		t.Fatal("manual batch became subject to the automatic task switch")
	}
	t.Log("disabled batch probes=0; omitted/false explicit probes=2; enabled manual batch=1")
}

func TestRefreshChannelModelsEnabledOnlyHandlerReturnsBusySkip(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	finish := sync.OnceFunc(func() { close(release) })
	var sends atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sends.Add(1)
		close(started)
		<-release
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer upstream.Close()
	defer finish()
	id := newEnabledOnlyHandlerChannel(t, upstream.URL, true)
	engine := gin.New()
	engine.POST("/refresh-model/:id", refreshChannelModels)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- enabledOnlyHandlerRequest(engine, fmt.Sprint(id), `{}`) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first request did not reach upstream")
	}
	assertEnabledOnlyHandlerSkip(t, enabledOnlyHandlerRequest(engine, fmt.Sprint(id), `{"enabled_only":true}`), model.ChannelRefreshSkipInProgress)
	if response := enabledOnlyHandlerRequest(engine, fmt.Sprint(id), `{}`); response.Code != http.StatusConflict {
		t.Fatalf("explicit busy request returned %d", response.Code)
	}
	if sends.Load() != 1 || !op.ChannelRefreshStatus(id).Running || op.ChannelRefreshStatus(id).Last != nil {
		t.Fatal("busy requests overwrote the first task or sent another probe")
	}
	finish()
	select {
	case response := <-done:
		if response.Code != http.StatusOK {
			t.Fatalf("first task returned %d", response.Code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first refresh did not complete")
	}
	t.Log("actual sends=1; enabled_only busy=200/skipped; explicit busy=409")
}
