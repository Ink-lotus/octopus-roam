# Process and ZIP operations for the local acceptance driver. No existing
# runtime or archive is overwritten; the caller supplies isolated test paths.
param(
    [Parameter(Mandatory = $true)][ValidateSet('Extract', 'Pack', 'Start', 'Status', 'Stop')][string]$Action,
    [Parameter(Mandatory = $true)][string]$Directory,
    [string]$Archive,
    [string]$Config
)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$Directory = [IO.Path]::GetFullPath($Directory)
$executable = Join-Path $Directory 'octopus-roam.exe'
switch ($Action) {
    'Extract' {
        if (Test-Path -LiteralPath $Directory) { throw 'Extraction directory must be new.' }
        Expand-Archive -LiteralPath $Archive -DestinationPath $Directory
    }
    'Pack' {
        if (Test-Path -LiteralPath $Archive) { throw 'Archive must be new.' }
        Add-Type -AssemblyName System.IO.Compression.FileSystem
        [IO.Compression.ZipFile]::CreateFromDirectory($Directory, $Archive, [IO.Compression.CompressionLevel]::Optimal, $false)
    }
    'Start' {
        $runtimeConfig = [IO.Path]::GetFullPath($Config)
        if (-not $runtimeConfig.StartsWith($Directory + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) { throw 'Config must be inside the isolated runtime.' }
        $arguments = @('start', '--config', ('"' + $runtimeConfig + '"'))
        $process = Start-Process -FilePath $executable -ArgumentList $arguments -WorkingDirectory $Directory -WindowStyle Hidden -RedirectStandardOutput (Join-Path $Directory 'stdout.log') -RedirectStandardError (Join-Path $Directory 'stderr.log') -PassThru
        @{ id = $process.Id } | ConvertTo-Json -Compress
    }
    'Status' {
        $processes = @(Get-Process -Name octopus-roam -ErrorAction SilentlyContinue | Where-Object { [string]::Equals($_.Path, $executable, [StringComparison]::OrdinalIgnoreCase) })
        @{ ids = @($processes | ForEach-Object Id) } | ConvertTo-Json -Compress
    }
    'Stop' {
        $processes = @(Get-Process -Name octopus-roam -ErrorAction SilentlyContinue | Where-Object { [string]::Equals($_.Path, $executable, [StringComparison]::OrdinalIgnoreCase) })
        foreach ($process in $processes) {
            # Retain the process handle before it exits so Get-Process can
            # report the real exit code after WaitForExit.
            $null = $process.Handle
            $resultPath = Join-Path $Directory ('stop-' + $process.Id + '.json')
            & pwsh -NoProfile -File (Join-Path $PSScriptRoot '..\windows\send-stop-signal.ps1') -TargetProcessId $process.Id -ExpectedExecutable $executable -ResultPath $resultPath
            if ($LASTEXITCODE -ne 0) { throw 'Normal stop signal failed.' }
            if (-not $process.WaitForExit(15000)) { throw 'Normal stop timed out.' }
            if ($null -eq $process.ExitCode -or $process.ExitCode -ne 0) { throw 'Normal stop returned a failure code or an unavailable exit code.' }
        }
        @{ stopped = $processes.Count; normal_exit = $true } | ConvertTo-Json -Compress
    }
}
