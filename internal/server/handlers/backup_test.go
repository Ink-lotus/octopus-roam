package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/gin-gonic/gin"
)

func TestDecodeBackupRejectsMixedEnvelope(t *testing.T) {
	for _, raw := range []string{
		`null`, `[]`, `{"data":null}`, `{"data":{"data":{"version":5}}}`,
		`{"product":"foreign","data":{"product":"octopus-solo","version":1000}}`,
		`{"channel_keys":[],"data":{"version":5}}`,
		`{"channel_grants":[],"data":{"version":5}}`,
	} {
		var dump model.DBDump
		if err := decodeDBDump([]byte(raw), &dump); err == nil {
			t.Fatalf("accepted mixed/invalid shape: %s", raw)
		}
	}
	for _, raw := range []string{`{"version":0}`, `{"version":5}`, `{"product":"octopus-solo","version":1000}`, `{"code":200,"message":"success","data":{"product":"octopus-solo","version":1000}}`} {
		var dump model.DBDump
		if err := decodeDBDump([]byte(raw), &dump); err != nil {
			t.Fatal(err)
		}
	}
}

func backupRequest(t *testing.T, body []byte, password string) *http.Request {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	part, err := writer.CreateFormFile("file", "backup.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("password", password); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/import", &buffer)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

func TestEncryptedBackupHandlersRoundTrip(t *testing.T) {
	engine := gin.New()
	engine.GET("/export", exportDB)
	engine.POST("/export", exportDB)
	engine.POST("/import", importDB)
	password := "handler-backup-password"
	const id = 820001
	seed := &model.DBDump{Version: 5, Channels: []model.Channel{{ID: id, ChannelConfig: model.ChannelConfig{Name: t.Name(), BaseURL: "https://example.invalid", Enabled: false}}},
		ChannelKeys: []model.ChannelKey{{ID: id, ChannelID: id, ChannelKeyConfig: model.ChannelKeyConfig{Name: "disabled", Key: "handler-synthetic-secret", Enabled: false}}}}
	if _, err := op.DBImportIncremental(t.Context(), seed); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.GetDB().Delete(&model.Channel{}, id); _ = op.InitCache() })
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/export", nil))
	if response.Code != http.StatusMethodNotAllowed || strings.Contains(response.Body.String(), "handler-synthetic-secret") {
		t.Fatal("GET still exports plaintext")
	}
	response = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/export", strings.NewReader(`{"password":"`+password+`"}`))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("export status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Header().Get("Content-Disposition"), ".encrypted.json") {
		t.Fatal("download metadata missing")
	}
	if bytes.Contains(response.Body.Bytes(), []byte("handler-synthetic-secret")) {
		t.Fatal("download leaked plaintext")
	}
	encrypted := bytes.Clone(response.Body.Bytes())
	if err := db.GetDB().Delete(&model.Channel{}, id).Error; err != nil {
		t.Fatal(err)
	}
	for _, pass := range []string{"wrong-password", password} {
		response = httptest.NewRecorder()
		engine.ServeHTTP(response, backupRequest(t, encrypted, pass))
		want := http.StatusOK
		if pass != password {
			want = http.StatusBadRequest
		}
		if response.Code != want {
			t.Fatalf("import status=%d want=%d body=%s", response.Code, want, response.Body.String())
		}
		var count int64
		db.GetDB().Model(&model.Channel{}).Where("id = ?", id).Count(&count)
		if pass != password && count != 0 {
			t.Fatal("wrong password wrote data")
		}
	}
	var channel model.Channel
	var key model.ChannelKey
	if err := db.GetDB().First(&channel, id).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().First(&key, id).Error; err != nil {
		t.Fatal(err)
	}
	if channel.Enabled || key.Enabled || key.Key != "handler-synthetic-secret" {
		t.Fatal("encrypted restore lost state")
	}
	// 原始 JSON、API 包裹和 multipart 均保留上游导入路径。
	for _, body := range []string{`{"version":0}`, `{"version":5}`, `{"code":200,"data":{"version":5}}`} {
		for _, multipartBody := range []bool{false, true} {
			request := httptest.NewRequest(http.MethodPost, "/import", strings.NewReader(body))
			if multipartBody {
				request = backupRequest(t, []byte(body), "")
			}
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("legacy import: %d %s", response.Code, response.Body.String())
			}
		}
	}
}

func TestBackupHandlerLimitsAndGateRelease(t *testing.T) {
	engine := gin.New()
	engine.POST("/export", exportDB)
	engine.POST("/import", importDB)
	for _, body := range []string{`{}`, `{"password":"short"}`, `{"password":"123456789012","unknown":1}`} {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/export", strings.NewReader(body)))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("bad password/body status=%d", response.Code)
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/import", strings.NewReader(`{}`))
	request.ContentLength = op.MaxBackupRequestBytes + 1
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatal("size limit missing")
	}
	backupWork <- struct{}{}
	func() {
		defer func() { <-backupWork }()
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/import", strings.NewReader(`{"version":5}`)))
		if response.Code != http.StatusTooManyRequests {
			t.Fatal("concurrency limit missing")
		}
	}()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	request = httptest.NewRequest(http.MethodPost, "/import", strings.NewReader(`{"version":5}`)).WithContext(ctx)
	engine.ServeHTTP(httptest.NewRecorder(), request)
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/import", strings.NewReader(`{"version":5}`)))
	if response.Code != http.StatusOK {
		t.Fatal("failure/cancellation leaked capacity")
	}
	var parsed any
	if err := json.Unmarshal(response.Body.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
}
