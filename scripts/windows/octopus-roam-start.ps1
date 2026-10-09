# Windows PowerShell 5.1 compatible. Keep this file beside octopus-roam.exe.
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$script:OctopusDirectory = $PSScriptRoot

function Assert-OctopusDeployment {
    foreach ($relativePath in @('octopus-roam.exe', 'data\config.json')) {
        if (-not (Test-Path -LiteralPath (Join-Path $script:OctopusDirectory $relativePath) -PathType Leaf)) {
            throw "Missing $relativePath. Copy the launcher scripts beside the deployed octopus-roam.exe and prepare data\config.json first."
        }
    }
    try {
        $config = Get-Content -LiteralPath (Join-Path $script:OctopusDirectory 'data\config.json') -Raw | ConvertFrom-Json
        if ($null -eq $config) { throw 'Empty configuration.' }
    } catch {
        throw 'data\config.json must contain a valid JSON configuration.'
    }
}

function Get-OctopusProcess {
    $executable = Join-Path $script:OctopusDirectory 'octopus-roam.exe'
    foreach ($candidate in @(Get-Process -Name octopus-roam -ErrorAction SilentlyContinue)) {
        # Some processes owned by other users do not expose their executable path.
        try { $candidatePath = $candidate.Path } catch { continue }
        if ($candidatePath -and [string]::Equals($candidatePath, $executable, [StringComparison]::OrdinalIgnoreCase)) {
            $candidate
        }
    }
}

function Start-Octopus {
    Assert-OctopusDeployment
    $hasher = [Security.Cryptography.SHA256]::Create()
    try {
        $bytes = [Text.Encoding]::UTF8.GetBytes($script:OctopusDirectory.ToUpperInvariant())
        $lockId = [BitConverter]::ToString($hasher.ComputeHash($bytes)).Replace('-', '')
    } finally {
        $hasher.Dispose()
    }

    $mutex = New-Object Threading.Mutex($false, "Local\OctopusRoam-$lockId")
    $ownsMutex = $false
    try {
        try {
            $ownsMutex = $mutex.WaitOne(10000)
        } catch [Threading.AbandonedMutexException] {
            $ownsMutex = $true
        }
        if (-not $ownsMutex) { throw 'Another launcher is busy. Try again shortly.' }
        $running = @(Get-OctopusProcess)
        if ($running.Count -gt 0) {
            Write-Host "Octopus Roam is already running (PID $($running[0].Id))."
            return
        }

        $logsDirectory = Join-Path $script:OctopusDirectory 'logs'
        [IO.Directory]::CreateDirectory($logsDirectory) | Out-Null
        $logName = 'startup-' + (Get-Date -Format 'yyyyMMdd-HHmmss-fff') + "-$PID"
        $stdoutPath = Join-Path $logsDirectory "$logName.out.log"
        $stderrPath = Join-Path $logsDirectory "$logName.err.log"
        # Reserve unique log files without overwriting existing output.
        [IO.File]::Open($stdoutPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write).Dispose()
        [IO.File]::Open($stderrPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write).Dispose()
        # Resolve the directory literally and let cmd redirect to fixed relative
        # file names. PowerShell 5.1 treats brackets in redirection paths as globs.
        # The command contains only fixed arguments plus a generated timestamp/PID.
        $runCommand = '"".\octopus-roam.exe" start --config "data\config.json" 1>".\logs\' + $logName + '.out.log" 2>".\logs\' + $logName + '.err.log""'
        Push-Location -LiteralPath $script:OctopusDirectory
        try {
            $process = Start-Process -FilePath (Join-Path ([Environment]::GetFolderPath('System')) 'cmd.exe') `
                -ArgumentList @('/d', '/v:off', '/s', '/c', $runCommand) `
                -WorkingDirectory '.' -WindowStyle Hidden -PassThru
        } finally {
            Pop-Location
        }
        if ($process.WaitForExit(2000)) {
            throw "Octopus Roam exited during startup. Check $logsDirectory."
        }
        Write-Host "Started Octopus Roam. Logs: $logsDirectory"
    } finally {
        if ($ownsMutex) { $mutex.ReleaseMutex() }
        $mutex.Dispose()
    }
}

if ($MyInvocation.InvocationName -ne '.') {
    try {
        Start-Octopus
    } catch {
        $message = $_.Exception.Message
        # The sign-in launcher is hidden, so keep failures available for diagnosis.
        try {
            $logsDirectory = Join-Path $script:OctopusDirectory 'logs'
            [IO.Directory]::CreateDirectory($logsDirectory) | Out-Null
            Add-Content -LiteralPath (Join-Path $logsDirectory 'autostart-errors.log') `
                -Value ("{0} {1}" -f (Get-Date -Format 's'), $message) -Encoding UTF8
        } catch { }
        Write-Error $message -ErrorAction Continue
        exit 1
    }
}
