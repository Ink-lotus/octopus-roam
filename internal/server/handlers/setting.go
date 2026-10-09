package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/Ink-lotus/octopus-roam/internal/relay"
	"github.com/Ink-lotus/octopus-roam/internal/server/middleware"
	"github.com/Ink-lotus/octopus-roam/internal/server/resp"
	"github.com/Ink-lotus/octopus-roam/internal/server/router"
	"github.com/Ink-lotus/octopus-roam/internal/task"
	"github.com/Ink-lotus/octopus-roam/internal/webhook"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/setting").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/privacy-rules", http.MethodGet).Handle(getPrivacyRules),
		).
		AddRoute(
			router.NewRoute("/privacy-rules", http.MethodPost).Use(middleware.RequireJSON()).Handle(mutatePrivacyRules),
		).
		AddRoute(
			router.NewRoute("/privacy-preview", http.MethodPost).Use(middleware.RequireJSON()).Handle(previewPrivacy),
		).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(getSettingList),
		).
		AddRoute(
			router.NewRoute("/set", http.MethodPost).
				Use(middleware.RequireJSON()).
				Handle(setSetting),
		).
		AddRoute(
			router.NewRoute("/export", http.MethodGet).
				Handle(exportDB),
		).
		AddRoute(
			router.NewRoute("/export", http.MethodPost).
				Use(middleware.RequireJSON()).
				Handle(exportDB),
		).
		AddRoute(
			router.NewRoute("/import", http.MethodPost).
				Handle(importDB),
		)
}

func getSettingList(c *gin.Context) {
	settings, err := op.SettingList(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, settings)
}

func setSetting(c *gin.Context) {
	var setting model.Setting
	if err := c.ShouldBindJSON(&setting); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	if setting.Key == model.SettingKeyGroupRouteDefaults {
		resp.Error(c, http.StatusBadRequest, "use /api/v1/group/route-defaults to update route defaults")
		return
	}
	if setting.Key == model.SettingKeyPrivacyProtectionRules {
		resp.Error(c, http.StatusBadRequest, "use /api/v1/setting/privacy-rules to update privacy rules")
		return
	}
	if err := setting.Validate(); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := op.SettingSetString(setting.Key, setting.Value); err != nil {
		if setting.Key == model.SettingKeyPrivacyProtectionEnabled {
			privacyError(c, err)
			return
		}
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	switch setting.Key {
	case model.SettingKeyModelInfoUpdateInterval:
		hours, err := strconv.Atoi(setting.Value)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		task.Update(string(setting.Key), time.Duration(hours)*time.Hour)
	}
	resp.Success(c, setting)
}

// 包含读取文件、全库查询与 KDF，避免忙碌时在内存中堆积备份请求。
var backupWork = make(chan struct{}, 1)

func beginBackup(c *gin.Context) bool {
	c.Header("Cache-Control", "no-store")
	if c.Request.Context().Err() != nil {
		resp.Error(c, http.StatusBadRequest, "backup request canceled")
		return false
	}
	select {
	case backupWork <- struct{}{}:
		return true
	default:
		c.Header("Retry-After", "1")
		resp.Error(c, http.StatusTooManyRequests, "another backup operation is running; retry shortly")
		return false
	}
}

func backupReadError(c *gin.Context, err error) {
	var maxBytes *http.MaxBytesError
	if errors.As(err, &maxBytes) || errors.Is(err, op.ErrBackupTooLarge) {
		resp.Error(c, http.StatusRequestEntityTooLarge, op.ErrBackupTooLarge.Error())
		return
	}
	resp.Error(c, http.StatusBadRequest, err.Error())
}

func exportDB(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if c.Request.Method != http.MethodPost {
		c.Header("Allow", http.MethodPost)
		resp.Error(c, http.StatusMethodNotAllowed, "use POST with a backup password to export an encrypted backup")
		return
	}
	if !beginBackup(c) {
		return
	}
	defer func() { <-backupWork }()
	var input struct {
		Password string `json:"password"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		backupReadError(c, fmt.Errorf("invalid export request: %w", err))
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		resp.Error(c, http.StatusBadRequest, "export request must contain one JSON object")
		return
	}
	if err := op.ValidateBackupExportPassword(input.Password); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	defer func() { input.Password = "" }()
	dump, err := op.DBExportAll(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, "could not export backup")
		return
	}

	if c.Request.Context().Err() != nil {
		return
	}
	encrypted, err := op.EncryptBackup(dump, input.Password)
	if errors.Is(err, op.ErrBackupTooLarge) {
		backupReadError(c, err)
		return
	}
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, "could not encrypt backup")
		return
	}
	if c.Request.Context().Err() != nil {
		return
	}
	c.Header("Content-Disposition", "attachment; filename=\"octopus-roam-backup-"+time.Now().UTC().Format("20060102150405")+".encrypted.json\"")
	c.Data(http.StatusOK, "application/json", encrypted)
}

func importDB(c *gin.Context) {
	if !beginBackup(c) {
		return
	}
	defer func() { <-backupWork }()
	var dump model.DBDump
	body, password, err := readBackupUpload(c)
	if err != nil {
		backupReadError(c, err)
		return
	}
	defer func() { clear(body); password = "" }()
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(body, &shape); err != nil || shape == nil {
		resp.Error(c, http.StatusBadRequest, "backup must be a JSON object")
		return
	}
	_, format := shape["format"]
	_, ciphertext := shape["ciphertext"]
	_, kdf := shape["kdf"]
	_, cipher := shape["cipher"]
	if format || ciphertext || kdf || cipher {
		decoded, err := op.DecryptBackup(body, password)
		if err != nil {
			backupReadError(c, err)
			return
		}
		dump = *decoded
	} else {
		if len(body) > op.MaxBackupPlaintextBytes {
			backupReadError(c, op.ErrBackupTooLarge)
			return
		}
		if err := decodeDBDump(body, &dump); err != nil {
			backupReadError(c, err)
			return
		}
	}
	if c.Request.Context().Err() != nil {
		return
	}

	seenLLMNames := make(map[string]struct{}, len(dump.LLMInfos))
	for i := range dump.LLMInfos {
		dump.LLMInfos[i].Name = strings.ToLower(strings.TrimSpace(dump.LLMInfos[i].Name))
		if dump.LLMInfos[i].Name == "" {
			resp.Error(c, http.StatusBadRequest, "model price name cannot be empty")
			return
		}
		if _, ok := seenLLMNames[dump.LLMInfos[i].Name]; ok {
			resp.Error(c, http.StatusBadRequest, "duplicate model price: "+dump.LLMInfos[i].Name)
			return
		}
		seenLLMNames[dump.LLMInfos[i].Name] = struct{}{}
	}
	for i := range dump.WebhookEndpoints {
		if err := webhook.NormalizeAndValidate(&dump.WebhookEndpoints[i]); err != nil {
			resp.Error(c, http.StatusBadRequest, "invalid webhook endpoint in backup: "+err.Error())
			return
		}
	}

	groupMutationMu.Lock()
	defer groupMutationMu.Unlock()
	result, err := op.DBImportIncremental(c.Request.Context(), &dump)
	if err != nil {
		if errors.Is(err, op.ErrBackupWrite) {
			resp.Error(c, http.StatusInternalServerError, op.ErrBackupWrite.Error())
			return
		}
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	if err := op.InitCache(); err != nil {
		resp.Error(c, http.StatusInternalServerError, "backup was imported but caches could not be reloaded; restart the service")
		return
	}
	relay.ResetAllRouteStates()
	relay.SetAllRouteConfigs(op.GroupList())

	resp.Success(c, result)
}

func readBackupUpload(c *gin.Context) ([]byte, string, error) {
	if c.Request.ContentLength > op.MaxBackupRequestBytes {
		return nil, "", op.ErrBackupTooLarge
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, op.MaxBackupRequestBytes)
	if !strings.HasPrefix(c.GetHeader("Content-Type"), "multipart/") {
		body, err := io.ReadAll(c.Request.Body)
		return body, "", err
	}
	reader, err := c.Request.MultipartReader()
	if err != nil {
		return nil, "", fmt.Errorf("invalid multipart backup")
	}
	var body []byte
	var password string
	seen := make(map[string]bool)
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", err
		}
		name := part.FormName()
		if seen[name] || (name != "file" && name != "password") {
			return nil, "", fmt.Errorf("unexpected or duplicate backup field")
		}
		seen[name] = true
		if name == "file" {
			body, err = io.ReadAll(part)
		} else {
			var value []byte
			value, err = io.ReadAll(io.LimitReader(part, 1025))
			if len(value) > 1024 {
				clear(value)
				return nil, "", op.ErrBackupDecrypt
			}
			password = string(value)
			clear(value)
		}
		if err != nil {
			return nil, "", err
		}
		if err := part.Close(); err != nil {
			return nil, "", err
		}
	}
	if !seen["file"] {
		return nil, "", fmt.Errorf("missing upload file field 'file'")
	}
	return body, password, nil
}

func decodeDBDump(body []byte, dump *model.DBDump) error {
	if dump == nil {
		return fmt.Errorf("empty dump destination")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("backup must be a JSON object")
	}
	if data, wrapped := fields["data"]; wrapped {
		for key := range fields {
			if key != "data" && key != "code" && key != "message" {
				return fmt.Errorf("ambiguous backup: mixed dump and response wrapper")
			}
		}
		fields = nil
		if err := json.Unmarshal(data, &fields); err != nil {
			return err
		}
		if fields == nil {
			return fmt.Errorf("empty wrapped backup")
		}
		if _, nested := fields["data"]; nested {
			return fmt.Errorf("nested backup wrapper is not supported")
		}
		body = data
	}
	// 只解析一个层级，不让响应包裹覆盖已有产品身份。
	var decoded model.DBDump
	if err := json.Unmarshal(body, &decoded); err != nil {
		return err
	}
	*dump = decoded
	return nil
}
