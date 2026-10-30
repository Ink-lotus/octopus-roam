package desktop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("OCTOPUS_NATIVE_FIXTURE") == "1" && len(os.Args) > 1 && os.Args[1] == "start" {
		if _, err := os.Stat("exit-early"); err == nil {
			os.Exit(3)
		}
		server, err := BeginServer()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		host, port, err := Endpoint(".")
		if err != nil {
			server.Close()
			os.Exit(2)
		}
		listener, err := net.Listen("tcp", net.JoinHostPort(host, port))
		if err != nil {
			server.Close()
			os.Exit(2)
		}
		if err := server.Ready(); err != nil {
			listener.Close()
			server.Close()
			os.Exit(2)
		}
		select {
		case <-server.Stop():
		case <-time.After(45 * time.Second):
		}
		_ = os.WriteFile(filepath.Join("data", "shutdown.txt"), []byte("graceful"), 0o600)
		listener.Close()
		server.Close()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type memoryTasks struct {
	tasks   map[string][]byte
	writes  int
	readErr error
}

func (s *memoryTasks) Read(name string) ([]byte, bool, error) {
	value, ok := s.tasks[name]
	return value, ok, s.readErr
}
func (s *memoryTasks) Create(name string, value []byte) error {
	if _, exists := s.tasks[name]; exists {
		return fmt.Errorf("already exists")
	}
	s.tasks[name] = bytes.Clone(value)
	s.writes++
	return nil
}
func (s *memoryTasks) Enable(name string) error {
	s.tasks[name] = bytes.ReplaceAll(s.tasks[name], []byte("<Enabled>false</Enabled>"), []byte("<Enabled>true</Enabled>"))
	s.writes++
	return nil
}
func (s *memoryTasks) Delete(name string) error { delete(s.tasks, name); s.writes++; return nil }

func newFixtureController(t *testing.T) (*Controller, *memoryTasks) {
	t.Helper()
	t.Setenv("OCTOPUS_NATIVE_FIXTURE", "1")
	t.Setenv("OCTOPUS_ROAM_SERVER_HOST", "")
	t.Setenv("OCTOPUS_ROAM_SERVER_PORT", "")
	directory := filepath.Join(t.TempDir(), "runtime [test] & O'Brien % ! 测试")
	if err := os.MkdirAll(filepath.Join(directory, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(self)
	if err != nil {
		t.Fatalf("read fixture binary: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "octopus-roam.exe"), binary, 0o700); err != nil {
		t.Fatal(err)
	}
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("fixture port: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()
	config := fmt.Sprintf(`{"server":{"host":"127.0.0.1","port":%d},"database":{"type":"sqlite","path":"data/data.db"}}`, port)
	if err := os.WriteFile(filepath.Join(directory, "data", "config.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &memoryTasks{tasks: make(map[string][]byte)}
	controller, err := NewController(directory, store)
	if err != nil {
		t.Fatalf("fixture controller: %v", err)
	}
	t.Cleanup(func() {
		if err := controller.Stop(false); err != nil {
			t.Errorf("fixture graceful cleanup: %v", err)
		}
	})
	return controller, store
}

func TestNativeStartStopAndTaskOwnership(t *testing.T) {
	controller, tasks := newFixtureController(t)
	dataPath := filepath.Join(controller.Directory, "data", "sentinel")
	if err := os.WriteFile(dataPath, []byte("preserved"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := controller.Start(false); err != nil {
		t.Fatal(err)
	}
	first, ready, err := Running(controller.Directory)
	if err != nil || first == nil || !ready {
		t.Fatalf("server not ready: %v", err)
	}
	pid := first.PID
	first.Close()
	if tasks.writes != 0 {
		t.Fatal("plain start modified autostart")
	}
	if err := controller.Start(true); err != nil {
		t.Fatal(err)
	}
	second, _, err := Running(controller.Directory)
	if err != nil || second == nil {
		t.Fatal("duplicate start lost server")
	}
	if second.PID != pid {
		t.Fatal("duplicate start created another server")
	}
	second.Close()
	if tasks.writes != 1 {
		t.Fatalf("task writes=%d", tasks.writes)
	}
	if state, err := controller.Autostart.State(); err != nil || state != TaskEnabled {
		t.Fatalf("state=%s err=%v", state, err)
	}
	if err := controller.Stop(false); err != nil {
		t.Fatal(err)
	}
	if state, _ := controller.Autostart.State(); state != TaskEnabled {
		t.Fatal("plain stop removed task")
	}
	if content, err := os.ReadFile(filepath.Join(controller.Directory, "data", "shutdown.txt")); err != nil || string(content) != "graceful" {
		t.Fatal("server did not exit gracefully")
	}
	if err := controller.Start(true); err != nil {
		t.Fatal(err)
	}
	if tasks.writes != 1 {
		t.Fatal("repeated enable rewrote task")
	}
	if err := controller.Stop(true); err != nil {
		t.Fatal(err)
	}
	if state, _ := controller.Autostart.State(); state != TaskDisabled {
		t.Fatal("combined stop left task enabled")
	}
	if content, err := os.ReadFile(dataPath); err != nil || string(content) != "preserved" {
		t.Fatal("data changed")
	}
}

func TestFailedStartsDoNotCreateTask(t *testing.T) {
	for _, failure := range []string{"occupied port", "invalid config", "early exit"} {
		t.Run(failure, func(t *testing.T) {
			controller, tasks := newFixtureController(t)
			switch failure {
			case "occupied port":
				host, port, _ := Endpoint(controller.Directory)
				listener, err := net.Listen("tcp", net.JoinHostPort(host, port))
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
			case "invalid config":
				if err := os.WriteFile(filepath.Join(controller.Directory, "data", "config.json"), []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "early exit":
				if err := os.WriteFile(filepath.Join(controller.Directory, "exit-early"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := controller.Start(true); err == nil {
				t.Fatal("bad start succeeded")
			}
			if tasks.writes != 0 {
				t.Fatal("failed start modified autostart")
			}
		})
	}
}

func TestTaskIsolationConflictAndDisabledTask(t *testing.T) {
	store := &memoryTasks{tasks: make(map[string][]byte)}
	a := &Autostart{directory: `D:\runtime [测试] & O'Brien % !`, userID: "S-1-5-21-100-200-300-1001", store: store}
	b := &Autostart{directory: `D:\different-runtime`, userID: a.userID, store: store}
	if a.Name() == b.Name() {
		t.Fatal("deployments share a task")
	}
	if err := a.Enable(); err != nil {
		t.Fatal(err)
	}
	if err := b.Enable(); err != nil {
		t.Fatal(err)
	}
	if state, err := a.State(); err != nil || state != TaskEnabled {
		t.Fatalf("XML cannot round-trip: %v %s", err, state)
	}
	original := bytes.Clone(store.tasks[a.Name()])
	for _, change := range []func([]byte) []byte{
		func(data []byte) []byte {
			return bytes.Replace(data, []byte("desktop boot"), []byte("desktop stop"), 1)
		},
		func(data []byte) []byte {
			return bytes.Replace(data, []byte("</Actions>"), []byte("<Exec><Command>other.exe</Command></Exec></Actions>"), 1)
		},
		func(data []byte) []byte {
			return bytes.Replace(data, []byte("</Triggers>"), []byte("<BootTrigger/></Triggers>"), 1)
		},
		func(data []byte) []byte {
			return bytes.Replace(data, []byte("LeastPrivilege"), []byte("HighestAvailable"), 1)
		},
	} {
		store.tasks[a.Name()] = change(original)
		before, writes := bytes.Clone(store.tasks[a.Name()]), store.writes
		if state, _ := a.State(); state != TaskConflict {
			t.Fatal("changed task treated as owned")
		}
		if a.Enable() == nil || a.Disable() == nil {
			t.Fatal("changed task was managed")
		}
		if store.writes != writes || !bytes.Equal(before, store.tasks[a.Name()]) {
			t.Fatal("conflicting task modified")
		}
	}
	store.tasks[a.Name()] = bytes.Replace(original, []byte("<StartWhenAvailable>true</StartWhenAvailable><Enabled>true</Enabled>"), []byte("<StartWhenAvailable>true</StartWhenAvailable><Enabled>false</Enabled>"), 1)
	if state, _ := a.State(); state != TaskDisabled {
		t.Fatal("disabled task state missing")
	}
	if err := a.Enable(); err != nil {
		t.Fatal(err)
	}
	if err := a.Disable(); err != nil {
		t.Fatal(err)
	}
	if state, _ := b.State(); state != TaskEnabled {
		t.Fatal("other deployment task was changed")
	}
	store.readErr = fmt.Errorf("permission denied")
	if _, err := b.State(); err == nil {
		t.Fatal("scheduler query failure treated as disabled")
	}
}

func TestStaleStateCannotStopAnotherProcess(t *testing.T) {
	controller, _ := newFixtureController(t)
	// A real PID pointing at another executable must never receive a stop request.
	state := State{PID: os.Getpid(), Created: 1, Ready: true}
	if err := WriteJSON(filepath.Join(controller.Directory, stateName), state); err != nil {
		t.Fatal(err)
	}
	if err := controller.Stop(false); err != nil {
		t.Fatal(err)
	}
	if err := controller.Start(false); err != nil {
		t.Fatal(err)
	}
	process, _, err := Running(controller.Directory)
	if err != nil || process == nil {
		t.Fatal("stale PID prevented start")
	}
	defer process.Close()
	state = State{PID: process.PID, Created: process.Created + 1, Ready: true}
	if err := WriteJSON(filepath.Join(controller.Directory, stateName), state); err != nil {
		t.Fatal(err)
	}
	if err := RequestStop(controller.Directory, time.Second); err == nil {
		t.Fatal("mismatched state was treated as stopped")
	}
	if exited, _ := process.Wait(0); exited {
		t.Fatal("mismatched creation time stopped process")
	}
	state.Created = process.Created
	if err := WriteJSON(filepath.Join(controller.Directory, stateName), state); err != nil {
		t.Fatal(err)
	}
}

func TestEndpointOverridesAndConfigPreservation(t *testing.T) {
	controller, _ := newFixtureController(t)
	path := filepath.Join(controller.Directory, "data", "config.json")
	before, _ := os.ReadFile(path)
	if err := prepare(controller.Directory); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("configuration overwritten")
	}
	t.Setenv("OCTOPUS_ROAM_SERVER_HOST", "::")
	t.Setenv("OCTOPUS_ROAM_SERVER_PORT", "32123")
	url, err := AccessURL(controller.Directory)
	if err != nil || url != "http://127.0.0.1:32123" {
		t.Fatalf("url=%s err=%v", url, err)
	}
	t.Setenv("OCTOPUS_ROAM_SERVER_PORT", "invalid")
	if _, _, err := Endpoint(controller.Directory); err == nil {
		t.Fatal("bad override accepted")
	}
}

func TestCMDMenuDispatchAndLiteralRuntime(t *testing.T) {
	// The menu's executable is a tiny native fixture, so no system tasks can be
	// touched even if menu dispatch regresses.
	directory := filepath.Join(t.TempDir(), "菜单 [test] & O'Brien % !")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	source := `package main
import ("encoding/json"; "os"; "path/filepath")
func main() {
 exe,_:=os.Executable()
 file,err:=os.OpenFile(filepath.Join(filepath.Dir(exe),os.Getenv("OCTO_MENU_LOG")),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600)
 if err!=nil { os.Exit(2) }
 err=json.NewEncoder(file).Encode(os.Args[1:]); closeErr:=file.Close()
 if err!=nil || closeErr!=nil { os.Exit(2) }
 if os.Getenv("OCTO_MENU_FAIL")=="true" && len(os.Args)>2 && os.Args[2]!="status" { os.Exit(1) }
}
`
	sourcePath := filepath.Join(directory, "fixture.go")
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", filepath.Join(directory, "octopus-roam.exe"), sourcePath)
	build.Env = append(os.Environ(), "GO111MODULE=off", "GOTOOLCHAIN=local")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("fixture build: %v %s", err, output)
	}
	menu, err := os.ReadFile(filepath.Join("..", "..", "scripts", "windows", "octopus-roam.cmd"))
	if err != nil {
		t.Fatal(err)
	}
	menuPath := filepath.Join(directory, "octopus-roam.cmd")
	if err := os.WriteFile(menuPath, menu, 0o600); err != nil {
		t.Fatal(err)
	}
	expected, expectedErr := Directory(directory)
	if expectedErr != nil {
		t.Fatal(expectedErr)
	}
	for _, testCase := range []struct {
		name, input, action string
		autostart, fail     bool
	}{
		{"exit", "0", "", false, false},
		{"start", "10", "start", false, false},
		{"start with autostart", "20", "start", true, false},
		{"stop", "30", "stop", false, false},
		{"stop with autostart", "40", "stop", true, false},
		{"failed operation returns to menu", "10", "start", false, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			logName := testCase.name + ".jsonl"
			command := exec.Command("cmd.exe")
			command.Stdin = strings.NewReader(testCase.input + "\r\n")
			command.Env = append(os.Environ(), "OCTO_MENU_LOG="+logName, fmt.Sprintf("OCTO_MENU_FAIL=%t", testCase.fail))
			command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000, CmdLine: `cmd.exe /d /v:off /s /c ""` + menuPath + `""`}
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("menu failed: %v %s", err, output)
			}
			if testCase.fail && !bytes.Contains(output, []byte("操作未完成")) {
				t.Fatalf("failed operation was not explained: %s", output)
			}
			data, err := os.ReadFile(filepath.Join(directory, logName))
			if err != nil {
				t.Fatal(err)
			}
			wantActions := []string{"status"}
			if testCase.action != "" {
				wantActions = append(wantActions, testCase.action, "status")
			}
			calls := bytes.Split(bytes.TrimSpace(data), []byte{'\n'})
			if len(calls) != len(wantActions) {
				t.Fatalf("menu calls: %s", data)
			}
			for index, call := range calls {
				var args []string
				wantCount := 4
				if index == 1 && testCase.autostart {
					wantCount++
				}
				if json.Unmarshal(call, &args) != nil || len(args) != wantCount || args[0] != "desktop" || args[1] != wantActions[index] || args[2] != "--runtime" {
					t.Fatalf("menu arguments: %s", call)
				}
				if wantCount == 5 && args[4] != "--autostart" {
					t.Fatalf("missing autostart flag: %s", call)
				}
				resolved, err := Directory(args[3])
				if err != nil || !strings.EqualFold(resolved, expected) {
					t.Fatalf("runtime changed: %v %s", err, resolved)
				}
			}
		})
	}
}
