package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/conf"
)

type targetUpdateFixture struct {
	t                 *testing.T
	runtimeDir        string
	executable        string
	stageRoot         string
	before            map[string][32]byte
	mu                sync.Mutex
	paths             []string
	tag               string
	publishOnDownload string
	metadataStatus    int
	assetStatus       int
	badChecksum       bool
	executableReads   atomic.Int32
	stages            atomic.Int32
	shutdowns         atomic.Int32
	exits             atomic.Int32
	restarts          atomic.Int32
	restarted         chan string
}

func newTargetUpdateFixture(t *testing.T, tag string) *targetUpdateFixture {
	t.Helper()
	f := &targetUpdateFixture{t: t, runtimeDir: t.TempDir(), stageRoot: t.TempDir(), tag: tag, restarted: make(chan string, 2)}
	f.executable = filepath.Join(f.runtimeDir, "octopus-roam")
	if err := os.WriteFile(f.executable, []byte("previous synthetic executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.runtimeDir, "data-sentinel"), []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.before = hashTargetFixture(t, f.runtimeDir)
	for _, name := range []string{"TMP", "TEMP", "TMPDIR"} {
		t.Setenv(name, f.stageRoot)
	}
	originalVersion, originalContainer := conf.Version, runningInContainer
	originalAPI, originalDownload, originalClient := releaseAPIEndpoint, releaseDownloadEndpoint, testHTTPClient
	originalOS, originalArch, originalPending := testRuntimeGOOS, testRuntimeGOARCH, updatePending
	originalExecutable, originalRestart, originalShutdown := executablePath, restartAfterUpdate, shutdownUpdate
	originalExit, originalStage := exitProcess, makeUpdateStage
	t.Cleanup(func() {
		// Keep process stubs installed until any accepted asynchronous restart runs,
		// including when a receipt assertion fails before the test waits for it.
		if updatePending && f.stages.Load() > 0 && f.restarts.Load() == 0 {
			select {
			case <-f.restarted:
			case <-time.After(3 * time.Second):
				t.Error("accepted update did not finish its scheduled test action")
			}
		}
		conf.Version, runningInContainer = originalVersion, originalContainer
		releaseAPIEndpoint, releaseDownloadEndpoint, testHTTPClient = originalAPI, originalDownload, originalClient
		testRuntimeGOOS, testRuntimeGOARCH, updatePending = originalOS, originalArch, originalPending
		executablePath, restartAfterUpdate, shutdownUpdate = originalExecutable, originalRestart, originalShutdown
		exitProcess, makeUpdateStage = originalExit, originalStage
	})
	conf.Version, updatePending = "v0.1.0-rc.1", false
	runningInContainer = func() bool { return false }
	testRuntimeGOOS, testRuntimeGOARCH = "linux", "amd64"
	executablePath = func() (string, error) { f.executableReads.Add(1); return f.executable, nil }
	makeUpdateStage = func(dir, pattern string) (string, error) { f.stages.Add(1); return os.MkdirTemp(dir, pattern) }
	shutdownUpdate = func() { f.shutdowns.Add(1) }
	exitProcess = func(int) { f.exits.Add(1) }
	restartAfterUpdate = func(executable, backup string) {
		f.restarts.Add(1)
		f.restarted <- executable + "|" + backup
	}
	archive := makeReleaseArchive(t, "octopus-roam", []byte("accepted synthetic executable "+tag))
	digest := sha256.Sum256(archive)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.paths = append(f.paths, r.URL.Path)
		if r.URL.Path == "/releases" {
			if f.metadataStatus != 0 {
				w.WriteHeader(f.metadataStatus)
				return
			}
			releases := []releaseRecord{}
			if f.tag != "" {
				releases = append(releases, releaseRecord{TagName: f.tag, Prerelease: true})
			}
			_ = json.NewEncoder(w).Encode(releases)
			return
		}
		if f.assetStatus != 0 {
			w.WriteHeader(f.assetStatus)
			return
		}
		switch r.URL.Path {
		case "/download/" + tag + "/octopus-roam-linux-amd64.zip":
			if f.publishOnDownload != "" {
				f.tag = f.publishOnDownload
			}
			_, _ = w.Write(archive)
		case "/download/" + tag + "/SHA256SUMS":
			checksum := hex.EncodeToString(digest[:])
			if f.badChecksum {
				checksum = strings.Repeat("0", 64)
			}
			_, _ = io.WriteString(w, checksum+"  octopus-roam-linux-amd64.zip\n")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	releaseAPIEndpoint, releaseDownloadEndpoint, testHTTPClient = server.URL+"/releases", server.URL+"/download", server.Client()
	return f
}

func hashTargetFixture(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	files := make(map[string][32]byte)
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[relative] = sha256.Sum256(mustReadFile(t, path))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func (f *targetUpdateFixture) requestedPaths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...)
}

func (f *targetUpdateFixture) assertNoInstall(wantStages int32) {
	f.t.Helper()
	if f.stages.Load() != wantStages || f.restarts.Load() != 0 || f.shutdowns.Load() != 0 || f.exits.Load() != 0 {
		f.t.Fatal("rejected update created an unexpected stage or scheduled process actions")
	}
	if updatePending || !reflect.DeepEqual(f.before, hashTargetFixture(f.t, f.runtimeDir)) {
		f.t.Fatal("rejected target changed pending state, executable, backup or data")
	}
	if files := hashTargetFixture(f.t, f.stageRoot); len(files) != 0 {
		f.t.Fatal("rejected target left staging files")
	}
}

func TestUpdateTargetMismatchHasNoInstallEffects(t *testing.T) {
	for _, platform := range []string{"linux", "windows"} {
		t.Run(platform, func(t *testing.T) {
			f := newTargetUpdateFixture(t, "v0.1.0-rc.3")
			testRuntimeGOOS = platform
			receipt, err := UpdateCore("v0.1.0-rc.2")
			if receipt != nil || !errors.Is(err, ErrUpdateTargetChanged) {
				t.Fatalf("stale target: receipt=%v err=%v", receipt, err)
			}
			if paths := f.requestedPaths(); !reflect.DeepEqual(paths, []string{"/releases"}) {
				t.Fatalf("stale target downloaded assets or reselected a release: %v", paths)
			}
			// getLatestInfo reads the failure marker once; installation must not resolve the executable again.
			if f.executableReads.Load() != 1 {
				t.Fatal("stale target reached executable installation")
			}
			f.assertNoInstall(0)
		})
	}
}

func TestUpdateTargetValidationPrecedesReleaseAccess(t *testing.T) {
	f := newTargetUpdateFixture(t, "v0.1.0-rc.2")
	for _, target := range []string{"", " ", " v0.1.0-rc.2", "v0.1.0-rc.2\n", "v0.1.0-beta.2", "v0.1.0-local.2", "v0.1.0-rc.0", "v00.1.0", "v1.2", "latest"} {
		receipt, err := UpdateCore(target)
		if receipt != nil || !errors.Is(err, ErrInvalidUpdateTarget) {
			t.Fatalf("invalid target was not rejected: receipt=%v err=%v", receipt, err)
		}
	}
	if len(f.requestedPaths()) != 0 || f.executableReads.Load() != 0 {
		t.Fatal("invalid target accessed release metadata or the executable")
	}
	f.assertNoInstall(0)
}

func TestUpdateTargetRemainsFixedAfterReleaseSourceChanges(t *testing.T) {
	f := newTargetUpdateFixture(t, "v0.1.0-rc.2")
	f.mu.Lock()
	f.publishOnDownload = "v0.1.0-rc.3"
	f.mu.Unlock()
	receipt, err := UpdateCore("v0.1.0-rc.2")
	if err != nil || receipt == nil || receipt.Status != StatusUpdatePending || receipt.TargetVersion != "v0.1.0-rc.2" {
		t.Fatalf("accepted target: receipt=%v err=%v", receipt, err)
	}
	if !updatePending {
		t.Fatal("receipt returned before pending was set")
	}
	select {
	case restarted := <-f.restarted:
		if !strings.HasPrefix(restarted, f.executable+"|") {
			t.Fatal("restart used a different executable")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("accepted update did not schedule a restart")
	}
	wantPaths := []string{"/releases", "/download/v0.1.0-rc.2/octopus-roam-linux-amd64.zip", "/download/v0.1.0-rc.2/SHA256SUMS"}
	if paths := f.requestedPaths(); !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("accepted target was reselected: %v", paths)
	}
	if content := string(mustReadFile(t, f.executable)); content != "accepted synthetic executable v0.1.0-rc.2" {
		t.Fatal("installed fixture does not match the accepted target")
	}
	backups, err := filepath.Glob(filepath.Join(f.runtimeDir, "backups", "before-update-*", "octopus-roam"))
	if err != nil || len(backups) != 1 || string(mustReadFile(t, backups[0])) != "previous synthetic executable" {
		t.Fatal("previous fixture was not backed up")
	}
	if f.stages.Load() != 1 || f.restarts.Load() != 1 {
		t.Fatal("accepted target was staged or scheduled more than once")
	}
	data, err := json.Marshal(receipt)
	if err != nil || string(data) != `{"status":"update_pending","target_version":"v0.1.0-rc.2"}` {
		t.Fatalf("receipt JSON does not match the HTTP contract: %s", data)
	}
}

func TestUpdateTargetPreservesExistingGates(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, *targetUpdateFixture)
		want  error
	}{
		{"locked", func(t *testing.T, _ *targetUpdateFixture) { updateMu.Lock(); t.Cleanup(updateMu.Unlock) }, ErrUpdateBusy},
		{"pending", func(*testing.T, *targetUpdateFixture) { updatePending = true }, ErrUpdateBusy},
		{"container", func(*testing.T, *targetUpdateFixture) { runningInContainer = func() bool { return true } }, ErrContainerUpdate},
		{"development", func(*testing.T, *targetUpdateFixture) { conf.Version = "v0.1.0-local.fixture" }, ErrDevelopmentBuild},
		{"no release", func(_ *testing.T, f *targetUpdateFixture) { f.tag = "" }, ErrNoIndependentRelease},
		{"already latest", func(*testing.T, *targetUpdateFixture) { conf.Version = "v0.1.0-rc.3" }, ErrAlreadyLatest},
		{"platform", func(*testing.T, *targetUpdateFixture) { testRuntimeGOOS = "unsupported" }, ErrUnsupportedPlatform},
		{"metadata failure", func(_ *testing.T, f *targetUpdateFixture) { f.metadataStatus = http.StatusServiceUnavailable }, ErrReleaseCheck},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newTargetUpdateFixture(t, "v0.1.0-rc.3")
			f.mu.Lock()
			tc.setup(t, f)
			f.mu.Unlock()
			pendingBefore := updatePending
			receipt, err := UpdateCore("v0.1.0-rc.2")
			if receipt != nil || !errors.Is(err, tc.want) {
				t.Fatalf("gate replaced with target conflict: receipt=%v err=%v want=%v", receipt, err, tc.want)
			}
			if updatePending != pendingBefore {
				t.Fatal("rejected request changed pending state")
			}
			updatePending = false
			f.assertNoInstall(0)
			for _, path := range f.requestedPaths() {
				if path != "/releases" {
					t.Fatal("blocked update downloaded an asset")
				}
			}
		})
	}
}

func TestUpdateTargetFailuresReturnNoReceipt(t *testing.T) {
	for _, failure := range []string{"download", "checksum", "replacement"} {
		t.Run(failure, func(t *testing.T) {
			f := newTargetUpdateFixture(t, "v0.1.0-rc.2")
			f.mu.Lock()
			switch failure {
			case "download":
				f.assetStatus = http.StatusNotFound
			case "checksum":
				f.badChecksum = true
			case "replacement":
				f.executable = filepath.Join(f.runtimeDir, "missing")
			}
			f.mu.Unlock()
			receipt, err := UpdateCore("v0.1.0-rc.2")
			if receipt != nil || err == nil || errors.Is(err, ErrUpdateTargetChanged) || updatePending {
				t.Fatalf("failure returned a receipt or wrong status: receipt=%v err=%v", receipt, err)
			}
			if failure == "download" && !errors.Is(err, ErrReleaseDownload) || failure == "checksum" && !errors.Is(err, ErrReleaseVerification) {
				t.Fatal("download or checksum error lost its original classification")
			}
			var wantStages int32
			if failure == "replacement" {
				wantStages = 1
			}
			f.assertNoInstall(wantStages)
		})
	}
}
