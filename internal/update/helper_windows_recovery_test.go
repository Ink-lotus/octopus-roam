//go:build windows

package update

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// A process can exit while a reader still holds its executable open. Recovery
// must wait for a short sharing conflict instead of leaving the new exe behind.
func TestNativeHelperRecoveryWithTransientExecutableLock(t *testing.T) {
	fixtures := t.TempDir()
	oldExe := buildNativeUpdateFixture(t, fixtures, "old", false)
	newExe := buildNativeUpdateFixture(t, fixtures, "new", false)
	f := prepareNativeUpdateFixture(t, oldExe, newExe)
	plan, err := createWindowsPlan(f.executable, f.stage, f.parent.Process.Pid, []string{"--recover"}, f.root)
	if err != nil {
		t.Fatal(err)
	}
	command, done, err := startWindowsHelper(plan)
	if err != nil {
		t.Fatal(err)
	}
	finished := false
	t.Cleanup(func() {
		if t.Failed() {
			if data, err := os.ReadFile(filepath.Join(f.stage, ".update-helper.log")); err == nil {
				redact := strings.NewReplacer(plan.Runtime, "<runtime>", plan.Stage, "<stage>", f.root, "<runtime>", f.stage, "<stage>")
				t.Logf("native helper failure: %s", redact.Replace(string(data)))
			}
		}
		if !finished {
			_ = command.Process.Kill()
			<-done
		}
	})
	if ready := waitForNativeFile(t, filepath.Join(f.stage, ".update-ready.json")); !bytes.Contains(ready, []byte(`"ready":true`)) {
		t.Fatalf("helper refused: %s", ready)
	}
	if err := os.WriteFile(f.stopParent, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if result := waitForNativeFile(t, f.resultPath); !bytes.Contains(result, []byte(`"marker":"new"`)) {
		t.Fatalf("new fixture did not start: %s", result)
	}
	name, err := windows.UTF16PtrFromString(f.executable)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if handle != 0 {
			windows.CloseHandle(handle)
		}
	}()
	if err := os.WriteFile(f.stopRestart, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var state struct{ Failed bool }
	if data := waitForNativeFile(t, filepath.Join(f.root, ".octopus-roam-update.json")); json.Unmarshal(data, &state) != nil || !state.Failed {
		t.Fatalf("new fixture was not rejected during startup: %s", data)
	}
	// The old fixture uses the same stop path. Permit its eventual restart while
	// keeping the actual Windows handle locked long enough to exercise recovery.
	if err := os.Remove(f.stopRestart); err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	if err := windows.CloseHandle(handle); err != nil {
		t.Fatal(err)
	}
	handle = 0
	select {
	case err := <-done:
		finished = true
		if err == nil {
			t.Fatal("failed startup reported success")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("helper did not finish recovery")
	}
	if !bytes.Equal(mustReadFile(t, f.executable), mustReadFile(t, oldExe)) {
		t.Fatal("failed update did not restore the previous executable after the reader released it")
	}
	if result := waitForNativeFile(t, f.resultPath); !bytes.Contains(result, []byte(`"marker":"old"`)) {
		t.Fatalf("previous program did not restart: %s", result)
	}
	if string(mustReadFile(t, filepath.Join(f.root, "octopus-roam.cmd"))) != "old-menu" {
		t.Fatal("previous menu was not restored")
	}
	if string(mustReadFile(t, filepath.Join(f.root, "data", "sentinel"))) != "keep-data" {
		t.Fatal("recovery changed user data")
	}
}
