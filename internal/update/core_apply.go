package update

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var (
	exitProcess      = os.Exit
	currentProcessID = os.Getpid
	makeUpdateStage  = os.MkdirTemp
)

func trimJSONBOM(data []byte) []byte {
	return bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
}

func updateCore(targetVersion string) (*UpdateReceipt, error) {
	if _, valid := parseVersion(targetVersion); !valid || strings.TrimSpace(targetVersion) != targetVersion {
		return nil, ErrInvalidUpdateTarget
	}
	if !updateMu.TryLock() {
		return nil, ErrUpdateBusy
	}
	defer updateMu.Unlock()
	if updatePending {
		return nil, ErrUpdateBusy
	}

	info, err := getLatestInfo()
	if err != nil {
		return nil, ErrReleaseCheck
	}
	switch info.Status {
	case StatusContainer:
		return nil, ErrContainerUpdate
	case StatusDevelopment:
		return nil, ErrDevelopmentBuild
	case StatusNoRelease:
		return nil, ErrNoIndependentRelease
	case StatusLatest:
		return nil, ErrAlreadyLatest
	case StatusUnsupported:
		return nil, ErrUnsupportedPlatform
	case StatusRequestFailed:
		return nil, ErrReleaseCheck
	case StatusAvailable:
		if !info.CanUpdate || info.TagName == "" {
			return nil, ErrReleaseCheck
		}
	default:
		return nil, ErrReleaseCheck
	}
	// The selected release stays fixed for downloads, installation and receipt.
	if info.TagName != targetVersion {
		return nil, ErrUpdateTargetChanged
	}

	filename, err := getDownloadFilename()
	if err != nil {
		return nil, ErrUnsupportedPlatform
	}
	archiveURL, err := releaseAssetURL(info.TagName, filename)
	if err != nil {
		return nil, ErrReleaseDownload
	}
	checksumURL, err := releaseAssetURL(info.TagName, checksumFilename)
	if err != nil {
		return nil, ErrReleaseDownload
	}
	archiveData, err := requestReleaseWithFallback(archiveURL, maxArchiveBytes)
	if err != nil {
		return nil, ErrReleaseDownload
	}
	checksumData, err := requestReleaseWithFallback(checksumURL, maxChecksumBytes)
	if err != nil {
		return nil, ErrReleaseVerification
	}
	expectedHash, err := parseChecksum(checksumData, filename)
	if err != nil {
		return nil, ErrReleaseVerification
	}
	actualHash := sha256.Sum256(archiveData)
	if !strings.EqualFold(expectedHash, hex.EncodeToString(actualHash[:])) {
		return nil, ErrReleaseVerification
	}

	execPath, err := executablePath()
	if err != nil {
		return nil, fmt.Errorf("cannot locate the running executable")
	}
	execPath, err = filepath.Abs(execPath)
	if err != nil {
		return nil, fmt.Errorf("cannot locate the running executable")
	}
	stage, err := makeUpdateStage("", ".octopus-roam-update-*")
	if err != nil {
		return nil, fmt.Errorf("cannot prepare the update")
	}
	keepStage := false
	defer func() {
		if !keepStage {
			_ = os.RemoveAll(stage)
		}
	}()
	if err := secureUnzip(archiveData, stage); err != nil {
		return nil, ErrReleaseVerification
	}
	newExecutable, err := validateExtractedPackage(stage, testRuntimeGOOS)
	if err != nil {
		return nil, ErrReleaseVerification
	}

	if testRuntimeGOOS == "windows" {
		if filepath.Base(execPath) != archiveExecutableName(testRuntimeGOOS) {
			return nil, ErrUnsupportedPlatform
		}
		if _, err := scheduleWindowsUpdate(execPath, newExecutable, stage); err != nil {
			return nil, err
		}
		keepStage = true
		updatePending = true
		go func() {
			time.Sleep(300 * time.Millisecond)
			shutdownUpdate()
			exitProcess(0)
		}()
		return &UpdateReceipt{Status: StatusUpdatePending, TargetVersion: info.TagName}, nil
	}
	backupDir, err := replaceUnixExecutable(execPath, newExecutable)
	if err != nil {
		return nil, err
	}
	updatePending = true
	go func() {
		time.Sleep(300 * time.Millisecond)
		restartAfterUpdate(execPath, backupDir)
	}()
	return &UpdateReceipt{Status: StatusUpdatePending, TargetVersion: info.TagName}, nil
}

func replaceUnixExecutable(execPath, newExecutable string) (string, error) {
	info, err := os.Stat(execPath)
	if err != nil {
		return "", fmt.Errorf("cannot access the installed executable")
	}
	backupDir, err := makeUpdateBackup(filepath.Dir(execPath), execPath, nil)
	if err != nil {
		return "", err
	}
	temporary := execPath + ".new-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := copyFile(newExecutable, temporary); err != nil {
		_ = os.Remove(temporary)
		return "", fmt.Errorf("cannot stage the new executable")
	}
	if err := os.Chmod(temporary, info.Mode().Perm()); err != nil {
		_ = os.Remove(temporary)
		return "", fmt.Errorf("cannot preserve executable permissions")
	}
	if err := os.Rename(temporary, execPath); err != nil {
		_ = os.Remove(temporary)
		_ = os.Remove(filepath.Join(backupDir, filepath.Base(execPath)))
		return "", fmt.Errorf("cannot replace the installed executable")
	}
	return backupDir, nil
}

func makeUpdateBackup(runtimeDir, executable string, extra []string) (string, error) {
	backupRoot := filepath.Join(runtimeDir, "backups")
	if err := os.MkdirAll(backupRoot, 0o755); err != nil {
		return "", fmt.Errorf("cannot create update backup")
	}
	backupDir, err := os.MkdirTemp(backupRoot, "before-update-"+time.Now().UTC().Format("20060102-150405.000")+"-")
	if err != nil {
		return "", fmt.Errorf("cannot create update backup")
	}
	if err := copyFile(executable, filepath.Join(backupDir, filepath.Base(executable))); err != nil {
		return "", fmt.Errorf("cannot create update backup")
	}
	for _, source := range extra {
		if source == "" {
			continue
		}
		relative, err := filepath.Rel(runtimeDir, source)
		if err != nil || !isPathInDest(source, runtimeDir) {
			return "", fmt.Errorf("invalid update backup path")
		}
		destination := filepath.Join(backupDir, relative)
		if err := copyFile(source, destination); err != nil {
			return "", fmt.Errorf("cannot create update backup")
		}
	}
	return backupDir, nil
}
