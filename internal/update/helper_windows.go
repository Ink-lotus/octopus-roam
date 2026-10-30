package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/desktop"
)

type updateFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
type windowsUpdatePlan struct {
	Schema        int          `json:"schema"`
	ParentPID     int          `json:"parent_pid"`
	ParentCreated uint64       `json:"parent_created"`
	Runtime       string       `json:"runtime"`
	Stage         string       `json:"stage"`
	Directory     string       `json:"directory"`
	Arguments     []string     `json:"arguments"`
	Files         []updateFile `json:"files"`
}
type helperReady struct {
	Ready bool   `json:"ready"`
	Error string `json:"error,omitempty"`
}

func fileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// Publish only complete files. A locked destination must stay intact and must
// not enter the rollback list; otherwise it can prevent recovery of the exe.
func replaceWindowsFile(source, destination, expectedDigest string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	output, err := os.CreateTemp(filepath.Dir(destination), ".octopus-update-*")
	if err != nil {
		return err
	}
	defer os.Remove(output.Name())
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(output, hash), input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if expectedDigest != "" && hex.EncodeToString(hash.Sum(nil)) != expectedDigest {
		return ErrReleaseVerification
	}
	return os.Rename(output.Name(), destination)
}

// Mutations reject symlinks and junctions, including destination parents.
func safeUpdatePath(root, relative string) (string, error) {
	if !filepath.IsLocal(relative) || relative == "." {
		return "", ErrReleaseVerification
	}
	path := root
	for _, part := range strings.Split(filepath.FromSlash(relative), string(filepath.Separator)) {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", ErrReleaseVerification
		}
	}
	return path, nil
}

func createWindowsPlan(execPath, stage string, parentPID int, args []string, directory string) (windowsUpdatePlan, error) {
	runtimeDir, err := desktop.Directory(filepath.Dir(execPath))
	if err != nil {
		return windowsUpdatePlan{}, err
	}
	stage, err = desktop.Directory(stage)
	if err != nil {
		return windowsUpdatePlan{}, err
	}
	if strings.EqualFold(runtimeDir, stage) || isPathInDest(stage, runtimeDir) || isPathInDest(runtimeDir, stage) {
		return windowsUpdatePlan{}, ErrReleaseVerification
	}
	if _, err := validateExtractedPackage(stage, "windows"); err != nil {
		return windowsUpdatePlan{}, ErrReleaseVerification
	}
	parent, err := desktop.OpenProcess(parentPID, execPath)
	if err != nil {
		return windowsUpdatePlan{}, err
	}
	defer parent.Close()
	plan := windowsUpdatePlan{Schema: 1, Runtime: runtimeDir, Stage: stage, ParentPID: parentPID, ParentCreated: parent.Created, Arguments: args, Directory: directory}
	names, err := windowsInstallFiles(stage)
	if err != nil {
		return windowsUpdatePlan{}, err
	}
	for _, name := range names {
		path, err := safeUpdatePath(stage, name)
		if err != nil {
			return windowsUpdatePlan{}, err
		}
		digest, err := fileDigest(path)
		if err != nil {
			return windowsUpdatePlan{}, err
		}
		plan.Files = append(plan.Files, updateFile{Path: name, SHA256: digest})
	}
	return plan, nil
}

func startWindowsHelper(plan windowsUpdatePlan) (*exec.Cmd, <-chan error, error) {
	// Run a copy of the currently trusted program, not the downloaded replacement.
	self, err := desktop.ExecutablePath()
	if err != nil {
		return nil, nil, err
	}
	helper := filepath.Join(plan.Stage, ".update-helper.exe")
	if err := copyFile(self, helper); err != nil {
		return nil, nil, err
	}
	planPath := filepath.Join(plan.Stage, ".update-plan.json")
	if err := desktop.WriteJSON(planPath, plan); err != nil {
		return nil, nil, err
	}
	command := exec.Command(helper, "__update-apply", planPath)
	command.Dir = plan.Stage
	desktop.Hidden(command)
	logFile, err := os.OpenFile(filepath.Join(plan.Stage, ".update-helper.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, err
	}
	defer logFile.Close()
	command.Stdout, command.Stderr = logFile, logFile
	if err := command.Start(); err != nil {
		return nil, nil, err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	return command, done, nil
}

func scheduleWindowsUpdate(execPath, newExecutable, stage string) (*exec.Cmd, error) {
	if !strings.EqualFold(filepath.Join(stage, "octopus-roam.exe"), newExecutable) {
		return nil, ErrReleaseVerification
	}
	directory, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	plan, err := createWindowsPlan(execPath, stage, currentProcessID(), os.Args[1:], directory)
	if err != nil {
		return nil, err
	}
	command, done, err := startWindowsHelper(plan)
	if err != nil {
		return nil, fmt.Errorf("cannot start the native Windows update helper: %w", err)
	}
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	finished := false
	for {
		if data, err := os.ReadFile(filepath.Join(stage, ".update-ready.json")); err == nil {
			var result helperReady
			if json.Unmarshal(trimJSONBOM(data), &result) == nil {
				if result.Ready {
					return command, nil
				}
				if !finished {
					<-done
				}
				if result.Error == "busy" {
					return nil, ErrUpdateBusy
				}
				return nil, fmt.Errorf("native update helper rejected preparation")
			}
		}
		if finished {
			return nil, fmt.Errorf("native update helper exited before becoming ready")
		}
		select {
		case <-done:
			finished = true
		case <-deadline.C:
			// This is our own unpublished helper; the server has not been stopped.
			_ = command.Process.Kill()
			<-done
			return nil, fmt.Errorf("native update helper did not become ready")
		case <-tick.C:
		}
	}
}

func readWindowsPlan(path string) (windowsUpdatePlan, error) {
	var plan windowsUpdatePlan
	data, err := os.ReadFile(path)
	if err != nil {
		return plan, err
	}
	if len(data) > 1024*1024 || json.Unmarshal(data, &plan) != nil || plan.Schema != 1 {
		return plan, ErrReleaseVerification
	}
	stage, err := desktop.Directory(filepath.Dir(path))
	if err != nil || !strings.EqualFold(stage, plan.Stage) {
		return plan, ErrReleaseVerification
	}
	runtimeDir, err := desktop.Directory(plan.Runtime)
	if err != nil || !strings.EqualFold(runtimeDir, plan.Runtime) || isPathInDest(stage, runtimeDir) || isPathInDest(runtimeDir, stage) {
		return plan, ErrReleaseVerification
	}
	self, err := desktop.ExecutablePath()
	if err != nil || !strings.EqualFold(self, filepath.Join(stage, ".update-helper.exe")) {
		return plan, ErrReleaseVerification
	}
	if !filepath.IsAbs(plan.Directory) {
		return plan, ErrReleaseVerification
	}
	for _, name := range []string{"logs", "backups"} {
		if _, err := safeUpdatePath(plan.Runtime, name); err != nil {
			return plan, err
		}
	}
	names, err := windowsInstallFiles(stage)
	if err != nil || len(names) != len(plan.Files) {
		return plan, ErrReleaseVerification
	}
	for index, name := range names {
		if name != plan.Files[index].Path {
			return plan, ErrReleaseVerification
		}
		if _, err := safeUpdatePath(plan.Runtime, name); err != nil {
			return plan, err
		}
		path, err := safeUpdatePath(stage, name)
		if err != nil {
			return plan, err
		}
		digest, err := fileDigest(path)
		if err != nil || digest != plan.Files[index].SHA256 {
			return plan, ErrReleaseVerification
		}
	}
	return plan, nil
}

// This entry never connects to a database or modifies startup configuration.
func RunWindowsUpdateHelper(planPath string) (result error) {
	plan, err := readWindowsPlan(planPath)
	if err != nil {
		return err
	}
	readyPath := filepath.Join(plan.Stage, ".update-ready.json")
	ready := false
	defer func() {
		if !ready {
			reason := "prepare"
			if errors.Is(result, desktop.ErrBusy) {
				reason = "busy"
			}
			_ = desktop.WriteJSON(readyPath, helperReady{Error: reason})
		}
	}()
	unlock, err := desktop.Lock(plan.Runtime, "", 5*time.Second)
	if err != nil {
		return err
	}
	defer unlock()
	target := filepath.Join(plan.Runtime, "octopus-roam.exe")
	parent, err := desktop.OpenProcess(plan.ParentPID, target)
	if err != nil {
		return err
	}
	defer parent.Close()
	if parent.Created != plan.ParentCreated {
		return fmt.Errorf("update parent was replaced")
	}
	if err := desktop.WriteJSON(readyPath, helperReady{Ready: true}); err != nil {
		return err
	}
	ready = true
	exited, err := parent.Wait(60 * time.Second)
	if err != nil {
		return err
	}
	if !exited {
		return fmt.Errorf("timed out waiting for the installed process to exit")
	}
	return applyWindowsPlan(plan)
}

func waitForExecutableRelease(executable string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		processes, err := desktop.Processes(executable)
		if err != nil {
			return err
		}
		count := len(processes)
		for _, process := range processes {
			process.Close()
		}
		if count == 0 {
			return nil
		}
		if !time.Now().Before(deadline) {
			return desktop.ErrBusy
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func restartWindows(plan windowsUpdatePlan) error {
	if _, err := safeUpdatePath(plan.Runtime, "logs"); err != nil {
		return err
	}
	logs := filepath.Join(plan.Runtime, "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		return err
	}
	output, err := os.CreateTemp(logs, "update-restart-*.log")
	if err != nil {
		return err
	}
	defer output.Close()
	command := exec.Command(filepath.Join(plan.Runtime, "octopus-roam.exe"), plan.Arguments...)
	command.Dir, command.Stdout, command.Stderr = plan.Directory, output, output
	desktop.Hidden(command)
	if err := command.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	native := len(plan.Files) == 2 && len(plan.Arguments) > 0 && plan.Arguments[0] == "start"
	if native {
		process, err := desktop.OpenProcess(command.Process.Pid, filepath.Join(plan.Runtime, "octopus-roam.exe"))
		if err != nil {
			return err
		}
		defer process.Close()
		deadline := time.NewTimer(30 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return fmt.Errorf("updated process exited during startup")
			case <-deadline.C:
				return fmt.Errorf("updated process is still initializing; inspect the restart log")
			case <-tick.C:
				ready, err := desktop.ReadyFor(plan.Directory, process)
				if err != nil {
					return err
				}
				if ready {
					return nil
				}
			}
		}
	}
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
		return fmt.Errorf("updated process exited during startup")
	case <-timer.C:
		return nil
	}
}

func applyWindowsPlan(plan windowsUpdatePlan) (result error) {
	target := filepath.Join(plan.Runtime, "octopus-roam.exe")
	guard, err := desktop.Lock(plan.Runtime, "-server", 5*time.Second)
	if err != nil {
		return err
	}
	defer func() {
		if guard != nil {
			guard()
		}
	}()
	if err := waitForExecutableRelease(target, 10*time.Second); err != nil {
		return err
	}
	statePath := filepath.Join(plan.Runtime, ".octopus-roam-update.json")
	var extra []string
	existed := make(map[string]bool)
	backup := ""
	installed := []string{}
	defer func() {
		if result == nil {
			_ = desktop.WriteJSON(statePath, map[string]bool{"failed": false})
			return
		}
		_ = desktop.WriteJSON(statePath, map[string]bool{"failed": true})
		if guard == nil {
			var err error
			guard, err = desktop.Lock(plan.Runtime, "-server", 0)
			if err != nil {
				result = fmt.Errorf("%w; rollback deferred because a process is still running", result)
				return
			}
		}
		if err := waitForExecutableRelease(target, 5*time.Second); err != nil {
			result = fmt.Errorf("%w; rollback deferred because the executable is in use", result)
			return
		}
		for index := len(installed) - 1; index >= 0; index-- {
			relative := installed[index]
			destination, err := safeUpdatePath(plan.Runtime, relative)
			if err != nil {
				result = fmt.Errorf("%w; rollback path rejected", result)
				return
			}
			if existed[relative] {
				err = replaceWindowsFile(filepath.Join(backup, filepath.FromSlash(relative)), destination, "")
			} else {
				err = os.Remove(destination)
				if os.IsNotExist(err) {
					err = nil
				}
			}
			if err != nil {
				result = fmt.Errorf("%w; rollback failed: %v", result, err)
				return
			}
		}
		guard()
		guard = nil
		if err := restartWindows(plan); err != nil {
			result = fmt.Errorf("%w; restarting the previous program failed: %v", result, err)
		}
	}()
	// Recovery is installed before backup creation: a backup failure must also
	// restart the intact old program after its parent has exited.
	if _, err := safeUpdatePath(plan.Runtime, "backups"); err != nil {
		return err
	}
	for _, file := range plan.Files {
		path, err := safeUpdatePath(plan.Runtime, file.Path)
		if err != nil {
			return err
		}
		if info, err := os.Stat(path); err == nil {
			if !info.Mode().IsRegular() {
				return ErrReleaseVerification
			}
			existed[file.Path] = true
			if file.Path != "octopus-roam.exe" {
				extra = append(extra, path)
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	backup, err = makeUpdateBackup(plan.Runtime, target, extra)
	if err != nil {
		return err
	}
	for _, file := range plan.Files {
		source, err := safeUpdatePath(plan.Stage, file.Path)
		if err != nil {
			return err
		}
		destination, err := safeUpdatePath(plan.Runtime, file.Path)
		if err != nil {
			return err
		}
		if err := replaceWindowsFile(source, destination, file.SHA256); err != nil {
			return err
		}
		installed = append(installed, file.Path)
	}
	guard()
	guard = nil
	return restartWindows(plan)
}
