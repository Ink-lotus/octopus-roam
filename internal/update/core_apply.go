package update

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/log"
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
	backupDir := filepath.Join(runtimeDir, "backups", "before-update-"+time.Now().UTC().Format("20060102-150405.000"))
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
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

const windowsUpdateHelper = `param(
    [int]$ParentPid,
    [string]$RuntimeDirectory,
    [string]$PreparedDirectory,
    [string]$ExecutableName,
    [string]$ArgumentsFile,
    [string]$ReadyFile
)
$ErrorActionPreference = 'Stop'
$hasher = [Security.Cryptography.SHA256]::Create()
try {
    $lockId = [BitConverter]::ToString($hasher.ComputeHash([Text.Encoding]::UTF8.GetBytes($RuntimeDirectory.ToUpperInvariant()))).Replace('-', '')
} finally { $hasher.Dispose() }
$mutex = New-Object Threading.Mutex($false, "Local\OctopusRoam-$lockId")
$ownsMutex = $false
$target = Join-Path $RuntimeDirectory $ExecutableName
$backup = $null
$installed = @()
$parentExited = $false
$stateFile = Join-Path $RuntimeDirectory '.octopus-roam-update.json'
function Write-JsonUtf8([string]$Path, $Value) {
    $json = $Value | ConvertTo-Json -Compress
    $encoding = New-Object System.Text.UTF8Encoding($false)
    [IO.File]::WriteAllText($Path, $json, $encoding)
}
function ConvertTo-ProcessArgument([string]$Value) {
    # Windows CreateProcess joins arguments; preserve embedded quotes and trailing backslashes.
    $escaped = [regex]::Replace($Value, '(\\*)"', '$1$1\"')
    $escaped = [regex]::Replace($escaped, '(\\+)$', '$1$1')
    return '"' + $escaped + '"'
}
try {
    try { $ownsMutex = $mutex.WaitOne(5000) } catch [Threading.AbandonedMutexException] { $ownsMutex = $true }
    if (-not $ownsMutex) { throw 'update_busy' }
    $restart = [IO.File]::ReadAllText($ArgumentsFile, [Text.Encoding]::UTF8) | ConvertFrom-Json
    $quotedArguments = @($restart.arguments | ForEach-Object { ConvertTo-ProcessArgument ([string]$_) }) -join ' '
    Write-JsonUtf8 $ReadyFile @{ ready = $true }
    $deadline = [DateTime]::UtcNow.AddSeconds(60)
    while ($null -ne (Get-Process -Id $ParentPid -ErrorAction SilentlyContinue)) {
        if ([DateTime]::UtcNow -gt $deadline) { throw 'shutdown_timeout' }
        Start-Sleep -Milliseconds 100
    }
    $parentExited = $true
    $others = @(Get-Process -Name octopus-roam -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $target })
    if ($others.Count) { throw 'update_busy' }
    $backup = Join-Path $RuntimeDirectory ('backups\before-update-' + (Get-Date -Format 'yyyyMMdd-HHmmss.fff'))
    New-Item -ItemType Directory -Force -Path $backup | Out-Null
    $relativeFiles = @($ExecutableName, 'octopus-roam.cmd', 'windows\octopus-roam-menu.ps1', 'windows\octopus-roam-start.ps1', 'windows\octopus-roam-autostart.ps1', 'windows\send-stop-signal.ps1')
    foreach ($relative in $relativeFiles) {
        $source = Join-Path $PreparedDirectory $relative
        if (-not (Test-Path -LiteralPath $source -PathType Leaf)) { throw 'incomplete_package' }
        $destination = Join-Path $RuntimeDirectory $relative
        if (Test-Path -LiteralPath $destination -PathType Leaf) {
            $backupDestination = Join-Path $backup $relative
            New-Item -ItemType Directory -Force -Path (Split-Path -Parent $backupDestination) | Out-Null
            Copy-Item -LiteralPath $destination -Destination $backupDestination -Force
        }
    }
    foreach ($relative in $relativeFiles) {
        $source = Join-Path $PreparedDirectory $relative
        $destination = Join-Path $RuntimeDirectory $relative
        New-Item -ItemType Directory -Force -Path (Split-Path -Parent $destination) | Out-Null
        $installed += $relative
        Copy-Item -LiteralPath $source -Destination $destination -Force
    }
    $logs = Join-Path $RuntimeDirectory 'logs'
    New-Item -ItemType Directory -Force -Path $logs | Out-Null
    $newProcess = Start-Process -FilePath $target -ArgumentList $quotedArguments -WorkingDirectory $restart.directory -WindowStyle Hidden -PassThru
    if ($newProcess.WaitForExit(2000)) { throw 'restart_failed' }
    try { Write-JsonUtf8 $stateFile @{ failed = $false } } catch { }
} catch {
    # Keep the verified stage and backup for diagnosis; rollback only files this attempt installed.
    foreach ($relative in $installed) {
        $destination = Join-Path $RuntimeDirectory $relative
        $backupDestination = Join-Path $backup $relative
        if (Test-Path -LiteralPath $backupDestination -PathType Leaf) {
            Copy-Item -LiteralPath $backupDestination -Destination $destination -Force
        } elseif (Test-Path -LiteralPath $destination -PathType Leaf) {
            Remove-Item -LiteralPath $destination -Force
        }
    }
    try { Write-JsonUtf8 $stateFile @{ failed = $true } } catch { }
    if (-not (Test-Path -LiteralPath $ReadyFile)) {
        Write-JsonUtf8 $ReadyFile @{ ready = $false }
    }
    if ($parentExited) {
        try {
            $recovered = Start-Process -FilePath $target -ArgumentList $quotedArguments -WorkingDirectory $restart.directory -WindowStyle Hidden -PassThru
            if ($recovered.WaitForExit(2000)) { throw 'recovery_failed' }
        } catch { }
    }
    exit 1
} finally {
    if ($ownsMutex) { $mutex.ReleaseMutex() }
    $mutex.Dispose()
}
Remove-Item -LiteralPath $PreparedDirectory -Recurse -Force
`

func scheduleWindowsUpdate(execPath, newExecutable, stage string) (*exec.Cmd, error) {
	runtimeDir := filepath.Dir(execPath)
	if _, err := os.Stat(execPath); err != nil {
		return nil, fmt.Errorf("cannot access the installed executable")
	}
	if _, err := os.Stat(newExecutable); err != nil {
		return nil, ErrReleaseVerification
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("cannot prepare the update restart")
	}
	argsData, err := json.Marshal(struct {
		Arguments []string `json:"arguments"`
		Directory string   `json:"directory"`
	}{os.Args[1:], cwd})
	if err != nil {
		return nil, fmt.Errorf("cannot prepare the update restart")
	}
	argsFile := filepath.Join(stage, ".update-arguments.json")
	if err := os.WriteFile(argsFile, argsData, 0o600); err != nil {
		return nil, fmt.Errorf("cannot prepare the update restart")
	}
	helper := filepath.Join(stage, ".update-helper.ps1")
	if err := os.WriteFile(helper, []byte(windowsUpdateHelper), 0o600); err != nil {
		return nil, fmt.Errorf("cannot prepare the update restart")
	}
	ready := filepath.Join(stage, ".update-ready.json")
	command := exec.Command("powershell.exe", "-NoProfile", "-WindowStyle", "Hidden", "-ExecutionPolicy", "RemoteSigned", "-File", helper,
		"-ParentPid", strconv.Itoa(currentProcessID()), "-RuntimeDirectory", runtimeDir, "-PreparedDirectory", stage,
		"-ExecutableName", filepath.Base(execPath), "-ArgumentsFile", argsFile, "-ReadyFile", ready)
	if err := command.Start(); err != nil {
		log.Debugf("start Windows update helper failed: %v", err)
		return nil, fmt.Errorf("cannot start the Windows update helper")
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	for {
		if data, err := os.ReadFile(ready); err == nil {
			var result struct {
				Ready bool `json:"ready"`
			}
			if json.Unmarshal(trimJSONBOM(data), &result) == nil {
				if result.Ready {
					return command, nil
				}
				<-done
				return nil, ErrUpdateBusy
			}
		}
		select {
		case <-done:
			return nil, fmt.Errorf("Windows update helper could not prepare the restart")
		case <-deadline.C:
			_ = command.Process.Kill()
			<-done
			return nil, fmt.Errorf("Windows update helper did not become ready")
		case <-time.After(50 * time.Millisecond):
		}
	}
}
