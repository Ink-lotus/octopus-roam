# Windows PowerShell 5.1 compatible. Packaged launchers pass the runtime directory.
param([string]$RuntimeDirectory = $PSScriptRoot)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$script:OctopusDirectory = [IO.Path]::GetFullPath($RuntimeDirectory).TrimEnd('\')

function Get-OctopusScriptPath {
    param([Parameter(Mandatory = $true)][string]$Name)
    # Keep existing flat deployments and their saved startup shortcuts working.
    $flatPath = Join-Path $script:OctopusDirectory $Name
    if (Test-Path -LiteralPath $flatPath -PathType Leaf) { return $flatPath }
    return Join-Path $script:OctopusDirectory "windows\$Name"
}

function Initialize-OctopusDeployment {
    if (-not (Test-Path -LiteralPath (Join-Path $script:OctopusDirectory 'octopus-roam.exe') -PathType Leaf)) {
        throw 'Missing octopus-roam.exe. Extract the entire Windows ZIP before starting.'
    }
    foreach ($directory in @('data', 'logs', 'backups')) {
        [IO.Directory]::CreateDirectory((Join-Path $script:OctopusDirectory $directory)) | Out-Null
    }
    $configPath = Join-Path $script:OctopusDirectory 'data\config.json'
    if (-not (Test-Path -LiteralPath $configPath)) {
        $config = @'
{
  "server": { "host": "127.0.0.1", "port": 8080 },
  "database": { "type": "sqlite", "path": "data/data.db" },
  "log": { "level": "info" }
}
'@
        $stream = [IO.File]::Open($configPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write)
        try {
            $bytes = [Text.Encoding]::UTF8.GetBytes($config + [Environment]::NewLine)
            $stream.Write($bytes, 0, $bytes.Length)
        } finally { $stream.Dispose() }
    }
}

function Get-OctopusEndpoint {
    $config = Get-Content -LiteralPath (Join-Path $script:OctopusDirectory 'data\config.json') -Raw | ConvertFrom-Json
    $listenHost = '0.0.0.0'
    $listenPort = '8080'
    if ($null -ne $config -and $config.PSObject.Properties['server'] -and $null -ne $config.server) {
        if ($config.server.PSObject.Properties['host']) { $listenHost = [string]$config.server.host }
        if ($config.server.PSObject.Properties['port']) { $listenPort = [string]$config.server.port }
    }
    # Match Viper's non-empty environment overrides when checking the listen port.
    if ($env:OCTOPUS_ROAM_SERVER_HOST) { $listenHost = $env:OCTOPUS_ROAM_SERVER_HOST }
    if ($env:OCTOPUS_ROAM_SERVER_PORT) { $listenPort = $env:OCTOPUS_ROAM_SERVER_PORT }
    $portNumber = 0
    if (-not [int]::TryParse($listenPort, [ref]$portNumber) -or $portNumber -lt 1 -or $portNumber -gt 65535) {
        throw 'server.port must be an integer from 1 to 65535.'
    }
    return [pscustomobject]@{ Host = $listenHost; Port = $portNumber }
}

function Assert-OctopusPortAvailable {
    $endpoint = Get-OctopusEndpoint
    $address = $null
    if ([string]::IsNullOrEmpty($endpoint.Host)) {
        $address = [Net.IPAddress]::IPv6Any
    } elseif (-not [Net.IPAddress]::TryParse($endpoint.Host, [ref]$address)) {
        $addresses = @([Net.Dns]::GetHostAddresses($endpoint.Host))
        $address = $addresses | Where-Object { $_.AddressFamily -eq [Net.Sockets.AddressFamily]::InterNetwork } | Select-Object -First 1
        if ($null -eq $address) { $address = $addresses | Select-Object -First 1 }
        if ($null -eq $address) { throw "Cannot resolve server.host: $($endpoint.Host)" }
    }
    $socket = New-Object Net.Sockets.Socket($address.AddressFamily, [Net.Sockets.SocketType]::Stream, [Net.Sockets.ProtocolType]::Tcp)
    try {
        $socket.ExclusiveAddressUse = $true
        if ($address.Equals([Net.IPAddress]::IPv6Any)) { $socket.DualMode = $true }
        $socket.Bind((New-Object Net.IPEndPoint($address, $endpoint.Port)))
    } catch {
        throw "Port $($endpoint.Port) on $($endpoint.Host) is unavailable. Close the application using it or change server.port in data\config.json."
    } finally { $socket.Dispose() }
}

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

function New-OctopusMutex {
    $hasher = [Security.Cryptography.SHA256]::Create()
    try {
        $bytes = [Text.Encoding]::UTF8.GetBytes($script:OctopusDirectory.ToUpperInvariant())
        $lockId = [BitConverter]::ToString($hasher.ComputeHash($bytes)).Replace('-', '')
    } finally {
        $hasher.Dispose()
    }

    return New-Object Threading.Mutex($false, "Local\OctopusRoam-$lockId")
}

function Start-Octopus {
    $mutex = New-OctopusMutex
    $ownsMutex = $false
    try {
        try {
            $ownsMutex = $mutex.WaitOne(10000)
        } catch [Threading.AbandonedMutexException] {
            $ownsMutex = $true
        }
        if (-not $ownsMutex) { throw 'Another launcher is busy. Try again shortly.' }
        $running = @(Get-OctopusProcess)
        if ($running.Count -gt 1) { throw 'Multiple instances are using this deployment. Resolve them before starting.' }
        if ($running.Count -gt 0) {
            Write-Host "Octopus Roam is already running (PID $($running[0].Id))."
            return
        }

        Initialize-OctopusDeployment
        Assert-OctopusDeployment
        Assert-OctopusPortAvailable

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

function Stop-Octopus {
    param([ValidateRange(1, 600)][int]$TimeoutSeconds = 60)
    $mutex = New-OctopusMutex
    $ownsMutex = $false
    try {
        try { $ownsMutex = $mutex.WaitOne(10000) } catch [Threading.AbandonedMutexException] { $ownsMutex = $true }
        if (-not $ownsMutex) { throw 'Another launcher or update is busy. Try again shortly.' }
        $running = @(Get-OctopusProcess)
        if ($running.Count -eq 0) {
            Write-Host 'Octopus Roam is already stopped.'
            return
        }
        if ($running.Count -gt 1) { throw 'Multiple instances are using this deployment. Resolve them before stopping.' }
        $helperPath = Get-OctopusScriptPath 'send-stop-signal.ps1'
        if (-not (Test-Path -LiteralPath $helperPath -PathType Leaf)) { throw 'Missing windows\send-stop-signal.ps1. Extract the entire Windows ZIP.' }
        $logsDirectory = Join-Path $script:OctopusDirectory 'logs'
        [IO.Directory]::CreateDirectory($logsDirectory) | Out-Null
        $resultPath = Join-Path $logsDirectory ('shutdown-' + [guid]::NewGuid().ToString('N') + '.json')
        $executable = Join-Path $script:OctopusDirectory 'octopus-roam.exe'
        $powerShellPath = Join-Path ([Environment]::GetFolderPath('System')) 'WindowsPowerShell\v1.0\powershell.exe'
        $helperArguments = '-NoProfile -ExecutionPolicy RemoteSigned -File "{0}" -TargetProcessId {1} -ExpectedExecutable "{2}" -ResultPath "{3}"' -f $helperPath, $running[0].Id, $executable, $resultPath
        Write-Host 'Waiting for Octopus Roam to save data and exit...'
        $helper = Start-Process -FilePath $powerShellPath -ArgumentList $helperArguments -WindowStyle Hidden -PassThru
        $null = $helper.Handle
        if (-not $helper.WaitForExit(15000)) { throw "The shutdown helper timed out. See $resultPath" }
        if (-not (Test-Path -LiteralPath $resultPath -PathType Leaf)) { throw "The shutdown helper did not report completion (exit $($helper.ExitCode))." }
        $result = Get-Content -LiteralPath $resultPath -Raw | ConvertFrom-Json
        if (-not $result.success) { throw $result.error }
        if (-not $running[0].WaitForExit($TimeoutSeconds * 1000)) {
            throw "Octopus Roam did not exit within $TimeoutSeconds seconds. It has not been force-stopped; check the logs and retry."
        }
        if (@(Get-OctopusProcess).Count -ne 0) { throw 'Octopus Roam is still running.' }
        Write-Host 'Octopus Roam has stopped.'
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
