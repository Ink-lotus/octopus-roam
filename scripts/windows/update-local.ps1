# Windows PowerShell 5.1 compatible. Default: build only. -Apply updates the runtime.
param(
    [string]$RuntimeDirectory = 'D:\tool\AI\octopus-roam-runtime',
    [switch]$Apply,
    [string]$PreparedDirectory
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$sourceDirectory = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$runtimePath = [IO.Path]::GetFullPath($RuntimeDirectory).TrimEnd('\')
$runtimeExecutable = Join-Path $runtimePath 'octopus-roam.exe'
$powerShellPath = Join-Path ([Environment]::GetFolderPath('System')) 'WindowsPowerShell\v1.0\powershell.exe'

function Get-RuntimeProcess {
    foreach ($candidate in @(Get-Process -Name octopus-roam -ErrorAction SilentlyContinue)) {
        try { $candidatePath = $candidate.Path } catch { continue }
        if ([string]::Equals($candidatePath, $runtimeExecutable, [StringComparison]::OrdinalIgnoreCase)) {
            $candidate
        }
    }
}

try {
    foreach ($relativePath in @('octopus-roam.exe', 'data\config.json')) {
        if (-not (Test-Path -LiteralPath (Join-Path $runtimePath $relativePath) -PathType Leaf)) {
            throw "Missing runtime file: $relativePath"
        }
    }
    if (-not (Test-Path -LiteralPath (Join-Path $runtimePath 'octopus-roam-start.ps1') -PathType Leaf) `
        -and -not (Test-Path -LiteralPath (Join-Path $runtimePath 'windows\octopus-roam-start.ps1') -PathType Leaf)) {
        throw 'Missing runtime launcher: windows\octopus-roam-start.ps1'
    }
    if ([string]::Equals($sourceDirectory, $runtimePath, [StringComparison]::OrdinalIgnoreCase)) {
        throw 'Source and runtime directories must be different.'
    }

    if (-not $PreparedDirectory) {
        $stamp = Get-Date -Format 'yyyyMMdd-HHmmss-fff'
        $PreparedDirectory = Join-Path $sourceDirectory "build\local-update-$stamp"
        $buildSource = Join-Path $PreparedDirectory 'source'
        $staticDirectory = Join-Path $buildSource 'static'
        [IO.Directory]::CreateDirectory($staticDirectory) | Out-Null
        foreach ($entry in @('cmd', 'internal', 'main.go', 'go.mod', 'go.sum')) {
            Copy-Item -LiteralPath (Join-Path $sourceDirectory $entry) -Destination $buildSource -Recurse
        }
        Copy-Item -LiteralPath (Join-Path $sourceDirectory 'static\static.go') -Destination $staticDirectory
        $version = 'v0.1.0-local.' + (Get-Date -Format 'yyyyMMddHHmmss')
        $savedFrontendVersion = $env:VITE_APP_VERSION
        $savedGoCache = $env:GOCACHE
        $savedGoModCache = $env:GOMODCACHE
        try {
            $env:VITE_APP_VERSION = $version
            $env:GOCACHE = Join-Path $sourceDirectory '.tmp-go-cache'
            $env:GOMODCACHE = Join-Path $sourceDirectory '.tmp-go-mod'
            Push-Location -LiteralPath (Join-Path $sourceDirectory 'web')
            try {
                & node 'node_modules/typescript/bin/tsc' --noEmit
                if ($LASTEXITCODE -ne 0) { throw 'TypeScript check failed.' }
                & node 'node_modules/vite/bin/vite.js' build --outDir (Join-Path $staticDirectory 'out') --emptyOutDir false
                if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed.' }
            } finally { Pop-Location }
            Push-Location -LiteralPath $buildSource
            try {
                $binaryPath = Join-Path $PreparedDirectory 'octopus-roam.exe'
                & go build -trimpath -tags=jsoniter -ldflags "-s -w -X github.com/Ink-lotus/octopus-roam/internal/conf.Version=$version" -o $binaryPath .
                if ($LASTEXITCODE -ne 0) { throw 'Backend build failed.' }
            } finally { Pop-Location }
        } finally {
            $env:VITE_APP_VERSION = $savedFrontendVersion
            $env:GOCACHE = $savedGoCache
            $env:GOMODCACHE = $savedGoModCache
        }
        $buildInfo = [ordered]@{
            version = $version
            built_at = (Get-Date).ToString('o')
            source_directory = $sourceDirectory
            source_state = 'local working tree, including uncommitted changes'
            sha256 = (Get-FileHash -LiteralPath $binaryPath -Algorithm SHA256).Hash
        }
        $buildInfo | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $PreparedDirectory 'build-info.json') -Encoding UTF8
        $preparedWindows = Join-Path $PreparedDirectory 'windows'
        [IO.Directory]::CreateDirectory($preparedWindows) | Out-Null
        Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'octopus-roam.cmd') -Destination $PreparedDirectory
        foreach ($scriptName in @('octopus-roam-menu.ps1', 'octopus-roam-start.ps1', 'octopus-roam-autostart.ps1', 'send-stop-signal.ps1')) {
            Copy-Item -LiteralPath (Join-Path $PSScriptRoot $scriptName) -Destination $preparedWindows
        }
    }

    $PreparedDirectory = [IO.Path]::GetFullPath($PreparedDirectory)
    $newExecutable = Join-Path $PreparedDirectory 'octopus-roam.exe'
    $manifestPath = Join-Path $PreparedDirectory 'build-info.json'
    $manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
    if ((Get-FileHash -LiteralPath $newExecutable -Algorithm SHA256).Hash -ne $manifest.sha256) {
        throw 'Prepared executable does not match build-info.json.'
    }
    $launcherFiles = @('octopus-roam.cmd', 'windows\octopus-roam-menu.ps1', 'windows\octopus-roam-start.ps1', 'windows\octopus-roam-autostart.ps1', 'windows\send-stop-signal.ps1')
    $hasLauncherPackage = @( $launcherFiles | Where-Object { Test-Path -LiteralPath (Join-Path $PreparedDirectory $_) } ).Count -gt 0
    if ($hasLauncherPackage) {
        foreach ($relativePath in $launcherFiles) {
            if (-not (Test-Path -LiteralPath (Join-Path $PreparedDirectory $relativePath) -PathType Leaf)) {
                throw "Incomplete prepared launcher package: $relativePath"
            }
        }
    }
    Write-Host "Prepared update: $PreparedDirectory"
    if (-not $Apply) {
        Write-Host 'Build completed. The running instance has not been changed.'
        Write-Host 'After closing AI clients, run this script with -Apply -PreparedDirectory pointing to the directory above.'
        exit 0
    }

    # Match the launcher's mutex so another launcher cannot start during replacement.
    $hasher = [Security.Cryptography.SHA256]::Create()
    try {
        $lockId = [BitConverter]::ToString($hasher.ComputeHash([Text.Encoding]::UTF8.GetBytes($runtimePath.ToUpperInvariant()))).Replace('-', '')
    } finally { $hasher.Dispose() }
    $mutex = New-Object Threading.Mutex($false, "Local\OctopusRoam-$lockId")
    $ownsMutex = $false
    try {
        try { $ownsMutex = $mutex.WaitOne(10000) } catch [Threading.AbandonedMutexException] { $ownsMutex = $true }
        if (-not $ownsMutex) { throw 'Another launcher or update is busy.' }
        $running = @(Get-RuntimeProcess)
        if ($running.Count -gt 1) { throw 'Multiple instances are using this runtime. Resolve them before updating.' }
        if ($running.Count -eq 1) {
            Write-Host 'Requesting graceful shutdown...'
            $signalLog = Join-Path $PreparedDirectory ('stop-' + (Get-Date -Format 'yyyyMMdd-HHmmss-fff'))
            $helperPath = Join-Path $PSScriptRoot 'send-stop-signal.ps1'
            $helperArguments = '-NoProfile -ExecutionPolicy RemoteSigned -File "{0}" -TargetProcessId {1} -ExpectedExecutable "{2}" -ResultPath "{3}"' -f $helperPath, $running[0].Id, $runtimeExecutable, "$signalLog.json"
            $helper = Start-Process -FilePath $powerShellPath -ArgumentList $helperArguments -WindowStyle Hidden -PassThru `
                -RedirectStandardOutput "$signalLog.out.log" -RedirectStandardError "$signalLog.err.log"
            $null = $helper.Handle
            if (-not $helper.WaitForExit(15000)) { throw "Signal helper timed out. See $signalLog.err.log" }
            if (-not (Test-Path -LiteralPath "$signalLog.json")) { throw "Signal helper did not report completion (exit $($helper.ExitCode)). See $signalLog.err.log" }
            $signalResult = Get-Content -LiteralPath "$signalLog.json" -Raw | ConvertFrom-Json
            if (-not $signalResult.success) { throw $signalResult.error }
            if (-not $running[0].WaitForExit(60000)) { throw 'Octopus Roam did not exit within 60 seconds. No files were replaced.' }
        }
        if (@(Get-RuntimeProcess).Count -ne 0) { throw 'Octopus Roam is still running. No files were replaced.' }

        $backupDirectory = Join-Path $runtimePath ('backups\before-update-' + (Get-Date -Format 'yyyyMMdd-HHmmss-fff'))
        [IO.Directory]::CreateDirectory($backupDirectory) | Out-Null
        Copy-Item -LiteralPath (Join-Path $runtimePath 'data') -Destination $backupDirectory -Recurse
        Copy-Item -LiteralPath $runtimeExecutable -Destination $backupDirectory
        $oldManifest = Join-Path $runtimePath 'build-info.json'
        if (Test-Path -LiteralPath $oldManifest) { Copy-Item -LiteralPath $oldManifest -Destination $backupDirectory }
        foreach ($launcherEntry in @('octopus-roam.cmd', 'windows', 'enable-autostart.cmd', 'disable-autostart.cmd')) {
            $existingLauncher = Join-Path $runtimePath $launcherEntry
            if (Test-Path -LiteralPath $existingLauncher) { Copy-Item -LiteralPath $existingLauncher -Destination $backupDirectory -Recurse }
        }
        Write-Host "Backup: $backupDirectory"
        Copy-Item -LiteralPath $newExecutable -Destination $runtimeExecutable -Force
        Copy-Item -LiteralPath $manifestPath -Destination $oldManifest -Force
        if ($hasLauncherPackage) {
            [IO.Directory]::CreateDirectory((Join-Path $runtimePath 'windows')) | Out-Null
            foreach ($relativePath in $launcherFiles) {
                Copy-Item -LiteralPath (Join-Path $PreparedDirectory $relativePath) -Destination (Join-Path $runtimePath $relativePath) -Force
            }
            # Remove the superseded entry points only after their replacements
            # are installed. The files were preserved in the update backup above.
            foreach ($legacyEntry in @('enable-autostart.cmd', 'disable-autostart.cmd')) {
                $legacyPath = Join-Path $runtimePath $legacyEntry
                if (Test-Path -LiteralPath $legacyPath -PathType Leaf) { Remove-Item -LiteralPath $legacyPath -Force }
            }
        }
    } finally {
        if ($ownsMutex) { $mutex.ReleaseMutex() }
        $mutex.Dispose()
    }
    $packagedLauncher = Join-Path $runtimePath 'windows\octopus-roam-start.ps1'
    if (Test-Path -LiteralPath $packagedLauncher -PathType Leaf) {
        & $powerShellPath -NoProfile -ExecutionPolicy RemoteSigned -File $packagedLauncher -RuntimeDirectory $runtimePath
    } else {
        & $powerShellPath -NoProfile -ExecutionPolicy RemoteSigned -File (Join-Path $runtimePath 'octopus-roam-start.ps1')
    }
    if ($LASTEXITCODE -ne 0) { throw "Restart failed. Inspect runtime logs. Backup: $backupDirectory" }
    Write-Host "Updated to $($manifest.version). Reload the browser; if necessary, use Settings > Version information > Force refresh."
} catch {
    Write-Error $_.Exception.Message -ErrorAction Continue
    exit 1
}
