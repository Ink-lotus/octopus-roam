package update

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/desktop"
	"golang.org/x/sys/windows"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == "__update-apply" {
		if err := RunWindowsUpdateHelper(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func buildNativeUpdateFixture(t *testing.T, root, marker string, earlyExit bool) string {
	t.Helper()
	source := fmt.Sprintf(`package main
import ("encoding/json"; "os"; "time")
func main() {
 if path:=os.Getenv("OCTO_FIXTURE_RESULT"); path!="" {
  cwd,_:=os.Getwd(); data,_:=json.Marshal(map[string]any{"marker":%q,"cwd":cwd,"args":os.Args[1:],"pid":os.Getpid()})
  _=os.WriteFile(path,data,0600)
 }
 if %t { return }
 deadline:=time.Now().Add(45*time.Second)
 for time.Now().Before(deadline) { if _,err:=os.Stat(os.Getenv("OCTO_FIXTURE_STOP"));err==nil{return};time.Sleep(20*time.Millisecond) }
}
`, marker, earlyExit)
	path := filepath.Join(root, marker+".go")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, marker+".exe")
	command := exec.Command("go", "build", "-o", binary, path)
	command.Env = append(os.Environ(), "GO111MODULE=off", "GOTOOLCHAIN=local")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v %s", err, output)
	}
	return binary
}

type nativeFixture struct {
	root, stage, executable, stopParent, stopRestart, resultPath string
	parent                                                       *exec.Cmd
	parentDone                                                   chan error
}

func prepareNativeUpdateFixture(t *testing.T, oldExecutable, newExecutable string) *nativeFixture {
	t.Helper()
	root := filepath.Join(t.TempDir(), "运行目录 [test] & O'Brien % !")
	stage := filepath.Join(t.TempDir(), "准备目录 [test] & O'Brien % !")
	for _, path := range []string{root, stage, filepath.Join(root, "data")} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fixture := &nativeFixture{root: root, stage: stage, executable: filepath.Join(root, "octopus-roam.exe"), stopParent: filepath.Join(root, "parent.stop"), stopRestart: filepath.Join(root, "restart.stop"), resultPath: filepath.Join(root, "restart.json")}
	if err := copyFileForTest(oldExecutable, fixture.executable); err != nil {
		t.Fatal(err)
	}
	if err := copyFileForTest(newExecutable, filepath.Join(stage, "octopus-roam.exe")); err != nil {
		t.Fatal(err)
	}
	for path, value := range map[string]string{filepath.Join(root, "octopus-roam.cmd"): "old-menu", filepath.Join(stage, "octopus-roam.cmd"): "new-menu", filepath.Join(root, "data", "sentinel"): "keep-data"} {
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fixture.parent = exec.Command(fixture.executable)
	fixture.parent.Env = append(os.Environ(), "OCTO_FIXTURE_RESULT=", "OCTO_FIXTURE_STOP="+fixture.stopParent)
	desktop.Hidden(fixture.parent)
	if err := fixture.parent.Start(); err != nil {
		t.Fatal(err)
	}
	fixture.parentDone = make(chan error, 1)
	go func() { fixture.parentDone <- fixture.parent.Wait() }()
	t.Setenv("OCTO_FIXTURE_RESULT", fixture.resultPath)
	t.Setenv("OCTO_FIXTURE_STOP", fixture.stopRestart)
	t.Cleanup(func() {
		_ = os.WriteFile(fixture.stopParent, nil, 0o600)
		_ = os.WriteFile(fixture.stopRestart, nil, 0o600)
		select {
		case <-fixture.parentDone:
		case <-time.After(5 * time.Second):
			_ = fixture.parent.Process.Kill()
			t.Error("fixture parent did not exit")
		}
		for _, path := range []string{fixture.executable, filepath.Join(stage, ".update-helper.exe")} {
			processes, _ := desktop.Processes(path)
			for _, process := range processes {
				exited, _ := process.Wait(5 * time.Second)
				if !exited {
					if owned, err := os.FindProcess(process.PID); err == nil {
						_ = owned.Kill()
					}
					t.Error("owned fixture did not exit")
				}
				process.Close()
			}
		}
	})
	return fixture
}

func waitForNativeFile(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && json.Valid(data) {
			return data
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("fixture output did not appear: %s", filepath.Base(path))
	return nil
}

func TestNativeHelperReplacementAndRecovery(t *testing.T) {
	fixtures := t.TempDir()
	oldExe := buildNativeUpdateFixture(t, fixtures, "old", false)
	newExe := buildNativeUpdateFixture(t, fixtures, "new", false)
	badExe := buildNativeUpdateFixture(t, fixtures, "failed", true)
	for _, testCase := range []struct {
		name, replacement, marker, menu             string
		success, blockBackup, lockMenu, changeStage bool
	}{
		{"replace", newExe, "new", "new-menu", true, false, false, false},
		{"recover failed restart", badExe, "old", "old-menu", false, false, false, false},
		{"recover backup failure", newExe, "old", "old-menu", false, true, false, false},
		{"recover locked menu", newExe, "old", "old-menu", false, false, true, false},
		{"recover changed stage", newExe, "old", "old-menu", false, false, false, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			f := prepareNativeUpdateFixture(t, oldExe, testCase.replacement)
			workdir := filepath.Join(f.root, "工作目录")
			if err := os.MkdirAll(workdir, 0o755); err != nil {
				t.Fatal(err)
			}
			args := []string{"--config", filepath.Join(workdir, "配置.json"), `quote"tail\`}
			plan, err := createWindowsPlan(f.executable, f.stage, f.parent.Process.Pid, args, workdir)
			if err != nil {
				t.Fatal(err)
			}
			command, done, err := startWindowsHelper(plan)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if command.ProcessState == nil {
					_ = command.Process.Kill()
				}
			})
			ready := waitForNativeFile(t, filepath.Join(f.stage, ".update-ready.json"))
			if !bytes.Contains(ready, []byte(`"ready":true`)) {
				t.Fatalf("helper refused: %s", ready)
			}
			if got := mustReadFile(t, f.executable); !bytes.Equal(got, mustReadFile(t, oldExe)) {
				t.Fatal("helper replaced running executable before parent exit")
			}
			if _, err := os.Stat(filepath.Join(f.root, "backups")); !os.IsNotExist(err) {
				t.Fatal("handshake modified runtime")
			}
			if testCase.lockMenu {
				name, err := windows.UTF16PtrFromString(filepath.Join(f.root, "octopus-roam.cmd"))
				if err != nil {
					t.Fatal(err)
				}
				handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, 0, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer windows.CloseHandle(handle)
			}
			if testCase.changeStage {
				if err := os.WriteFile(filepath.Join(f.stage, "octopus-roam.cmd"), []byte("changed-after-ready"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if testCase.blockBackup {
				if err := os.WriteFile(filepath.Join(f.root, "backups"), []byte("blocked"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(f.stopParent, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			select {
			case err = <-done:
				if testCase.success && err != nil {
					t.Fatalf("helper failed: %v", err)
				}
				if !testCase.success && err == nil {
					t.Fatal("failed restart reported success")
				}
			case <-time.After(25 * time.Second):
				t.Fatal("helper did not finish")
			}
			if !testCase.success && !bytes.Equal(mustReadFile(t, f.executable), mustReadFile(t, oldExe)) {
				t.Fatal("failed update did not restore the previous executable")
			}
			var result struct {
				Marker string
				Cwd    string
				Args   []string
				PID    int
			}
			if err := json.Unmarshal(waitForNativeFile(t, f.resultPath), &result); err != nil {
				t.Fatal(err)
			}
			if result.Marker != testCase.marker || result.Cwd != workdir || fmt.Sprint(result.Args) != fmt.Sprint(args) {
				t.Fatalf("restart arguments or result changed: %+v", result)
			}
			if string(mustReadFile(t, filepath.Join(f.root, "octopus-roam.cmd"))) != testCase.menu {
				t.Fatal("menu replacement/rollback failed")
			}
			if string(mustReadFile(t, filepath.Join(f.root, "data", "sentinel"))) != "keep-data" {
				t.Fatal("data changed")
			}
			var state struct{ Failed bool }
			if json.Unmarshal(mustReadFile(t, filepath.Join(f.root, ".octopus-roam-update.json")), &state) != nil || state.Failed == testCase.success {
				t.Fatal("update status incorrect")
			}
			if testCase.blockBackup {
				if string(mustReadFile(t, filepath.Join(f.root, "backups"))) != "blocked" {
					t.Fatal("backup obstruction was overwritten")
				}
			} else {
				backups, backupErr := os.ReadDir(filepath.Join(f.root, "backups"))
				if backupErr != nil || len(backups) != 1 || !bytes.Equal(mustReadFile(t, filepath.Join(f.root, "backups", backups[0].Name(), "octopus-roam.exe")), mustReadFile(t, oldExe)) {
					t.Fatal("old executable backup missing")
				}
			}
			if files, _ := filepath.Glob(filepath.Join(f.stage, "*.ps1")); len(files) != 0 {
				t.Fatal("native updater generated a PowerShell script")
			}
			_ = os.WriteFile(f.stopRestart, nil, 0o600)
		})
	}
}

func TestNativeSchedulerHandshakeAndBusy(t *testing.T) {
	fixtures := t.TempDir()
	oldExe := buildNativeUpdateFixture(t, fixtures, "parent", false)
	for _, busy := range []bool{false, true} {
		t.Run(fmt.Sprint("busy=", busy), func(t *testing.T) {
			f := prepareNativeUpdateFixture(t, oldExe, oldExe)
			originalPID := currentProcessID
			currentProcessID = func() int { return f.parent.Process.Pid }
			t.Cleanup(func() { currentProcessID = originalPID })
			if busy {
				unlock, err := desktop.Lock(f.root, "", time.Second)
				if err != nil {
					t.Fatal(err)
				}
				defer unlock()
			}
			command, err := scheduleWindowsUpdate(f.executable, filepath.Join(f.stage, "octopus-roam.exe"), f.stage)
			if busy {
				if !errors.Is(err, ErrUpdateBusy) {
					t.Fatalf("wanted busy, got %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if command == nil || command.Process == nil {
					t.Fatal("no helper process")
				}
				process, err := desktop.OpenProcess(command.Process.Pid, filepath.Join(f.stage, ".update-helper.exe"))
				if err != nil {
					t.Fatal(err)
				}
				_ = command.Process.Kill()
				_, _ = process.Wait(5 * time.Second)
				process.Close()
			}
			if !bytes.Equal(mustReadFile(t, f.executable), mustReadFile(t, oldExe)) {
				t.Fatal("handshake modified executable")
			}
		})
	}
}

func TestNativeHelperRejectsChangedStage(t *testing.T) {
	fixtures := t.TempDir()
	exe := buildNativeUpdateFixture(t, fixtures, "parent", false)
	f := prepareNativeUpdateFixture(t, exe, exe)
	plan, err := createWindowsPlan(f.executable, f.stage, f.parent.Process.Pid, nil, f.root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.stage, "octopus-roam.cmd"), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, done, err := startWindowsHelper(plan)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err == nil {
			t.Fatal("changed package accepted")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("helper did not reject")
	}
	if _, err := os.Stat(filepath.Join(f.stage, ".update-ready.json")); !os.IsNotExist(err) {
		t.Fatal("unverified helper reported ready")
	}
	if !bytes.Equal(mustReadFile(t, f.executable), mustReadFile(t, exe)) {
		t.Fatal("unverified helper modified runtime")
	}
}

func TestWindowsUpdatePathsRejectTraversal(t *testing.T) {
	for _, path := range []string{".", "../outside", "C:\\outside", "..\\outside"} {
		if _, err := safeUpdatePath(t.TempDir(), path); !errors.Is(err, ErrReleaseVerification) {
			t.Fatalf("unsafe path %q: %v", path, err)
		}
	}
	if _, err := safeUpdatePath(t.TempDir(), "windows/octopus-roam-start.ps1"); err != nil {
		t.Fatal(err)
	}
}
