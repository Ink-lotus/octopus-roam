package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/Ink-lotus/octopus-roam/internal/relay"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
	"gorm.io/gorm"
)

// Process isolation keeps deliberately damaged rows and publisher failures out
// of other HTTP tests. The child uses TestMain's fresh SQLite database.
func isolatedBackupHTTP(t *testing.T) bool {
	t.Helper()
	if os.Getenv("ROAM_B03_HTTP_TEST") == t.Name() {
		return false
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), executable, "-test.run=^"+t.Name()+"$", "-test.timeout=45s")
	command.Env = append(os.Environ(), "ROAM_B03_HTTP_TEST="+t.Name())
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("isolated backup HTTP test: %v\n%s", err, output)
	}
	return true
}

func backupHTTPGraph(id int, upstream string) *model.DBDump {
	return &model.DBDump{Product: "octopus-roam", Version: 1003,
		Channels:         []model.Channel{{ID: id, ChannelConfig: model.ChannelConfig{Name: fmt.Sprintf("b03-http-%d", id), BaseURL: upstream, Enabled: true, AllowedProtocols: model.ProtocolOpenAIChatCompletion}}},
		ChannelKeys:      []model.ChannelKey{{ID: id, ChannelID: id, ChannelKeyConfig: model.ChannelKeyConfig{Name: "fixture-key", Key: "synthetic-private-b03", Enabled: true}}},
		ChannelModels:    []model.ChannelModel{{ID: id, ChannelID: id, Name: "fixture-model"}},
		ChannelGrants:    []model.ChannelGrant{{ID: id, ChannelKeyID: id, ChannelModelID: id, Protocols: model.ProtocolOpenAIChatCompletion}},
		Groups:           []model.Group{{ID: id, Name: fmt.Sprintf("b03-http-group-%d", id), ActiveItemID: id}},
		GroupItems:       []model.GroupItem{{ID: id, GroupID: id, ChannelGrantID: id}},
		WebhookEndpoints: []model.WebhookEndpoint{{ID: id, Name: "fixture", URL: "http://127.0.0.1:1", BodyTemplate: "{}"}},
	}
}

func backupHTTPFingerprint(t *testing.T) [32]byte {
	t.Helper()
	dump, err := op.DBExportAll(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	dump.ExportedAt = time.Time{}
	raw, err := json.Marshal(dump)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(raw)
}

func backupHTTPImport(t *testing.T, engine *gin.Engine, dump *model.DBDump, status int) string {
	t.Helper()
	raw, err := json.Marshal(dump)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, backupRequest(t, raw, ""))
	if response.Code != status || strings.Contains(response.Body.String(), "synthetic-private") {
		t.Fatalf("import status=%d want=%d or unsafe error; body=%s", response.Code, status, response.Body.String())
	}
	return response.Body.String()
}

type backupHTTPPublisher struct{ fail atomic.Bool }

func (p *backupHTTPPublisher) Configure(int, uint64, []model.CooldownRule) (func(), error) {
	if p.fail.Load() {
		return nil, errors.New("synthetic-private-publisher-error")
	}
	return nil, nil
}
func (*backupHTTPPublisher) Remove(int, uint64) (func(), error) { return nil, nil }

func TestBackupIntegrityHTTPAtomicErrorsAndPublication(t *testing.T) {
	if isolatedBackupHTTP(t) {
		return
	}
	engine := gin.New()
	engine.POST("/import", importDB)
	seed := backupHTTPGraph(1, "http://127.0.0.1:1")
	backupHTTPImport(t, engine, seed, http.StatusOK)
	events := subscribeGroupEventForTest(t)
	for _, test := range []struct {
		name   string
		edit   func(*model.DBDump)
		status int
	}{
		{"input", func(d *model.DBDump) { d.GroupItems[0].ChannelGrantID = 999 }, http.StatusBadRequest},
		{"conflict", func(d *model.DBDump) { d.ChannelKeys[0].Key = "synthetic-private-replacement" }, http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := backupHTTPFingerprint(t)
			dump := backupHTTPGraph(1, "http://127.0.0.1:1")
			test.edit(dump)
			backupHTTPImport(t, engine, dump, test.status)
			if before != backupHTTPFingerprint(t) || len(events) != 0 {
				t.Fatal("rejection changed rows or published a partial success")
			}
		})
	}
	before := backupHTTPFingerprint(t)
	const callback = "b03:http_late_write"
	if err := db.GetDB().Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "webhook_endpoints" {
			tx.AddError(tx.Session(&gorm.Session{NewDB: true}).Exec("INSERT INTO b03_missing_http_table (id) VALUES (1)").Error)
		}
	}); err != nil {
		t.Fatal(err)
	}
	backupHTTPImport(t, engine, backupHTTPGraph(100, "http://127.0.0.1:1"), http.StatusInternalServerError)
	_ = db.GetDB().Callback().Create().Remove(callback)
	if before != backupHTTPFingerprint(t) || len(events) != 0 {
		t.Fatal("late SQL failure changed rows or published SSE")
	}
	// time.Time's parsing error includes the invalid field: never echo it.
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, backupRequest(t, []byte(`{"version":5,"exported_at":"synthetic-private-invalid-time"}`), ""))
	if response.Code != 400 || strings.Contains(response.Body.String(), "synthetic-private") {
		t.Fatal("malformed JSON exposed an input value")
	}
	pub := &backupHTTPPublisher{}
	if err := op.RegisterChannelCooldownPublisher(pub); err != nil {
		t.Fatal(err)
	}
	pub.fail.Store(true)
	seed.Channels[0].InputToken = 909
	seed.ChannelKeys[0].Enabled = false
	body := backupHTTPImport(t, engine, seed, http.StatusServiceUnavailable)
	if !strings.Contains(body, "committed") || !strings.Contains(body, "restart") || len(events) != 0 {
		t.Fatal("committed failure did not explain recovery or published success")
	}
	var key model.ChannelKey
	if err := db.GetDB().First(&key, 1).Error; err != nil || key.Enabled {
		t.Fatal("503 did not retain the actual committed state")
	}
	if _, err := op.ChannelRelaySnapshot(1); !errors.Is(err, op.ErrBackupCommitted) {
		t.Fatal("503 left a stale route available")
	}
	if err := op.StatsSaveDB(t.Context()); !errors.Is(err, op.ErrBackupCommitted) {
		t.Fatal("503 allowed stale statistics to overwrite committed data")
	}
	pub.fail.Store(false)
	if err := op.RegisterChannelCooldownPublisher(nil); err != nil {
		t.Fatal(err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatal(err)
	}
	if err := op.StatsSaveDB(t.Context()); err != nil {
		t.Fatal(err)
	}
	channel, err := op.ChannelGet(1)
	if err != nil || channel.InputToken != 909 {
		t.Fatal("recovery lost committed statistics")
	}
}

func TestBackupIntegrityHTTPMismatchCannotForwardSaveOrRefresh(t *testing.T) {
	if isolatedBackupHTTP(t) {
		return
	}
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"fixture","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()
	for _, id := range []int{1, 2} {
		if _, err := op.DBImportIncremental(t.Context(), backupHTTPGraph(id, upstream.URL)); err != nil {
			t.Fatal(err)
		}
	}
	engine := gin.New()
	engine.POST("/forward", relay.Forward(llm.APIFormatOpenAIChatCompletion))
	engine.GET("/detail/:id", getChannelDetail)
	engine.POST("/update", updateChannel)
	engine.POST("/refresh/:id", refreshChannelModels)
	forward := func(ctx context.Context) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/forward", strings.NewReader(`{"model":"b03-http-group-1","messages":[{"role":"user","content":"fixture"}]}`)).WithContext(ctx)
		request.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(response, request)
		return response
	}
	if response := forward(t.Context()); response.Code != 200 || requests.Load() != 1 {
		t.Fatalf("healthy forwarding control failed: %d", response.Code)
	}
	detail, err := op.ChannelDetailGet(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().Model(&model.ChannelKey{}).Where("id = ?", 1).Updates(map[string]any{"channel_id": 2, "name": "legacy-moved"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatal(err)
	}
	before := backupHTTPFingerprint(t)
	for _, stream := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		if stream {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/forward", strings.NewReader(`{"model":"b03-http-group-1","stream":true,"messages":[{"role":"user","content":"fixture"}]}`)).WithContext(ctx)
			request.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(response, request)
		} else {
			forward(ctx)
		}
		cancel()
	}
	raw, _ := json.Marshal(detail)
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/detail/1", nil),
		httptest.NewRequest(http.MethodPost, "/update", bytes.NewReader(raw)),
		httptest.NewRequest(http.MethodPost, "/refresh/1", strings.NewReader(`{"auto_add_groups":true}`)),
	} {
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != 409 || !strings.Contains(response.Body.String(), "db-audit") || strings.Contains(response.Body.String(), "synthetic-private") {
			t.Fatalf("old mismatch HTTP protection: status=%d body=%s", response.Code, response.Body.String())
		}
	}
	group, err := op.GroupGet(1)
	if err != nil || len(group.Items) != 1 || group.Items[0].Available || group.Items[0].UnavailableReason == "" {
		t.Fatal("damaged member disappeared or did not expose an unavailable reason")
	}
	if requests.Load() != 1 || backupHTTPFingerprint(t) != before {
		t.Fatal("mismatch protection sent upstream requests or changed persisted members")
	}
}
