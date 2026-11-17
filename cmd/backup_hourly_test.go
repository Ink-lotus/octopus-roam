package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
)

func TestBackupHourlyConversionNeverOverwritesInputAndRequiresExplicitFlag(t *testing.T) {
	const password = "synthetic-conversion-password"
	t.Setenv("ROAM_HOURLY_TEST_PASSWORD", password)
	dir := t.TempDir()
	input, output := filepath.Join(dir, "old.encrypted.json"), filepath.Join(dir, "new.encrypted.json")
	source := &model.DBDump{Product: "octopus-roam", Version: 1003, StatsHourly: []model.StatsHourly{{Hour: 24, Date: "20261117", StatsMetrics: model.StatsMetrics{InputToken: 42}}}}
	raw, err := op.EncryptBackup(source, password)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, raw, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--input", input, "--output", output, "--password-env", "ROAM_HOURLY_TEST_PASSWORD"}
	command := newBackupHourlyCommand()
	command.SetArgs(args)
	if err := command.ExecuteContext(t.Context()); err == nil {
		t.Fatal("implicit conversion accepted")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("failed command created output")
	}
	command = newBackupHourlyCommand()
	command.SetArgs(append(args, "--archive-legacy-hours"))
	var report bytes.Buffer
	command.SetOut(&report)
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	var summary struct {
		ArchivedRows   int  `json:"archived_rows"`
		DatabaseOpened bool `json:"database_opened"`
	}
	if err := json.Unmarshal(report.Bytes(), &summary); err != nil || summary.ArchivedRows != 1 || summary.DatabaseOpened {
		t.Fatal("bad report", err)
	}
	if bytes.Contains(report.Bytes(), []byte(password)) || bytes.Contains(report.Bytes(), []byte("20261117")) {
		t.Fatal("conversion report exposed contents")
	}
	newBytes, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := op.DecryptBackup(newBytes, password)
	if err != nil || restored.Version != 1004 || len(restored.StatsHourly) != 0 || len(restored.StatsHourlyArchives) != 1 {
		t.Fatal("converted backup incorrect", err)
	}
	command = newBackupHourlyCommand()
	command.SetArgs(append(args, "--archive-legacy-hours"))
	if err := command.ExecuteContext(t.Context()); err == nil {
		t.Fatal("existing output overwritten")
	}
	for path, expected := range map[string][]byte{input: raw, output: newBytes} {
		actual, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(actual, expected) {
			t.Fatal("conversion changed an existing file", err)
		}
	}
}
