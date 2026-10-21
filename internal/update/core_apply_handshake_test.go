package update

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestReadyFileParsesWithAndWithoutBOM(t *testing.T) {
	cases := []struct {
		name  string
		data  []byte
		ready bool
		valid bool
	}{
		{name: "bom true", data: append([]byte{0xef, 0xbb, 0xbf}, []byte(`{"ready":true}`)...), ready: true, valid: true},
		{name: "utf8 true", data: []byte(`{"ready":true}`), ready: true, valid: true},
		{name: "bom false", data: append([]byte{0xef, 0xbb, 0xbf}, []byte(`{"ready":false}`)...), valid: true},
		{name: "truncated", data: []byte(`{"ready":tr`), valid: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var result struct {
				Ready bool `json:"ready"`
			}
			err := json.Unmarshal(trimJSONBOM(tc.data), &result)
			if (err == nil) != tc.valid || (err == nil && result.Ready != tc.ready) {
				t.Fatalf("data=%q ready=%v err=%v", tc.data, result.Ready, err)
			}
		})
	}
}

func TestWindowsUpdateSchedulerCompletesReadyHandshake(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows update handshake requires a Windows host")
	}
	if _, err := exec.LookPath("powershell.exe"); err != nil {
		t.Skip("Windows PowerShell is unavailable")
	}
	runtimeDir := t.TempDir()
	stage := t.TempDir()
	execPath := filepath.Join(runtimeDir, "octopus-roam.exe")
	newExecutable := filepath.Join(stage, "octopus-roam.exe")
	if err := os.WriteFile(execPath, []byte("old"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newExecutable, []byte("new"), 0o700); err != nil {
		t.Fatal(err)
	}
	command, err := scheduleWindowsUpdate(execPath, newExecutable, stage)
	if err != nil {
		t.Fatal(err)
	}
	if command == nil || command.Process == nil {
		t.Fatal("scheduler returned no running helper")
	}
	defer func() {
		_ = command.Process.Kill()
		// scheduleWindowsUpdate owns Wait in its reaper goroutine. A second
		// Wait here races with that goroutine inside os/exec.
	}()
	readyData, err := os.ReadFile(filepath.Join(stage, ".update-ready.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(readyData) >= 3 && string(readyData[:3]) == "\xef\xbb\xbf" {
		t.Fatal("ready file contains a UTF-8 BOM")
	}
	var ready struct {
		Ready bool `json:"ready"`
	}
	if err := json.Unmarshal(readyData, &ready); err != nil || !ready.Ready {
		t.Fatalf("ready file=%q err=%v", readyData, err)
	}
	if got := mustReadHandshakeFile(t, execPath); string(got) != "old" {
		t.Fatalf("runtime executable changed during handshake: %q", got)
	}
}

func mustReadHandshakeFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
