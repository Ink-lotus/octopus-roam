package update

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestUnixRestartRestoresBackupAndExitsNonZeroWhenExecFails(t *testing.T) {
	originalGOOS, originalExec, originalExit, originalShutdown := testRuntimeGOOS, execProcess, exitProcess, shutdownUpdate
	t.Cleanup(func() {
		testRuntimeGOOS, execProcess, exitProcess, shutdownUpdate = originalGOOS, originalExec, originalExit, originalShutdown
	})
	testRuntimeGOOS = "linux"
	shutdownUpdate = func() {}
	execCalls := 0
	exitCode := 0
	execProcess = func(string, []string, []string) error {
		execCalls++
		return errors.New("synthetic exec failure")
	}
	exitProcess = func(code int) { exitCode = code }
	runtimeDir := t.TempDir()
	execPath := filepath.Join(runtimeDir, "octopus-roam")
	backupDir := filepath.Join(runtimeDir, "backup")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(execPath, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "octopus-roam"), []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	restartExecutable(execPath, backupDir)
	data, err := os.ReadFile(execPath)
	if err != nil || string(data) != "old" {
		t.Fatalf("restored executable=%q err=%v", data, err)
	}
	if execCalls != 2 || exitCode != 1 {
		t.Fatalf("execCalls=%d exitCode=%d, want 2 and 1", execCalls, exitCode)
	}
}

func TestUnixRestartExitsNonZeroWhenBackupIsMissing(t *testing.T) {
	originalGOOS, originalExec, originalExit, originalShutdown := testRuntimeGOOS, execProcess, exitProcess, shutdownUpdate
	t.Cleanup(func() {
		testRuntimeGOOS, execProcess, exitProcess, shutdownUpdate = originalGOOS, originalExec, originalExit, originalShutdown
	})
	testRuntimeGOOS = "linux"
	shutdownUpdate = func() {}
	execCalls := 0
	exitCode := 0
	execProcess = func(string, []string, []string) error {
		execCalls++
		return errors.New("synthetic exec failure")
	}
	exitProcess = func(code int) { exitCode = code }
	execPath := filepath.Join(t.TempDir(), "octopus-roam")
	if err := os.WriteFile(execPath, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	restartExecutable(execPath, t.TempDir())
	data, err := os.ReadFile(execPath)
	if err != nil || string(data) != "new" {
		t.Fatalf("executable changed unexpectedly=%q err=%v", data, err)
	}
	if execCalls != 1 || exitCode != 1 {
		t.Fatalf("execCalls=%d exitCode=%d, want 1 and 1", execCalls, exitCode)
	}
}

func TestUnixReplaceReturnsBackupDirectoryContainingPreviousExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix replacement backup test requires Unix rename semantics")
	}
	runtimeDir := t.TempDir()
	execPath := filepath.Join(runtimeDir, "octopus-roam")
	newPath := filepath.Join(runtimeDir, "new")
	if err := os.WriteFile(execPath, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	backupDir, err := replaceUnixExecutable(execPath, newPath)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(filepath.Join(backupDir, "octopus-roam"))
	if err != nil || string(backup) != "old" {
		t.Fatalf("backup=%q err=%v", backup, err)
	}
	installed, err := os.ReadFile(execPath)
	if err != nil || string(installed) != "new" {
		t.Fatalf("installed=%q err=%v", installed, err)
	}
}
