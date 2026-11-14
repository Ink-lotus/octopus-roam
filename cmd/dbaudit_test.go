package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/db/integrity"
	"github.com/Ink-lotus/octopus-roam/internal/model"
)

func TestDatabaseAuditCLIReadsWithoutInitializingOrWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.sqlite")
	if err := db.InitDB("sqlite", path, false); err != nil {
		t.Fatal(err)
	}
	// Keep only migration-created settings; do not initialize the cache defaults.
	for _, row := range []any{
		&model.Channel{ID: 1, ChannelConfig: model.ChannelConfig{Name: "fixture", BaseURL: "http://127.0.0.1:1"}},
		&model.Channel{ID: 2, ChannelConfig: model.ChannelConfig{Name: "other", BaseURL: "http://127.0.0.1:1"}},
		&model.ChannelKey{ID: 1, ChannelID: 2, ChannelKeyConfig: model.ChannelKeyConfig{Name: "fixture-key", Key: "synthetic-private-audit"}},
		&model.ChannelModel{ID: 1, ChannelID: 1, Name: "fixture-model"},
		&model.ChannelGrant{ID: 1, ChannelKeyID: 1, ChannelModelID: 1, Protocols: model.ProtocolAll},
	} {
		if err := db.GetDB().Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	var settingsBefore int64
	if err := db.GetDB().Model(&model.Setting{}).Count(&settingsBefore).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		command := newDatabaseAuditCommand()
		var output bytes.Buffer
		command.SetOut(&output)
		command.SetErr(&output)
		command.SetArgs([]string{"--sqlite-path", path})
		err := command.ExecuteContext(t.Context())
		if err == nil {
			t.Fatal("audit did not return a failing exit condition for mismatched data")
		}
		var report integrity.AuditReport
		if err := json.Unmarshal(output.Bytes(), &report); err != nil || !report.Complete || !report.ReadOnly || report.Counts["settings"] != settingsBefore || len(report.Issues) != 1 || report.Issues[0].Code != "cross_channel_grant" {
			t.Fatalf("audit output differs: %v %+v", err, report)
		}
		if strings.Contains(output.String(), "synthetic-private") {
			t.Fatal("audit output exposed credentials")
		}
		after, err := os.ReadFile(path)
		if err != nil || sha256.Sum256(after) != sha256.Sum256(before) {
			t.Fatal("read-only command altered the closed SQLite file")
		}
	}
	missing := filepath.Join(t.TempDir(), "must-not-create.sqlite")
	command := newDatabaseAuditCommand()
	command.SetArgs([]string{"--sqlite-path", missing})
	var output bytes.Buffer
	command.SetOut(&output)
	if err := command.ExecuteContext(t.Context()); err == nil {
		t.Fatal("missing database accepted")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("audit created a missing database")
	}
}
