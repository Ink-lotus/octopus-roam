package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/gin-gonic/gin"
)

func TestChannelAutomationSettingAPIAndStats(t *testing.T) {
	engine := gin.New()
	engine.POST("/setting", setSetting)
	engine.GET("/stats", listChannelStats)
	for _, key := range []model.SettingKey{model.SettingKeyChannelAutoUpdateEnabled, model.SettingKeyChannelAutoUpdateInterval, model.SettingKeyChannelAutoAddGroups} {
		old, _ := op.SettingGetString(key)
		t.Cleanup(func() { _ = op.SettingSetString(key, old) })
		value := "true"
		if key == model.SettingKeyChannelAutoUpdateInterval {
			value = "30"
		}
		for _, v := range []string{"invalid", value} {
			raw, _ := json.Marshal(model.Setting{Key: key, Value: v})
			req := httptest.NewRequest(http.MethodPost, "/setting", bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, req)
			want := http.StatusOK
			if v == "invalid" {
				want = http.StatusBadRequest
			}
			if response.Code != want {
				t.Fatalf("setting %s: %d %s", key, response.Code, response.Body.String())
			}
		}
	}
	channel, _, _ := newChannelRouteFixture(t)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/stats", nil))
	var envelope struct {
		Data []model.ChannelStats `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	for _, stats := range envelope.Data {
		if stats.ChannelID == channel {
			if !stats.FollowGlobalAutoUpdate || stats.Refresh == nil || stats.Concurrency.Active != 0 || stats.Concurrency.Waiting != 0 {
				t.Fatalf("missing automation diagnostic: %+v", stats)
			}
			return
		}
	}
	t.Fatal("channel absent from stats")
}
