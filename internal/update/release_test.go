package update

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestWindowsPackageLayouts(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		extra []string
		menu  string
		valid bool
	}{
		{"native", nil, "file", true},
		{"legacy", legacyWindowsFiles, "file", true},
		{"partial legacy", legacyWindowsFiles[:1], "file", false},
		{"missing menu", nil, "", false},
		{"menu directory", nil, "directory", false},
		{"unexpected script", []string{"windows/extra.ps1"}, "file", false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			stage := t.TempDir()
			files := append([]string{"octopus-roam.exe"}, testCase.extra...)
			if testCase.menu == "file" {
				files = append(files, "octopus-roam.cmd")
			} else if testCase.menu == "directory" {
				if err := os.Mkdir(filepath.Join(stage, "octopus-roam.cmd"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range files {
				path := filepath.Join(stage, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := validateExtractedPackage(stage, "windows"); (err == nil) != testCase.valid {
				t.Fatalf("valid=%v err=%v", testCase.valid, err)
			}
		})
	}
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
