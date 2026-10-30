package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var ErrBusy = errors.New("另一个启动、关闭或更新操作正在进行")
var errProcessMismatch = errors.New("进程不属于此部署")
var errProcessExited = errors.New("process has exited")

// Resolve the final object directly: walking every ancestor unnecessarily
// requires directory-list permissions and fails for valid 8.3 TEMP paths.
func canonicalPath(path string) (string, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	buffer := make([]uint16, 32768)
	size, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
	if err != nil {
		return "", err
	}
	if size >= uint32(len(buffer)) {
		return "", fmt.Errorf("path is too long")
	}
	result := windows.UTF16ToString(buffer[:size])
	if strings.HasPrefix(result, `\\?\UNC\`) {
		result = `\\` + strings.TrimPrefix(result, `\\?\UNC\`)
	} else {
		result = strings.TrimPrefix(result, `\\?\`)
	}
	return filepath.Clean(result), nil
}

func ExecutablePath() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	return canonicalPath(path)
}

// Windows mutex ownership belongs to an OS thread, not to a Go goroutine.
// The returned function must be called by the acquiring goroutine.
func Lock(directory, suffix string, timeout time.Duration) (func(), error) {
	directory, err := Directory(directory)
	if err != nil {
		return nil, err
	}
	runtime.LockOSThread()
	name, err := windows.UTF16PtrFromString(`Local\OctopusRoam-` + Identity(directory) + suffix)
	if err != nil {
		runtime.UnlockOSThread()
		return nil, err
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if handle != 0 {
			windows.CloseHandle(handle)
		}
		runtime.UnlockOSThread()
		return nil, err
	}
	result, err := windows.WaitForSingleObject(handle, uint32(timeout.Milliseconds()))
	if err != nil || (result != windows.WAIT_OBJECT_0 && result != windows.WAIT_ABANDONED) {
		windows.CloseHandle(handle)
		runtime.UnlockOSThread()
		if err != nil {
			return nil, err
		}
		return nil, ErrBusy
	}
	return func() { windows.ReleaseMutex(handle); windows.CloseHandle(handle); runtime.UnlockOSThread() }, nil
}

func Hidden(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}

// A scheduled console executable may receive a console from Task Scheduler.
// Detach only this process; the server itself is created without a console.
func DetachConsole() {
	windows.NewLazySystemDLL("kernel32.dll").NewProc("FreeConsole").Call()
}

type Process struct {
	PID     int
	Created uint64
	handle  windows.Handle
}

func OpenProcess(pid int, expectedExecutable string) (*Process, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("invalid process ID")
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*Process, error) { windows.CloseHandle(handle); return nil, err }
	if result, waitErr := windows.WaitForSingleObject(handle, 0); waitErr != nil {
		return fail(waitErr)
	} else if result == windows.WAIT_OBJECT_0 {
		return fail(errProcessExited)
	}
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil {
		if result, _ := windows.WaitForSingleObject(handle, 0); result == windows.WAIT_OBJECT_0 {
			return fail(errProcessExited)
		}
		return fail(err)
	}
	actual := windows.UTF16ToString(buffer[:size])
	expectedExecutable, err = canonicalPath(expectedExecutable)
	if err != nil {
		return fail(err)
	}
	actual, err = canonicalPath(actual)
	if err != nil {
		return fail(err)
	}
	if !strings.EqualFold(filepath.Clean(actual), filepath.Clean(expectedExecutable)) {
		return fail(errProcessMismatch)
	}
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		return fail(err)
	}
	return &Process{PID: pid, Created: uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime), handle: handle}, nil
}

func (p *Process) Close() { windows.CloseHandle(p.handle) }
func (p *Process) Wait(timeout time.Duration) (bool, error) {
	result, err := windows.WaitForSingleObject(p.handle, uint32(timeout.Milliseconds()))
	return result == windows.WAIT_OBJECT_0, err
}

func Processes(executable string) ([]*Process, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	var result []*Process
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if !strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), filepath.Base(executable)) {
			continue
		}
		if process, openErr := OpenProcess(int(entry.ProcessID), executable); openErr == nil {
			exited, waitErr := process.Wait(0)
			if !exited && waitErr == nil {
				result = append(result, process)
			} else {
				process.Close()
			}
		}
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		for _, p := range result {
			p.Close()
		}
		return nil, err
	}
	return result, nil
}

func Running(directory string) (*Process, bool, error) {
	data, err := os.ReadFile(filepath.Join(directory, stateName))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var state State
	if json.Unmarshal(data, &state) != nil {
		return nil, false, fmt.Errorf("进程状态文件无效")
	}
	process, err := OpenProcess(state.PID, filepath.Join(directory, "octopus-roam.exe"))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, errProcessMismatch) || errors.Is(err, errProcessExited) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	exited, err := process.Wait(0)
	if err != nil {
		process.Close()
		return nil, false, err
	}
	if exited || process.Created != state.Created {
		process.Close()
		return nil, false, nil
	}
	return process, state.Ready, nil
}

func ReadyFor(directory string, process *Process) (bool, error) {
	data, err := os.ReadFile(filepath.Join(directory, stateName))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return false, err
	}
	return state.PID == process.PID && state.Created == process.Created && state.Ready, nil
}

type Server struct {
	directory string
	state     State
	stop      chan struct{}
	cancel    chan struct{}
	done      chan struct{}
	event     windows.Handle
	unlock    func()
	once      sync.Once
}

// BeginServer holds a separate lifetime lock, also used while updating files.
// A named event works for both console launches and CREATE_NO_WINDOW children.
func BeginServer() (*Server, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	directory, err := Directory(cwd)
	if err != nil {
		return nil, err
	}
	unlock, err := Lock(directory, "-server", 0)
	if err != nil {
		return nil, fmt.Errorf("本目录已有服务运行或正在更新: %w", err)
	}
	name, err := windows.UTF16PtrFromString(`Local\OctopusRoam-` + Identity(directory) + "-stop")
	if err != nil {
		unlock()
		return nil, err
	}
	event, err := windows.CreateEvent(nil, 1, 0, name)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if event != 0 {
			windows.CloseHandle(event)
		}
		unlock()
		return nil, err
	}
	if err := windows.ResetEvent(event); err != nil {
		windows.CloseHandle(event)
		unlock()
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		windows.CloseHandle(event)
		unlock()
		return nil, err
	}
	process, err := OpenProcess(os.Getpid(), executable)
	if err != nil {
		windows.CloseHandle(event)
		unlock()
		return nil, err
	}
	state := State{PID: os.Getpid(), Created: process.Created}
	process.Close()
	if err := WriteJSON(filepath.Join(directory, stateName), state); err != nil {
		windows.CloseHandle(event)
		unlock()
		return nil, err
	}
	s := &Server{directory: directory, state: state, stop: make(chan struct{}), cancel: make(chan struct{}), done: make(chan struct{}), event: event, unlock: unlock}
	go func() {
		defer close(s.done)
		for {
			select {
			case <-s.cancel:
				return
			default:
			}
			result, err := windows.WaitForSingleObject(event, 100)
			if err != nil {
				return
			}
			if result == windows.WAIT_OBJECT_0 {
				close(s.stop)
				return
			}
		}
	}()
	return s, nil
}

func (s *Server) Stop() <-chan struct{} { return s.stop }
func (s *Server) Ready() error {
	s.state.Ready = true
	return WriteJSON(filepath.Join(s.directory, stateName), s.state)
}
func (s *Server) Close() {
	s.once.Do(func() {
		close(s.cancel)
		<-s.done
		windows.CloseHandle(s.event)
		// Keep the stale record for diagnosis. PID + creation time prevents reuse.
		s.unlock()
	})
}

func RequestStop(directory string, timeout time.Duration) error {
	process, _, err := Running(directory)
	if err != nil {
		return err
	}
	if process == nil {
		others, err := Processes(filepath.Join(directory, "octopus-roam.exe"))
		if err != nil {
			return err
		}
		unmanaged := false
		for _, other := range others {
			if other.PID != os.Getpid() {
				unmanaged = true
			}
			other.Close()
		}
		if unmanaged {
			return fmt.Errorf("发现未托管或状态不匹配的进程，请在原终端正常关闭")
		}
		return nil
	}
	defer process.Close()
	name, err := windows.UTF16PtrFromString(`Local\OctopusRoam-` + Identity(directory) + "-stop")
	if err != nil {
		return err
	}
	event, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name)
	if err != nil {
		return fmt.Errorf("无法请求正常退出，请在原终端关闭旧版本: %w", err)
	}
	defer windows.CloseHandle(event)
	if err := windows.SetEvent(event); err != nil {
		return err
	}
	exited, err := process.Wait(timeout)
	if err != nil {
		return err
	}
	if !exited {
		return fmt.Errorf("服务未在 %s 内退出，请检查日志后重试", timeout)
	}
	return nil
}
