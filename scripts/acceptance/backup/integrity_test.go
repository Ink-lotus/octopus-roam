package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/op"
)

func TestIntegrityAcceptanceMatrixSQLite(t *testing.T) {
	if os.Getenv("ROAM_B03_ACCEPTANCE_MATRIX_TEST") != t.Name() {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		command := exec.CommandContext(t.Context(), executable, "-test.run=^"+t.Name()+"$", "-test.timeout=60s")
		command.Env = append(os.Environ(), "ROAM_B03_ACCEPTANCE_MATRIX_TEST="+t.Name())
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("isolated integrity acceptance: %v\n%s", err, output)
		}
		return
	}
	dir := t.TempDir()
	fixture := suite{ctx: t.Context(), opts: options{dialect: "sqlite", backupPath: filepath.Join(dir, "fixture.encrypted.json")}, dsn: filepath.Join(dir, "source.sqlite"), report: &report{}}
	if err := fixture.makeFixture(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(fixture.opts.backupPath)
	if err != nil {
		t.Fatal(err)
	}
	dump, err := op.DecryptBackup(raw, fixturePassword)
	if err != nil {
		t.Fatal(err)
	}
	verify := suite{ctx: t.Context(), opts: options{dialect: "sqlite"}, dsn: filepath.Join(dir, "restored.sqlite"), report: &report{}}
	if err := verify.verifyIntegrity(dump); err != nil {
		t.Fatal(err)
	}
}
