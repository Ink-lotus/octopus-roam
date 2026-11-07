package desktop

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestSchedulerCreateTaskXML(t *testing.T) {
	a := &Autostart{
		directory: filepath.Join(t.TempDir(), "runtime 测试 𐐷 & O'Brien % !"),
		userID:    "S-1-5-21-100-200-300-1001",
	}
	definition := a.Definition()
	original := bytes.Clone(definition)
	var temporary string
	var taskXML []byte
	var exportedXML []byte
	scheduler := Scheduler{run: func(args ...string) ([]byte, error) {
		if slices.Equal(args, []string{"/Query", "/TN", a.Name(), "/XML"}) {
			return exportedXML, nil
		}
		if len(args) != 5 || !slices.Equal(args[:4], []string{"/Create", "/TN", a.Name(), "/XML"}) {
			t.Fatalf("unexpected scheduler command (must not force overwrite): %q", args)
		}
		temporary = args[4]
		var err error
		taskXML, err = os.ReadFile(temporary)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(taskXML, []byte{0xff, 0xfe}) {
			t.Error("task file is missing the UTF-16LE BOM")
		}

		// Use Windows' XML parser against the actual file passed to /XML.
		// NewTask only creates an in-memory definition; no task is registered.
		systemDirectory, err := windows.GetSystemDirectory()
		if err != nil {
			t.Fatal(err)
		}
		script := `
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
$service = $null
$task = $null
try {
    $service = New-Object -ComObject 'Schedule.Service'
    $service.Connect()
    $task = $service.NewTask(0)
    $task.XmlText = [IO.File]::ReadAllText($env:OCTOPUS_TEST_TASK_XML)
    [pscustomobject]@{
        Command = $task.Actions.Item(1).Path
        Arguments = $task.Actions.Item(1).Arguments
        Directory = $task.Actions.Item(1).WorkingDirectory
        UserID = $task.Principal.UserId
        TriggerUserID = $task.Triggers.Item(1).UserId
        LogonType = $task.Principal.LogonType
        RunLevel = $task.Principal.RunLevel
        XML = $task.XmlText
    } | ConvertTo-Json -Compress
} catch {
    Write-Output $_.Exception.Message
    exit 1
} finally {
    foreach ($item in @($task, $service)) {
        if ($null -ne $item) { [Runtime.InteropServices.Marshal]::FinalReleaseComObject($item) | Out-Null }
    }
}
`
		command := exec.Command(filepath.Join(systemDirectory, "WindowsPowerShell", "v1.0", "powershell.exe"),
			"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script)
		command.Env = append(os.Environ(), "OCTOPUS_TEST_TASK_XML="+temporary)
		Hidden(command)
		output, err := command.CombinedOutput()
		if err != nil {
			return output, err
		}
		var parsed struct {
			Command, Arguments, Directory, UserID, TriggerUserID string
			LogonType, RunLevel                                  int
			XML                                                  string
		}
		if err := json.Unmarshal(bytes.TrimSpace(output), &parsed); err != nil {
			t.Fatalf("Windows task parser returned invalid JSON: %v: %s", err, output)
		}
		if parsed.Command != filepath.Join(a.directory, "octopus-roam.exe") ||
			parsed.Arguments != a.arguments() || parsed.Directory != a.directory ||
			parsed.UserID != a.userID || parsed.TriggerUserID != a.userID ||
			parsed.LogonType != 3 || parsed.RunLevel != 0 {
			t.Fatalf("task definition changed after Windows parsed it: %+v", parsed)
		}
		exportedXML = []byte(parsed.XML)
		return nil, nil
	}}
	if err := scheduler.Create(a.Name(), definition); err != nil {
		t.Fatalf("Windows rejected the generated task XML: %v", err)
	}
	if !bytes.Equal(definition, original) {
		t.Fatal("creating the task mutated its input definition")
	}
	if _, err := os.Stat(temporary); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary task XML was not cleaned up: %v", err)
	}
	a.store = scheduler
	if state, err := a.State(); err != nil || state != TaskEnabled {
		t.Fatalf("Windows-exported task no longer recognized as owned: state=%s err=%v", state, err)
	}
}

func TestTaskWindowsDefaultsAndAccountNames(t *testing.T) {
	store := &memoryTasks{tasks: make(map[string][]byte)}
	a, err := NewAutostart(`D:\isolated-task-fixture`, store)
	if err != nil {
		t.Fatal(err)
	}
	sid, err := windows.StringToSid(a.userID)
	if err != nil {
		t.Fatal(err)
	}
	account, domain, _, err := sid.LookupAccount("")
	if err != nil {
		t.Fatalf("resolve current Windows account: %v", err)
	}
	accountName := account
	if domain != "" {
		accountName = domain + `\` + account
	}
	userElement := []byte("<UserId>" + xmlText(a.userID) + "</UserId>")
	accountElement := []byte("<UserId>" + xmlText(accountName) + "</UserId>")
	// Windows omits these defaults and can export the trigger user as an account
	// name even though the submitted XML used the same user's SID.
	normalized := bytes.ReplaceAll(a.Definition(), []byte("<Enabled>true</Enabled>"), nil)
	normalized = bytes.ReplaceAll(normalized, []byte("<RunLevel>LeastPrivilege</RunLevel>"), nil)
	for _, testCase := range []struct {
		name       string
		definition []byte
		state      TaskState
	}{
		{"omitted defaults", normalized, TaskEnabled},
		{"trigger account name", bytes.Replace(normalized, userElement, accountElement, 1), TaskEnabled},
		{"both account names", bytes.ReplaceAll(normalized, userElement, accountElement), TaskEnabled},
		{"different trigger user", bytes.Replace(normalized, userElement, []byte("<UserId>S-1-5-32-544</UserId>"), 1), TaskConflict},
		{"missing trigger user", bytes.Replace(normalized, userElement, nil, 1), TaskConflict},
		{"disabled trigger", bytes.Replace(normalized, []byte("<LogonTrigger>"), []byte("<LogonTrigger><Enabled>false</Enabled>"), 1), TaskConflict},
		{"elevated principal", bytes.Replace(normalized, []byte("</Principal>"), []byte("<RunLevel>HighestAvailable</RunLevel></Principal>"), 1), TaskConflict},
		{"disabled task", bytes.Replace(normalized, []byte("<Settings>"), []byte("<Settings><Enabled>false</Enabled>"), 1), TaskDisabled},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			store.tasks[a.Name()] = bytes.Clone(testCase.definition)
			writes := store.writes
			if state, err := a.State(); err != nil || state != testCase.state {
				t.Fatalf("unexpected normalized task state: got=%s want=%s err=%v", state, testCase.state, err)
			}
			if testCase.state == TaskConflict {
				if a.Enable() == nil || a.Disable() == nil || store.writes != writes || !bytes.Equal(store.tasks[a.Name()], testCase.definition) {
					t.Fatal("conflicting task was not protected")
				}
				return
			}
			if err := a.Enable(); err != nil {
				t.Fatal(err)
			}
			if state, err := a.State(); err != nil || state != TaskEnabled {
				t.Fatalf("normalized task could not be enabled: state=%s err=%v", state, err)
			}
			if testCase.state == TaskEnabled && store.writes != writes {
				t.Fatal("enabling an already enabled normalized task rewrote it")
			}
			if err := a.Disable(); err != nil {
				t.Fatalf("owned normalized task could not be removed: %v", err)
			}
		})
	}
}

func TestSchedulerOperationErrors(t *testing.T) {
	commandErr := errors.New("exit status 1")
	detail := "(1,40)::错误: 无法切换编码"
	for _, operation := range []struct {
		name, message string
		run           func(Scheduler) error
	}{
		{"create", "无法创建登录计划任务", func(s Scheduler) error {
			return s.Create("fixture", (&Autostart{directory: `D:\fixture`, userID: "S-1-5-21-100-200-300-1001"}).Definition())
		}},
		{"enable", "无法启用登录计划任务", func(s Scheduler) error { return s.Enable("fixture") }},
		{"delete", "无法移除登录计划任务", func(s Scheduler) error { return s.Delete("fixture") }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			var temporary string
			scheduler := Scheduler{run: func(args ...string) ([]byte, error) {
				if args[0] == "/Create" {
					temporary = args[4]
				}
				return []byte("\r\n" + detail + "\r\n"), commandErr
			}}
			err := operation.run(scheduler)
			if !errors.Is(err, commandErr) || err.Error() != operation.message+": exit status 1\n"+detail {
				t.Fatalf("scheduler error lost its cause or output: %v", err)
			}
			if temporary != "" {
				if _, err := os.Stat(temporary); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("failed creation left temporary task XML: %v", err)
				}
			}
		})
	}
	t.Run("no command output", func(t *testing.T) {
		scheduler := Scheduler{run: func(...string) ([]byte, error) { return nil, commandErr }}
		err := scheduler.Enable("fixture")
		if !errors.Is(err, commandErr) || err.Error() != "无法启用登录计划任务: exit status 1" {
			t.Fatalf("empty output hid command error: %v", err)
		}
	})
}

func TestSchedulerQueryErrors(t *testing.T) {
	queryErr, listErr := errors.New("named query failed"), errors.New("list query failed")
	for _, testCase := range []struct {
		name, list, wantDetail string
		listErr, wantErr       error
	}{
		{"list denied", "ERROR: Access is denied.", "ERROR: Access is denied.", listErr, listErr},
		{"existing task unreadable", "\"\\fixture\",\"N/A\",\"Ready\"\r\n", "ERROR: Task access denied.", nil, queryErr},
		{"missing task", "\"\\another-task\",\"N/A\",\"Ready\"\r\n", "", nil, nil},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			calls := 0
			scheduler := Scheduler{run: func(args ...string) ([]byte, error) {
				calls++
				if slices.Equal(args, []string{"/Query", "/TN", "fixture", "/XML"}) {
					return []byte("ERROR: Task access denied."), queryErr
				}
				if slices.Equal(args, []string{"/Query", "/FO", "CSV", "/NH"}) {
					return []byte(testCase.list), testCase.listErr
				}
				return nil, fmt.Errorf("unexpected query: %q", args)
			}}
			data, exists, err := scheduler.Read("fixture")
			if calls != 2 || data != nil || exists || !errors.Is(err, testCase.wantErr) {
				t.Fatalf("unexpected query result: calls=%d exists=%t err=%v", calls, exists, err)
			}
			if testCase.wantErr != nil && !strings.Contains(err.Error(), testCase.wantDetail) {
				t.Fatalf("query error lost scheduler output: %v", err)
			}
		})
	}
}
