package update

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/conf"
	"github.com/Ink-lotus/octopus-roam/internal/rhttp"
	"github.com/charmbracelet/log"
)

const (
	productionReleaseAPIEndpoint      = "https://api.github.com/repos/Ink-lotus/octopus-roam/releases"
	productionReleaseDownloadEndpoint = "https://github.com/Ink-lotus/octopus-roam/releases/download"
	checksumFilename                  = "SHA256SUMS"
	maxReleaseMetadataBytes           = 2 << 20
	maxChecksumBytes                  = 2 << 20
	maxArchiveBytes                   = 512 << 20
	maxArchiveEntries                 = 128
	maxArchiveExpandedBytes           = 768 << 20
)

var (
	// 这些变量只供包内测试注入本地 HTTP 夹具；生产代码始终使用固定发布源。
	releaseAPIEndpoint      = productionReleaseAPIEndpoint
	releaseDownloadEndpoint = productionReleaseDownloadEndpoint
	testHTTPClient          *http.Client
	testRuntimeGOOS         = runtime.GOOS
	testRuntimeGOARCH       = runtime.GOARCH
	executablePath          = os.Executable
	restartAfterUpdate      = restartExecutable
	execProcess             = syscall.Exec
	updateMu                sync.Mutex
	updatePending           bool
	runningInContainer      = detectContainer
)

var (
	ErrNoIndependentRelease = errors.New("no independent Octopus Roam release is available")
	ErrAlreadyLatest        = errors.New("Octopus Roam is already up to date")
	ErrDevelopmentBuild     = errors.New("development or local builds cannot be updated automatically")
	ErrUnsupportedPlatform  = errors.New("this platform does not support online replacement")
	ErrUpdateBusy           = errors.New("another update is already in progress")
	ErrInvalidUpdateTarget  = errors.New("a valid update target version is required")
	ErrUpdateTargetChanged  = errors.New("the available update target changed; refresh version information and try again")
	ErrReleaseCheck         = errors.New("unable to check the Octopus Roam release source")
	ErrReleaseDownload      = errors.New("unable to download the Octopus Roam release")
	ErrReleaseVerification  = errors.New("Octopus Roam release verification failed")
	ErrContainerUpdate      = errors.New("container deployments must be upgraded by replacing the image")
)

type UpdateStatus string

type UpdateReceipt struct {
	Status        UpdateStatus `json:"status"`
	TargetVersion string       `json:"target_version"`
}

const (
	StatusAvailable     UpdateStatus = "available"
	StatusLatest        UpdateStatus = "latest"
	StatusNoRelease     UpdateStatus = "no_release"
	StatusDevelopment   UpdateStatus = "development"
	StatusRequestFailed UpdateStatus = "request_failed"
	StatusUnsupported   UpdateStatus = "unsupported_platform"
	StatusUpdatePending UpdateStatus = "update_pending"
	StatusContainer     UpdateStatus = "container"
	StatusUpdateFailed  UpdateStatus = "update_failed"
)

type releaseRecord struct {
	TagName     string `json:"tag_name"`
	PublishedAt string `json:"published_at"`
	Body        string `json:"body"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
}

type parsedVersion struct {
	major  int
	minor  int
	patch  int
	rc     int
	stable bool
	raw    string
}

func parseVersion(raw string) (parsedVersion, bool) {
	value := strings.TrimSpace(raw)
	if !strings.HasPrefix(value, "v") {
		return parsedVersion{}, false
	}
	value = strings.TrimPrefix(value, "v")
	parts := strings.SplitN(value, "-", 2)
	if len(parts[0]) == 0 {
		return parsedVersion{}, false
	}
	numbers := strings.Split(parts[0], ".")
	if len(numbers) != 3 {
		return parsedVersion{}, false
	}
	parsed := parsedVersion{stable: len(parts) == 1, raw: raw}
	for i, part := range numbers {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return parsedVersion{}, false
		}
		if strings.IndexFunc(part, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			return parsedVersion{}, false
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return parsedVersion{}, false
		}
		switch i {
		case 0:
			parsed.major = n
		case 1:
			parsed.minor = n
		case 2:
			parsed.patch = n
		}
	}
	if !parsed.stable {
		pre := strings.Split(parts[1], ".")
		if len(pre) != 2 || pre[0] != "rc" || pre[1] == "" || (len(pre[1]) > 1 && pre[1][0] == '0') {
			return parsedVersion{}, false
		}
		rc, err := strconv.Atoi(pre[1])
		if err != nil || rc <= 0 || strings.IndexFunc(pre[1], func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			return parsedVersion{}, false
		}
		parsed.rc = rc
	}
	return parsed, true
}

func compareVersions(left, right parsedVersion) int {
	for _, pair := range [][2]int{{left.major, right.major}, {left.minor, right.minor}, {left.patch, right.patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if left.stable != right.stable {
		if left.stable {
			return 1
		}
		return -1
	}
	if left.rc < right.rc {
		return -1
	}
	if left.rc > right.rc {
		return 1
	}
	return 0
}

func canUpgrade(current, candidate parsedVersion) bool {
	if compareVersions(candidate, current) <= 0 {
		return false
	}
	if current.stable {
		return candidate.stable
	}
	return true
}

func releaseEndpoints() (string, string) {
	return releaseAPIEndpoint, releaseDownloadEndpoint
}

func addQuery(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	query := u.Query()
	query.Set("per_page", "30")
	u.RawQuery = query.Encode()
	return u.String()
}

func releaseAssetURL(tag, filename string) (string, error) {
	_, downloadEndpoint := releaseEndpoints()
	u, err := url.Parse(downloadEndpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", ErrReleaseDownload
	}
	if _, ok := parseVersion(tag); !ok {
		return "", ErrReleaseDownload
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + url.PathEscape(tag) + "/" + url.PathEscape(filename)
	u.RawQuery = ""
	return u.String(), nil
}

func requestClient(useProxy bool) (*http.Client, error) {
	var client *http.Client
	var err error
	if testHTTPClient != nil {
		client = testHTTPClient
	} else if useProxy {
		client, err = rhttp.Proxy()
	} else {
		client, err = rhttp.Direct()
	}
	if err != nil {
		return nil, err
	}
	clone := *client
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 || !allowedReleaseHost(req.URL) {
			return fmt.Errorf("release redirect host is not allowed")
		}
		if req.URL.Host != "api.github.com" {
			req.Header.Del("Authorization")
		}
		return nil
	}
	return &clone, nil
}

func allowedReleaseHost(target *url.URL) bool {
	if target == nil || target.User != nil {
		return false
	}
	if testHTTPClient != nil {
		apiEndpoint, downloadEndpoint := releaseEndpoints()
		for _, endpoint := range []string{apiEndpoint, downloadEndpoint} {
			if u, err := url.Parse(endpoint); err == nil && u.Scheme == target.Scheme && strings.EqualFold(u.Host, target.Host) {
				return true
			}
		}
	}
	if target.Scheme != "https" {
		return false
	}
	switch strings.ToLower(target.Host) {
	case "api.github.com", "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		return true
	default:
		return false
	}
}

func requestRelease(urlString string, useProxy bool, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := requestClient(useProxy)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(urlString)
	if err != nil || !allowedReleaseHost(u) {
		return nil, ErrReleaseDownload
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlString, nil)
	if err != nil {
		return nil, err
	}
	if github_pat != "" && strings.EqualFold(u.Host, "api.github.com") {
		req.Header.Set("Authorization", "Bearer "+github_pat)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("release source returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("release response exceeds size limit")
	}
	return data, nil
}

func requestReleaseWithFallback(urlString string, limit int64) ([]byte, error) {
	data, err := requestRelease(urlString, false, limit)
	if err == nil {
		return data, nil
	}
	if testHTTPClient != nil {
		return nil, err
	}
	log.Warnf("direct release request failed; trying configured proxy")
	return requestRelease(urlString, true, limit)
}

func decodeReleases(data []byte) ([]releaseRecord, error) {
	var releases []releaseRecord
	if err := json.Unmarshal(data, &releases); err == nil {
		return releases, nil
	}
	var release releaseRecord
	if err := json.Unmarshal(data, &release); err != nil || release.TagName == "" {
		return nil, fmt.Errorf("release metadata has no usable tag")
	}
	return []releaseRecord{release}, nil
}

func selectRelease(current string, releases []releaseRecord) (*releaseRecord, UpdateStatus) {
	var newest *releaseRecord
	var newestVersion parsedVersion
	for i := range releases {
		release := releases[i]
		if release.Draft {
			continue
		}
		version, ok := parseVersion(release.TagName)
		if !ok || (release.Prerelease && version.stable) {
			continue
		}
		if newest == nil || compareVersions(version, newestVersion) > 0 {
			copy := release
			newest = &copy
			newestVersion = version
		}
	}
	if newest == nil {
		return nil, StatusNoRelease
	}
	currentVersion, comparable := parseVersion(current)
	if !comparable {
		return newest, StatusDevelopment
	}
	var candidate *releaseRecord
	var candidateVersion parsedVersion
	for i := range releases {
		release := releases[i]
		if release.Draft {
			continue
		}
		version, ok := parseVersion(release.TagName)
		if !ok || (release.Prerelease && version.stable) || !canUpgrade(currentVersion, version) {
			continue
		}
		if candidate == nil || compareVersions(version, candidateVersion) > 0 {
			copy := release
			candidate = &copy
			candidateVersion = version
		}
	}
	if candidate == nil {
		if currentVersion.stable {
			// 稳定版本不能把更高 RC 当作“最新版本”展示；以当前稳定版作为状态锚点。
			return &releaseRecord{TagName: current}, StatusLatest
		}
		return newest, StatusLatest
	}
	return candidate, StatusAvailable
}

func getLatestInfo() (*LatestInfo, error) {
	current := conf.Version
	if runningInContainer() {
		return &LatestInfo{CurrentVersion: current, Status: StatusContainer}, nil
	}
	if _, comparable := parseVersion(current); !comparable {
		return &LatestInfo{CurrentVersion: current, Status: StatusDevelopment}, nil
	}
	if updateFailedState() {
		return &LatestInfo{CurrentVersion: current, Status: StatusUpdateFailed, Message: "上一次独立版本更新失败，请检查安装目录后手动升级"}, nil
	}
	apiEndpoint, _ := releaseEndpoints()
	data, err := requestReleaseWithFallback(addQuery(apiEndpoint), maxReleaseMetadataBytes)
	if err != nil {
		return &LatestInfo{CurrentVersion: current, Status: StatusRequestFailed, Message: "无法获取 Octopus Roam 独立版本信息"}, nil
	}
	releases, err := decodeReleases(data)
	if err != nil {
		return &LatestInfo{CurrentVersion: current, Status: StatusRequestFailed, Message: "独立版本信息格式无效"}, nil
	}
	selected, status := selectRelease(current, releases)
	info := &LatestInfo{CurrentVersion: current, Status: status}
	if selected == nil {
		info.Message = "尚无可用的独立版本"
		return info, nil
	}
	info.TagName = selected.TagName
	info.PublishedAt = selected.PublishedAt
	info.Body = selected.Body
	switch status {
	case StatusAvailable:
		if _, err := getDownloadFilenameFor(testRuntimeGOOS, testRuntimeGOARCH); err != nil {
			info.Status = StatusUnsupported
			info.Message = "当前平台暂不支持在线替换，请按发布页手动升级"
			return info, nil
		}
		info.CanUpdate = true
		info.Message = "发现可用的独立版本"
	case StatusLatest:
		info.Message = "当前已经是最新版本"
	case StatusDevelopment:
		info.Message = "当前为开发或本地构建版本，不执行自动更新"
	}
	return info, nil
}

// updateFailedState consumes a successful helper marker and preserves a failed
// marker so the restarted process can surface recovery status in the UI.
func updateFailedState() bool {
	executable, err := executablePath()
	if err != nil {
		return false
	}
	statePath := filepath.Join(filepath.Dir(executable), ".octopus-roam-update.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		return false
	}
	data = trimJSONBOM(data)
	var state struct {
		Failed bool `json:"failed"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return false
	}
	if state.Failed {
		return true
	}
	_ = os.Remove(statePath)
	return false
}

func getDownloadFilenameFor(goos, arch string) (string, error) {
	switch goos {
	case "windows":
		if arch == "amd64" {
			return "octopus-roam-windows-amd64.zip", nil
		}
	case "darwin":
		if arch == "amd64" || arch == "arm64" {
			return "octopus-roam-darwin-" + arch + ".zip", nil
		}
	case "linux", "android":
		switch arch {
		case "386", "amd64", "arm", "arm64":
			return "octopus-roam-" + goos + "-" + arch + ".zip", nil
		}
	}
	return "", fmt.Errorf("unsupported platform: %s/%s", goos, arch)
}

func getDownloadFilename() (string, error) {
	return getDownloadFilenameFor(testRuntimeGOOS, testRuntimeGOARCH)
}

func parseChecksum(data []byte, filename string) (string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	var checksum string
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || len(fields[0]) != sha256.Size*2 {
			continue
		}
		name := strings.TrimPrefix(strings.TrimPrefix(fields[1], "*"), "./")
		if name != filename {
			continue
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			return "", ErrReleaseVerification
		}
		if checksum != "" {
			return "", ErrReleaseVerification
		}
		checksum = strings.ToLower(fields[0])
	}
	if checksum != "" {
		return checksum, nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("checksum for %s is missing", filename)
}

func secureUnzip(data []byte, dest string) error {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	if len(r.File) > maxArchiveEntries {
		return fmt.Errorf("archive contains too many entries")
	}
	var expanded uint64
	seen := make(map[string]struct{}, len(r.File))
	for _, f := range r.File {
		if f.UncompressedSize64 > uint64(maxArchiveExpandedBytes) || f.UncompressedSize64 > uint64(maxArchiveExpandedBytes)-expanded {
			return fmt.Errorf("archive expands beyond size limit")
		}
		expanded += f.UncompressedSize64
		name := strings.TrimSuffix(f.Name, "/")
		if name == "" || strings.ContainsAny(name, "\\:") || path.IsAbs(name) || path.Clean(name) != name {
			return fmt.Errorf("invalid file path in release archive")
		}
		relative := filepath.Clean(filepath.FromSlash(name))
		key := strings.ToLower(relative)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("release archive contains duplicate paths")
		}
		seen[key] = struct{}{}
		fpath := filepath.Join(dest, relative)
		if !isPathInDest(fpath, dest) {
			return fmt.Errorf("invalid file path in release archive")
		}
		info := f.FileInfo()
		if info.IsDir() {
			if err := os.MkdirAll(fpath, os.ModePerm); err != nil {
				return err
			}
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("release archive contains a symlink")
		}
		if err := extractSecureFile(f, fpath); err != nil {
			return err
		}
	}
	return nil
}

func extractSecureFile(f *zip.File, fpath string) error {
	if err := os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
		return err
	}
	outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode().Perm())
	if err != nil {
		return err
	}
	defer outFile.Close()
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	_, err = io.Copy(outFile, io.LimitReader(rc, maxArchiveExpandedBytes+1))
	return err
}

func archiveExecutableName(goos string) string {
	if goos == "windows" {
		return "octopus-roam.exe"
	}
	return "octopus-roam"
}

func allowedPackagePaths(goos string) map[string]struct{} {
	paths := map[string]struct{}{
		"README.md": {}, "README_zh.md": {}, "LICENSE": {}, checksumFilename: {}, "THIRD_PARTY_LICENSES.csv": {},
	}
	if goos == "windows" {
		paths["octopus-roam.cmd"] = struct{}{}
		for _, name := range []string{"octopus-roam-menu.ps1", "octopus-roam-start.ps1", "octopus-roam-autostart.ps1", "send-stop-signal.ps1"} {
			paths[filepath.Join("windows", name)] = struct{}{}
		}
	}
	return paths
}

func validateExtractedPackage(stage, goos string) (string, error) {
	executable := filepath.Join(stage, archiveExecutableName(goos))
	info, err := os.Stat(executable)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return "", fmt.Errorf("release archive does not contain its executable")
	}
	allowed := allowedPackagePaths(goos)
	entries := 0
	err = filepath.WalkDir(stage, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == stage || entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("release package contains a symlink")
		}
		rel, err := filepath.Rel(stage, path)
		if err != nil {
			return err
		}
		rel = filepath.Clean(rel)
		if rel != archiveExecutableName(goos) {
			if _, ok := allowed[rel]; !ok {
				return fmt.Errorf("release package contains an unexpected file")
			}
		}
		entries++
		return nil
	})
	if err != nil {
		return "", err
	}
	if entries == 0 {
		return "", fmt.Errorf("release package is empty")
	}
	if goos == "windows" {
		for required := range allowedPackagePaths(goos) {
			if required != "octopus-roam.cmd" && !strings.HasPrefix(required, "windows"+string(filepath.Separator)) {
				continue
			}
			if entry, err := os.Stat(filepath.Join(stage, required)); err != nil || !entry.Mode().IsRegular() {
				return "", fmt.Errorf("release archive has an incomplete Windows launcher package")
			}
		}
	}
	return executable, nil
}

func detectContainer() bool {
	if os.Getenv("container") != "" {
		return true
	}
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(marker); err == nil {
			return true
		}
	}
	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		for _, marker := range []string{"docker", "kubepods", "containerd", "libpod"} {
			if bytes.Contains(data, []byte(marker)) {
				return true
			}
		}
	}
	return false
}
