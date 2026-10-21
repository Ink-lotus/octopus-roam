package update

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/conf"
)

func TestVersionOrderingAndChannelRules(t *testing.T) {
	cases := []struct {
		left, right string
		want        int
	}{
		{"v0.1.10", "v0.1.9", 1},
		{"v0.1.0-rc.10", "v0.1.0-rc.2", 1},
		{"v0.1.0", "v0.1.0-rc.10", 1},
	}
	for _, tc := range cases {
		left, leftOK := parseVersion(tc.left)
		right, rightOK := parseVersion(tc.right)
		if !leftOK || !rightOK || compareVersions(left, right) != tc.want {
			t.Errorf("compareVersions(%q, %q) = %v, want %v", tc.left, tc.right, compareVersions(left, right), tc.want)
		}
	}
	for _, raw := range []string{"dev", "unknown", "v0.1.0-local.1", "v0.1.0-beta.1", "v1.2"} {
		if _, ok := parseVersion(raw); ok {
			t.Errorf("parseVersion(%q) unexpectedly accepted", raw)
		}
	}
	stable, _ := parseVersion("v0.1.0")
	rc, _ := parseVersion("v0.1.0-rc.1")
	if canUpgrade(stable, rc) {
		t.Error("stable builds must not downgrade to an RC")
	}
	if !canUpgrade(rc, stable) {
		t.Error("an RC should upgrade to the corresponding stable release")
	}
	if !canUpgrade(rc, mustParseTestVersion(t, "v0.1.0-rc.2")) {
		t.Error("an RC should upgrade to a later RC")
	}
}

func mustParseTestVersion(t *testing.T, value string) parsedVersion {
	t.Helper()
	version, ok := parseVersion(value)
	if !ok {
		t.Fatalf("parseVersion(%q) failed", value)
	}
	return version
}

func TestSelectReleaseSkipsIneligibleVersions(t *testing.T) {
	releases := []releaseRecord{
		{TagName: "v0.1.0-rc.10", Prerelease: true},
		{TagName: "v0.1.0-rc.2", Prerelease: true},
		{TagName: "v0.1.0"},
		{TagName: "v9.0.0-beta.1", Prerelease: true},
	}
	selected, status := selectRelease("v0.1.0-rc.1", releases)
	if selected == nil || selected.TagName != "v0.1.0" || status != StatusAvailable {
		t.Fatalf("selected=%+v status=%s", selected, status)
	}
	selected, status = selectRelease("v0.1.0", releases)
	if selected == nil || selected.TagName != "v0.1.0" || status != StatusLatest {
		t.Fatalf("stable selected=%+v status=%s", selected, status)
	}
	selected, status = selectRelease("v0.1.0-local.1", releases)
	if selected == nil || selected.TagName != "v0.1.0" || status != StatusDevelopment {
		t.Fatalf("local selected=%+v status=%s", selected, status)
	}
}

func TestContainerStatusBlocksOnlineUpdate(t *testing.T) {
	originalVersion, originalContainer, originalPending := conf.Version, runningInContainer, updatePending
	t.Cleanup(func() {
		conf.Version = originalVersion
		runningInContainer = originalContainer
		updatePending = originalPending
	})
	conf.Version = "v0.1.0"
	runningInContainer = func() bool { return true }
	info, err := GetLatestInfo()
	if err != nil || info.Status != StatusContainer || info.CanUpdate {
		t.Fatalf("container info=%+v err=%v", info, err)
	}
	if _, err := UpdateCore("v0.1.1"); !errors.Is(err, ErrContainerUpdate) {
		t.Fatalf("UpdateCore() error=%v, want container error", err)
	}
}

func TestFailedUpdateStateIsReportedWithoutNetwork(t *testing.T) {
	originalVersion, originalExecutable, originalContainer := conf.Version, executablePath, runningInContainer
	t.Cleanup(func() {
		conf.Version = originalVersion
		executablePath = originalExecutable
		runningInContainer = originalContainer
	})
	conf.Version = "v0.1.0"
	runningInContainer = func() bool { return false }
	runtimeDir := t.TempDir()
	executable := filepath.Join(runtimeDir, "octopus-roam")
	if err := os.WriteFile(executable, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, ".octopus-roam-update.json"), append([]byte{0xef, 0xbb, 0xbf}, []byte(`{"failed":true}`)...), 0o600); err != nil {
		t.Fatal(err)
	}
	executablePath = func() (string, error) { return executable, nil }
	info, err := GetLatestInfo()
	if err != nil || info.Status != StatusUpdateFailed || info.CanUpdate {
		t.Fatalf("info=%+v err=%v", info, err)
	}
}

func TestWindowsUpdateHelperParses(t *testing.T) {
	powershell, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skip("Windows PowerShell is unavailable")
	}
	path := filepath.Join(t.TempDir(), "update-helper.ps1")
	if err := os.WriteFile(path, []byte(windowsUpdateHelper), 0o600); err != nil {
		t.Fatal(err)
	}
	quoted := strings.ReplaceAll(path, "'", "''")
	command := "$ErrorActionPreference='Stop'; [scriptblock]::Create((Get-Content -LiteralPath '" + quoted + "' -Raw)) | Out-Null"
	if output, err := exec.Command(powershell, "-NoProfile", "-NonInteractive", "-Command", command).CombinedOutput(); err != nil {
		t.Fatalf("helper parse failed: %v: %s", err, output)
	}
}

func TestWindowsUpdateHelperReplacesAndRecoversRealProcesses(t *testing.T) {
	powershell, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skip("Windows PowerShell is unavailable")
	}
	fixtureRoot := t.TempDir()
	oldExecutable := buildUpdateFixture(t, fixtureRoot, "old", "30s")
	newExecutable := buildUpdateFixture(t, fixtureRoot, "new", "30s")
	parentExecutable := buildUpdateFixture(t, fixtureRoot, "parent", "1s")

	t.Run("successful replacement preserves restart arguments", func(t *testing.T) {
		runWindowsHelperCase(t, powershell, oldExecutable, newExecutable, parentExecutable, true)
	})
	t.Run("failed restart rolls back and restarts old process", func(t *testing.T) {
		failedExecutable := buildUpdateFixture(t, fixtureRoot, "failed", "0s")
		runWindowsHelperCase(t, powershell, oldExecutable, failedExecutable, parentExecutable, false)
	})
}

func buildUpdateFixture(t *testing.T, root, marker, sleep string) string {
	t.Helper()
	source := fmt.Sprintf(`package main

import (
    "encoding/json"
    "os"
    "strings"
    "time"
)

func main() {
    if path := os.Getenv("OCTO_FIXTURE_RESULT"); path != "" {
        result := map[string]any{"marker": %q, "cwd": must(os.Getwd()), "args": os.Args[1:], "pid": os.Getpid()}
        if data, err := json.Marshal(result); err == nil { _ = os.WriteFile(path, data, 0600) }
    }
    sleep := %s
    for _, arg := range os.Args[1:] {
        if strings.HasPrefix(arg, "--sleep=") {
            if parsed, err := time.ParseDuration(strings.TrimPrefix(arg, "--sleep=")); err == nil { sleep = parsed }
        }
    }
    time.Sleep(sleep)
}

func must(value string, err error) string {
    if err != nil { return "" }
    return value
}
`, marker, durationExpression(sleep))
	sourcePath := filepath.Join(root, marker+".go")
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, marker+".exe")
	command := exec.Command("go", "build", "-trimpath", "-o", output, sourcePath)
	command.Env = append(os.Environ(), "GO111MODULE=off", "GOTOOLCHAIN=local")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build %s fixture: %v: %s", marker, err, output)
	}
	return output
}

func durationExpression(value string) string {
	switch value {
	case "0s":
		return "time.Duration(0)"
	case "1s":
		return "time.Second"
	default:
		return "30 * time.Second"
	}
}

func runWindowsHelperCase(t *testing.T, powershell, oldExecutable, newExecutable, parentExecutable string, wantSuccess bool) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "运行目录 with spaces")
	prepared := filepath.Join(t.TempDir(), "准备目录 with spaces")
	workdir := filepath.Join(root, "工作 目录")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(prepared, "windows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "windows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyFileForTest(oldExecutable, filepath.Join(root, "octopus-roam.exe")); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"octopus-roam.cmd", "windows/octopus-roam-menu.ps1", "windows/octopus-roam-start.ps1", "windows/octopus-roam-autostart.ps1", "windows/send-stop-signal.ps1"} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(relative)), []byte("old-"+relative), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := copyFileForTest(newExecutable, filepath.Join(prepared, "octopus-roam.exe")); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"octopus-roam.cmd", "windows/octopus-roam-menu.ps1", "windows/octopus-roam-start.ps1", "windows/octopus-roam-autostart.ps1", "windows/send-stop-signal.ps1"} {
		if err := os.WriteFile(filepath.Join(prepared, filepath.FromSlash(relative)), []byte("new-"+relative), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	argumentsPath := filepath.Join(prepared, ".update-arguments.json")
	readyPath := filepath.Join(prepared, ".update-ready.json")
	resultPath := filepath.Join(root, "restart result.json")
	arguments := struct {
		Arguments []string `json:"arguments"`
		Directory string   `json:"directory"`
	}{
		Arguments: []string{"--config", filepath.Join(workdir, "配置 文件.json"), `quote"tail\`},
		Directory: workdir,
	}
	data, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(argumentsPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	parent := exec.Command(parentExecutable)
	parent.Env = append(os.Environ(), "OCTO_FIXTURE_RESULT=")
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if parent.Process != nil {
			_ = parent.Process.Kill()
		}
	}()
	command := exec.Command(powershell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "RemoteSigned", "-File", filepath.Join(prepared, ".update-helper.ps1"),
		"-ParentPid", strconv.Itoa(parent.Process.Pid), "-RuntimeDirectory", root, "-PreparedDirectory", prepared,
		"-ExecutableName", "octopus-roam.exe", "-ArgumentsFile", argumentsPath, "-ReadyFile", readyPath)
	if err := os.WriteFile(filepath.Join(prepared, ".update-helper.ps1"), []byte(windowsUpdateHelper), 0o600); err != nil {
		t.Fatal(err)
	}
	command.Env = append(os.Environ(), "OCTO_FIXTURE_RESULT="+resultPath)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
	}()
	waitForTestFile(t, readyPath, 10*time.Second)
	err = command.Wait()
	if wantSuccess && err != nil {
		t.Fatalf("helper failed: %v", err)
	}
	if !wantSuccess && err == nil {
		t.Fatal("helper unexpectedly succeeded")
	}
	if err := parent.Wait(); err != nil && !strings.Contains(err.Error(), "exit status") {
		t.Logf("parent process wait: %v", err)
	}
	if wantSuccess {
		result := waitForRestartResult(t, resultPath)
		if result.Marker != "new" || result.Cwd != workdir || len(result.Args) != len(arguments.Arguments) {
			t.Fatalf("restart result=%+v, want marker/cwd/args", result)
		}
		for i := range arguments.Arguments {
			if result.Args[i] != arguments.Arguments[i] {
				t.Fatalf("argument %d=%q, want %q", i, result.Args[i], arguments.Arguments[i])
			}
		}
		if _, err := os.Stat(prepared); !os.IsNotExist(err) {
			t.Fatalf("prepared directory still exists after success: %v", err)
		}
		state, err := os.ReadFile(filepath.Join(root, ".octopus-roam-update.json"))
		var stateValue struct {
			Failed bool `json:"failed"`
		}
		if err != nil || json.Unmarshal(bytes.TrimPrefix(state, []byte{0xef, 0xbb, 0xbf}), &stateValue) != nil || stateValue.Failed {
			t.Fatalf("success state=%q err=%v", state, err)
		}
	} else {
		if updated, err := os.ReadFile(filepath.Join(root, "octopus-roam.exe")); err != nil || !bytes.Equal(updated, mustReadFile(t, oldExecutable)) {
			t.Fatalf("old executable was not restored: err=%v", err)
		}
		result := waitForRestartResult(t, resultPath)
		if result.Marker != "old" || result.Cwd != workdir || len(result.Args) != len(arguments.Arguments) {
			t.Fatalf("recovery result=%+v", result)
		}
		for i := range arguments.Arguments {
			if result.Args[i] != arguments.Arguments[i] {
				t.Fatalf("recovery argument %d=%q, want %q", i, result.Args[i], arguments.Arguments[i])
			}
		}
		state, err := os.ReadFile(filepath.Join(root, ".octopus-roam-update.json"))
		var stateValue struct {
			Failed bool `json:"failed"`
		}
		if err != nil || json.Unmarshal(bytes.TrimPrefix(state, []byte{0xef, 0xbb, 0xbf}), &stateValue) != nil || !stateValue.Failed {
			t.Fatalf("failure state=%q err=%v", state, err)
		}
	}
	wantSupport := "old-octopus-roam.cmd"
	if wantSuccess {
		wantSupport = "new-octopus-roam.cmd"
	}
	if result, err := os.ReadFile(filepath.Join(root, "octopus-roam.cmd")); err != nil || string(result) != wantSupport {
		t.Fatalf("support file rollback/backup mismatch: %q err=%v", result, err)
	}
}

type updateFixtureResult struct {
	Marker string   `json:"marker"`
	Cwd    string   `json:"cwd"`
	Args   []string `json:"args"`
	PID    int      `json:"pid"`
}

func waitForRestartResult(t *testing.T, path string) updateFixtureResult {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			var result updateFixtureResult
			if json.Unmarshal(data, &result) == nil {
				if process, err := os.FindProcess(result.PID); err == nil {
					_ = process.Kill()
				}
				return result
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("restart result did not appear: %s", path)
	return updateFixtureResult{}
}

func waitForTestFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("file did not appear: %s", path)
}

func copyFileForTest(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o700)
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestReleaseCheckAndVerifiedLocalUpdate(t *testing.T) {
	originalVersion := conf.Version
	originalAPI, originalDownload := releaseAPIEndpoint, releaseDownloadEndpoint
	originalClient, originalOS, originalArch, originalContainer, originalPending := testHTTPClient, testRuntimeGOOS, testRuntimeGOARCH, runningInContainer, updatePending
	originalExecutable, originalRestart := executablePath, restartAfterUpdate
	t.Cleanup(func() {
		conf.Version = originalVersion
		releaseAPIEndpoint, releaseDownloadEndpoint = originalAPI, originalDownload
		testHTTPClient, testRuntimeGOOS, testRuntimeGOARCH = originalClient, originalOS, originalArch
		runningInContainer = originalContainer
		updatePending = originalPending
		executablePath, restartAfterUpdate = originalExecutable, originalRestart
	})

	conf.Version = "v0.1.0"
	runningInContainer = func() bool { return false }
	testRuntimeGOOS, testRuntimeGOARCH = "linux", "amd64"
	testArchive := makeReleaseArchive(t, "octopus-roam", []byte("new executable"))
	digest := sha256.Sum256(testArchive)
	checksums := hex.EncodeToString(digest[:]) + "  octopus-roam-linux-amd64.zip\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/releases":
			_ = json.NewEncoder(w).Encode([]releaseRecord{{TagName: "v0.1.1", Body: "local fixture"}})
		case r.URL.Path == "/download/v0.1.1/octopus-roam-linux-amd64.zip":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(testArchive)
		case r.URL.Path == "/download/v0.1.1/SHA256SUMS":
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, checksums)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	releaseAPIEndpoint, releaseDownloadEndpoint = server.URL+"/releases", server.URL+"/download"
	testHTTPClient = server.Client()

	info, err := GetLatestInfo()
	if err != nil || info.Status != StatusAvailable || !info.CanUpdate || info.TagName != "v0.1.1" {
		t.Fatalf("info=%+v err=%v", info, err)
	}
	runtimeDir := t.TempDir()
	executable := filepath.Join(runtimeDir, "octopus-roam")
	if err := os.WriteFile(executable, []byte("old executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "data-sentinel"), []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	executablePath = func() (string, error) { return executable, nil }
	restarted := make(chan struct{})
	restartAfterUpdate = func(string, string) { close(restarted) }
	if _, err := UpdateCore("v0.1.1"); err != nil {
		t.Fatalf("UpdateCore() error = %v", err)
	}
	if _, err := UpdateCore("v0.1.1"); !errors.Is(err, ErrUpdateBusy) {
		t.Fatalf("a second queued update was accepted: %v", err)
	}
	select {
	case <-restarted:
	case <-time.After(time.Second):
		t.Fatal("restart was not scheduled")
	}
	updated, err := os.ReadFile(executable)
	if err != nil || string(updated) != "new executable" {
		t.Fatalf("updated executable=%q err=%v", updated, err)
	}
	if preserved, err := os.ReadFile(filepath.Join(runtimeDir, "data-sentinel")); err != nil || string(preserved) != "preserve" {
		t.Fatalf("data sentinel=%q err=%v", preserved, err)
	}
	backups, err := filepath.Glob(filepath.Join(runtimeDir, "backups", "before-update-*", "octopus-roam"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("backup files=%v err=%v", backups, err)
	}
}

func TestReleaseVerificationFailureLeavesExecutableUntouched(t *testing.T) {
	originalVersion := conf.Version
	originalAPI, originalDownload := releaseAPIEndpoint, releaseDownloadEndpoint
	originalClient, originalOS, originalArch, originalContainer, originalPending := testHTTPClient, testRuntimeGOOS, testRuntimeGOARCH, runningInContainer, updatePending
	originalExecutable, originalRestart := executablePath, restartAfterUpdate
	t.Cleanup(func() {
		conf.Version = originalVersion
		releaseAPIEndpoint, releaseDownloadEndpoint = originalAPI, originalDownload
		testHTTPClient, testRuntimeGOOS, testRuntimeGOARCH = originalClient, originalOS, originalArch
		runningInContainer = originalContainer
		updatePending = originalPending
		executablePath, restartAfterUpdate = originalExecutable, originalRestart
	})
	conf.Version = "v0.1.0"
	runningInContainer = func() bool { return false }
	testRuntimeGOOS, testRuntimeGOARCH = "linux", "amd64"
	testArchive := makeReleaseArchive(t, "octopus-roam", []byte("new executable"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/releases" {
			_ = json.NewEncoder(w).Encode([]releaseRecord{{TagName: "v0.1.1"}})
			return
		}
		if strings.HasSuffix(r.URL.Path, "SHA256SUMS") {
			_, _ = io.WriteString(w, strings.Repeat("0", 64)+"  octopus-roam-linux-amd64.zip\n")
			return
		}
		_, _ = w.Write(testArchive)
	}))
	defer server.Close()
	releaseAPIEndpoint, releaseDownloadEndpoint = server.URL+"/releases", server.URL+"/download"
	testHTTPClient = server.Client()
	runtimeDir := t.TempDir()
	executable := filepath.Join(runtimeDir, "octopus-roam")
	if err := os.WriteFile(executable, []byte("old executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	executablePath = func() (string, error) { return executable, nil }
	restartAfterUpdate = func(string, string) {}
	if _, err := UpdateCore("v0.1.1"); !errors.Is(err, ErrReleaseVerification) {
		t.Fatalf("UpdateCore() error = %v, want verification error", err)
	}
	updated, readErr := os.ReadFile(executable)
	if readErr != nil || string(updated) != "old executable" {
		t.Fatalf("executable changed after verification failure: %q err=%v", updated, readErr)
	}
}

func TestSecureUnzipRejectsTraversalAndSymlink(t *testing.T) {
	data := makeZip(t, map[string][]byte{"../outside": []byte("bad")})
	if err := secureUnzip(data, t.TempDir()); err == nil {
		t.Fatal("secureUnzip accepted a traversal entry")
	}
}

func TestReleaseAssetRequestDoesNotCarryAPIKey(t *testing.T) {
	originalClient, originalToken := testHTTPClient, github_pat
	originalAPI, originalDownload, originalContainer := releaseAPIEndpoint, releaseDownloadEndpoint, runningInContainer
	t.Cleanup(func() {
		testHTTPClient, github_pat = originalClient, originalToken
		releaseAPIEndpoint, releaseDownloadEndpoint = originalAPI, originalDownload
		runningInContainer = originalContainer
	})
	var gotAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, "asset")
	}))
	defer server.Close()
	testHTTPClient = server.Client()
	releaseAPIEndpoint, releaseDownloadEndpoint = server.URL+"/releases", server.URL+"/download"
	github_pat = "synthetic-token"
	if _, err := requestRelease(server.URL+"/asset", false, 64); err != nil {
		t.Fatal(err)
	}
	if gotAuthorization != "" {
		t.Fatalf("asset request carried Authorization header: %q", gotAuthorization)
	}
}

func TestReleaseCheckFailureIsAStableStatus(t *testing.T) {
	originalVersion := conf.Version
	originalAPI, originalDownload, originalContainer := releaseAPIEndpoint, releaseDownloadEndpoint, runningInContainer
	originalClient := testHTTPClient
	t.Cleanup(func() {
		conf.Version = originalVersion
		releaseAPIEndpoint, releaseDownloadEndpoint = originalAPI, originalDownload
		testHTTPClient = originalClient
		runningInContainer = originalContainer
	})
	conf.Version = "v0.1.0"
	runningInContainer = func() bool { return false }
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	releaseAPIEndpoint, releaseDownloadEndpoint = server.URL+"/releases", server.URL+"/download"
	testHTTPClient = server.Client()
	info, err := GetLatestInfo()
	if err != nil || info.Status != StatusRequestFailed || info.CanUpdate {
		t.Fatalf("info=%+v err=%v", info, err)
	}
	if strings.Contains(info.Message, server.URL) {
		t.Fatalf("status leaked the test source URL: %q", info.Message)
	}
}

func makeReleaseArchive(t *testing.T, executableName string, executable []byte) []byte {
	t.Helper()
	return makeZip(t, map[string][]byte{executableName: executable, "README.md": []byte("fixture")})
}

func makeZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
