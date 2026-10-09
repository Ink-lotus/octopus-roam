package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/Ink-lotus/octopus-roam/internal/relay"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func defaultsHandlerRequest(t *testing.T, method string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	engine.GET("/api/v1/group/route-defaults", getGroupRouteDefaults)
	engine.POST("/api/v1/group/route-defaults", setGroupRouteDefaults)
	request := httptest.NewRequest(method, "/api/v1/group/route-defaults", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}

func restoreHandlerDefaults(t *testing.T) model.GroupRelayConfig {
	t.Helper()
	config, err := op.GroupRouteDefaultsGet()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, _, err := op.GroupRouteDefaultsSet(config, false, context.Background()); err != nil {
			t.Error(err)
		}
	})
	return config
}

func defaultsPayload(config model.GroupRelayConfig, apply bool) any {
	return struct {
		RelayConfig model.GroupRelayConfig `json:"relay_config"`
		Apply       bool                   `json:"apply_to_existing"`
	}{config, apply}
}

func TestRouteDefaultsAPIContractResetAndEvents(t *testing.T) {
	config := restoreHandlerDefaults(t)
	_, group, itemID := newChannelRouteFixture(t)
	stream := subscribeGroupEventForTest(t)
	events := make(chan groupResponse, 4)
	go func() {
		for event := range stream {
			if snapshot, ok := event.Data.(groupResponse); ok && snapshot.ID == group.ID {
				events <- snapshot
			}
		}
	}()
	config.MemberRetryIntervalSeconds += 10
	config.MemberAffinitySeconds = 300
	response := defaultsHandlerRequest(t, http.MethodPost, defaultsPayload(config, false))
	if response.Code != http.StatusOK {
		t.Fatalf("保存默认策略返回 %d", response.Code)
	}
	var saved struct {
		Data struct {
			RelayConfig   model.GroupRelayConfig `json:"relay_config"`
			AppliedGroups int                    `json:"applied_groups"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Data.AppliedGroups != 0 || saved.Data.RelayConfig.MemberAffinitySeconds != 0 || relay.RouteStateOf(group).CurrentItemID != itemID {
		t.Fatal("仅保存默认策略改变了已有路由或返回了错误的 API 数据")
	}
	select {
	case <-events:
		t.Fatal("仅保存默认策略发布了分组变化")
	default:
	}
	response = defaultsHandlerRequest(t, http.MethodGet, nil)
	var get struct {
		Data model.GroupRelayConfig `json:"data"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &get) != nil || get.Data != saved.Data.RelayConfig {
		t.Fatal("GET 未返回扁平默认策略")
	}
	response = defaultsHandlerRequest(t, http.MethodPost, defaultsPayload(config, true))
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &saved) != nil || saved.Data.AppliedGroups < 1 {
		t.Fatal("应用默认策略未报告受影响分组")
	}
	select {
	case snapshot := <-events:
		if snapshot.RelayConfig.MemberRetryIntervalSeconds != config.MemberRetryIntervalSeconds || snapshot.Runtime.CurrentItemID != 0 {
			t.Fatal("应用后的 changed 事件缺少新策略或未重置路由")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("应用默认策略没有发布分组 changed 事件")
	}
	if state := relay.RouteStateOf(group); state.CurrentItemID != 0 || len(state.Cooldowns) != 0 || state.ProbeItemID != 0 {
		t.Fatal("应用默认策略没有清空旧路由状态")
	}
}

func TestRouteDefaultsFailureDoesNotResetOrPublish(t *testing.T) {
	config := restoreHandlerDefaults(t)
	_, group, itemID := newChannelRouteFixture(t)
	stream := subscribeGroupEventForTest(t)
	callback := "test:reject_default_group_update"
	if err := db.GetDB().Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if row, ok := tx.Statement.Dest.(*model.Group); ok && row.ID == group.ID {
			tx.AddError(errors.New("scripted persistence failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.GetDB().Callback().Update().Remove(callback) })
	changed := config
	changed.MemberRetryIntervalSeconds += 23
	response := defaultsHandlerRequest(t, http.MethodPost, defaultsPayload(changed, true))
	_ = db.GetDB().Callback().Update().Remove(callback)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("事务失败返回 %d, 期望 500", response.Code)
	}
	if state := relay.RouteStateOf(group); state.CurrentItemID != itemID {
		t.Fatal("失败事务重置了仍有效的路由")
	}
	select {
	case <-stream:
		t.Fatal("失败事务发布了成功事件")
	default:
	}
	if current, err := op.GroupRouteDefaultsGet(); err != nil || current != config {
		t.Fatal("失败事务改变了默认策略")
	}
}

func TestRouteDefaultsRejectInvalidAndGenericSettingsWrites(t *testing.T) {
	config := restoreHandlerDefaults(t)
	for _, payload := range []any{nil, map[string]any{}, map[string]any{"relay_config": nil}, map[string]any{"relay_config": map[string]any{"manual_max_attempts": -1}}, map[string]any{"relay_config": map[string]any{"member_cooldown_seconds": -1}}} {
		if response := defaultsHandlerRequest(t, http.MethodPost, payload); response.Code != http.StatusBadRequest {
			t.Fatalf("非法默认策略返回 %d, 期望 400", response.Code)
		}
	}
	engine := gin.New()
	engine.POST("/set", setSetting)
	request := httptest.NewRequest(http.MethodPost, "/set", bytes.NewBufferString(`{"key":"group_route_defaults","value":"{}"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatal("通用 setting/set 没有拒绝默认策略写入")
	}
	if after, _ := op.GroupRouteDefaultsGet(); after != config {
		t.Fatal("非法设置改变了默认策略")
	}
}
