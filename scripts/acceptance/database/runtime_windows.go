package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"golang.org/x/sys/windows"
)

// The unmodified executable receives its ordinary --config JSON from a
// one-use Windows named pipe, never from a credential-bearing file or env.
// The pipe rejects remote clients and verifies the connecting process ID.
func (s *suite) productRuntime() error {
	if s.o.executable == "" || s.o.stopScript == "" || s.o.version == "" {
		return errAssertion
	}
	raw, err := os.ReadFile(s.o.executable)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != s.o.executableHash {
		return errAssertion
	}
	directory := filepath.Join(s.o.work, "原始候选 重启")
	if err := os.Mkdir(directory, 0o700); err != nil {
		return err
	}
	executable := filepath.Join(directory, "octopus-roam.exe")
	file, err := os.OpenFile(executable, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return err
	}
	_, err = file.Write(raw)
	if closeErr := file.Close(); err != nil || closeErr != nil {
		return errors.Join(err, closeErr)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if err := s.productCycle(executable, directory, attempt); err != nil {
			return err
		}
	}
	return nil
}

func (s *suite) productCycle(executable, directory string, attempt int) (retErr error) {
	databaseConfig, err := s.productDatabaseConfig()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		return err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	pipeName := `\\.\pipe\octopus-roam-acceptance-` + hex.EncodeToString(nonce[:]) + ".json"
	name, err := windows.UTF16PtrFromString(pipeName)
	if err != nil {
		return err
	}
	pipe, err := windows.CreateNamedPipe(name, windows.PIPE_ACCESS_OUTBOUND|windows.FILE_FLAG_FIRST_PIPE_INSTANCE, windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS, 1, 65536, 0, 15000, nil)
	if err != nil {
		return err
	}
	var closeOnce sync.Once
	closePipe := func() { closeOnce.Do(func() { _ = windows.CloseHandle(pipe) }) }
	defer closePipe()
	command := exec.Command(executable, "start", "--config", pipeName)
	command.Dir = directory
	var captured limitedBuffer
	command.Stdout, command.Stderr = &captured, &captured
	command.Env = []string{}
	for _, entry := range os.Environ() {
		key := strings.ToUpper(strings.SplitN(entry, "=", 2)[0])
		if strings.HasPrefix(key, "OCTOPUS_ROAM_") {
			continue
		}
		command.Env = append(command.Env, entry)
	}
	command.Env = append(command.Env, "OCTOPUS_ROAM_SKIP_STARTUP_PRICE_UPDATE=1", "GOTRACEBACK=none")
	// A private hidden console lets the existing stop helper send Ctrl+Break
	// without broadcasting to the user's shell or existing runtime.
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NEW_CONSOLE | windows.CREATE_NEW_PROCESS_GROUP}
	if err := command.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	exited := false
	stop := func() error {
		if exited {
			return errAssertion
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		stop := exec.CommandContext(ctx, "pwsh", "-NoProfile", "-File", s.o.stopScript, "-TargetProcessId", strconv.Itoa(command.Process.Pid), "-ExpectedExecutable", executable, "-ResultPath", filepath.Join(directory, fmt.Sprintf("normal-stop-%d.json", attempt)))
		stop.Stdout, stop.Stderr = io.Discard, io.Discard
		stop.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if err := stop.Run(); err != nil {
			return err
		}
		select {
		case e := <-done:
			exited = true
			return e
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	defer func() {
		if !exited {
			if err := stop(); err != nil {
				// Only the exact process created above is terminated on failed
				// normal shutdown; that case is reported as a failed test.
				_ = command.Process.Kill()
				select {
				case <-done:
					exited = true
				case <-time.After(5 * time.Second):
				}
				if retErr == nil {
					retErr = err
				}
			}
		}
		if retErr != nil && exited {
			// Only static diagnostic names survive. In particular, never
			// persist the raw startup/database text, even on failed startup.
			text := strings.ToLower(string(captured.data))
			for _, diagnostic := range []struct{ token, name string }{
				{"database init error", "product_database_initialization_failed"},
				{"cache init error", "product_cache_initialization_failed"},
				{"unsupported database type", "product_database_config_not_loaded"},
				{"config file not found", "product_config_file_not_found"},
				{"unknown flag", "product_command_flag_rejected"},
			} {
				if strings.Contains(text, diagnostic.token) {
					s.r.Checks = append(s.r.Checks, check{Name: diagnostic.name, Status: "failed", Error: "validation_failed"})
				}
			}
		}
	}()
	pipeDone := make(chan error, 1)
	go func() {
		err := windows.ConnectNamedPipe(pipe, nil)
		if err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
			pipeDone <- err
			return
		}
		var clientPID uint32
		if err := windows.GetNamedPipeClientProcessId(pipe, &clientPID); err != nil || int(clientPID) != command.Process.Pid {
			pipeDone <- errAssertion
			return
		}
		config := map[string]any{"server": map[string]any{"host": "127.0.0.1", "port": port}, "database": databaseConfig, "log": map[string]string{"level": "error"}}
		encoded, err := json.Marshal(config)
		if err != nil {
			pipeDone <- err
			return
		}
		var written uint32
		if err := windows.WriteFile(pipe, encoded, &written, nil); err != nil || int(written) != len(encoded) {
			pipeDone <- errAssertion
			return
		}
		if err := windows.FlushFileBuffers(pipe); err != nil {
			pipeDone <- err
			return
		}
		// Closing the server end produces EOF for the ordinary file reader.
		// DisconnectNamedPipe instead makes a subsequent Read fail with
		// ERROR_PIPE_NOT_CONNECTED, which Viper correctly rejects.
		closePipe()
		pipeDone <- nil
	}()
	select {
	case err := <-pipeDone:
		if err != nil {
			_ = s.test("product_config_pipe_delivery", func() error { return err })
			return err
		}
		_ = s.test("product_config_pipe_delivery", func() error { return nil })
	case <-time.After(20 * time.Second):
		return context.DeadlineExceeded
	case err := <-done:
		exited = true
		_ = s.test("product_exited_before_config_delivery", func() error { return errAssertion })
		if err != nil {
			return err
		}
		return errAssertion
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return errAssertion }}
	defer client.CloseIdleConnections()
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	ready := false
	for deadline := time.Now().Add(45 * time.Second); time.Now().Before(deadline); {
		response, e := client.Get(base + "/")
		if e == nil {
			_ = response.Body.Close()
			if response.StatusCode == 200 {
				ready = true
				break
			}
		}
		select {
		case <-done:
			exited = true
			return errAssertion
		case <-time.After(150 * time.Millisecond):
		}
	}
	if !ready {
		return context.DeadlineExceeded
	}
	_ = s.test("product_loopback_http_ready", func() error { return nil })
	api := func(endpoint, method string, body any, target any) error {
		var input io.Reader
		if body != nil {
			raw, e := json.Marshal(body)
			if e != nil {
				return e
			}
			input = bytes.NewReader(raw)
		}
		request, e := http.NewRequestWithContext(s.ctx, method, base+endpoint, input)
		if e != nil {
			return e
		}
		if body != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		response, e := client.Do(request)
		if e != nil {
			return e
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return errAssertion
		}
		var wrapper struct {
			Data json.RawMessage `json:"data"`
		}
		if json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&wrapper) != nil {
			return errAssertion
		}
		if target != nil {
			return json.Unmarshal(wrapper.Data, target)
		}
		return nil
	}
	if err := api("/api/v1/user/login", "POST", map[string]any{"username": "admin", "password": "admin", "expire": -1}, nil); err != nil {
		return err
	}
	var version string
	if err := api("/api/v1/update/now-version", "GET", nil, &version); err != nil || version != s.o.version {
		return errAssertion
	}
	var settings []model.Setting
	if err := api("/api/v1/setting/list", "GET", nil, &settings); err != nil {
		return err
	}
	want := "11"
	if attempt == 2 {
		want = "13"
	}
	found := false
	for _, setting := range settings {
		if setting.Key == model.SettingKeyStatsSaveInterval && setting.Value == want {
			found = true
		}
	}
	if !found {
		return errAssertion
	}
	var channel model.ChannelDetail
	if err := api(fmt.Sprintf("/api/v1/channel/detail/%d", fixtureID), "GET", nil, &channel); err != nil {
		return err
	}
	if channel.Enabled || channel.FollowsGlobalAutoUpdate() || channel.MaxConcurrency != 5 || len(channel.Keys) != 1 || channel.Keys[0].Enabled {
		return errAssertion
	}
	var groups []model.Group
	if err := api("/api/v1/group/list", "GET", nil, &groups); err != nil {
		return err
	}
	found = false
	for _, group := range groups {
		if group.ID == fixtureID && len(group.Items) == 1 && group.Items[0].ChannelGrantID == fixtureID {
			found = true
		}
	}
	if !found {
		return errAssertion
	}
	if attempt == 1 {
		if err := api("/api/v1/setting/set", "POST", map[string]string{"key": "stats_save_interval", "value": "13"}, nil); err != nil {
			return err
		}
	} else {
		if err := api("/api/v1/setting/set", "POST", map[string]string{"key": "stats_save_interval", "value": "11"}, nil); err != nil {
			return err
		}
		// Exercise the real import HTTP boundary with the synthetic encrypted
		// backup and a wrong password. Never forward the server's error body.
		backup, e := os.ReadFile(filepath.Join(s.o.work, "backup.encrypted.json"))
		if e != nil {
			return e
		}
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, e := writer.CreateFormFile("file", "synthetic-backup.json")
		if e != nil {
			return e
		}
		if _, e := part.Write(backup); e != nil {
			return e
		}
		if e := writer.WriteField("password", "wrong synthetic password"); e != nil {
			return e
		}
		if e := writer.Close(); e != nil {
			return e
		}
		request, e := http.NewRequestWithContext(s.ctx, "POST", base+"/api/v1/setting/import", &body)
		if e != nil {
			return e
		}
		request.Header.Set("Content-Type", writer.FormDataContentType())
		response, e := client.Do(request)
		if e != nil {
			return e
		}
		_ = response.Body.Close()
		if response.StatusCode < 400 || response.StatusCode >= 500 {
			return errAssertion
		}
	}
	return stop()
}
