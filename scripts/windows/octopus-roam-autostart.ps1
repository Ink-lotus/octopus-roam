# Windows PowerShell 5.1 compatible. Changes only the current user's Startup shortcut.
[CmdletBinding()]
param(
    [ValidateSet('Enable', 'Disable', 'Status')]
    [string]$Action = 'Status'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'octopus-roam-start.ps1')

function Get-OctopusStartupDirectory {
    $directory = [Environment]::GetFolderPath('Startup')
    if ([string]::IsNullOrWhiteSpace($directory)) { throw 'The current user has no Windows Startup folder.' }
    return $directory
}

function Get-OctopusShortcutDefinition {
    $powershell = Join-Path ([Environment]::GetFolderPath('System')) 'WindowsPowerShell\v1.0\powershell.exe'
    $launcher = Join-Path $script:OctopusDirectory 'octopus-roam-start.ps1'
    return [pscustomobject]@{
        TargetPath = $powershell
        Arguments = '-NoLogo -NoProfile -NonInteractive -WindowStyle Hidden -ExecutionPolicy RemoteSigned -File "' + $launcher + '"'
        WorkingDirectory = $script:OctopusDirectory
    }
}

function Invoke-OctopusAutostart {
    param([Parameter(Mandatory = $true)][ValidateSet('Enable', 'Disable', 'Status')][string]$Mode)

    if ($Mode -eq 'Enable') { Assert-OctopusDeployment }
    $startupDirectory = Get-OctopusStartupDirectory
    $shortcutPath = Join-Path $startupDirectory 'Octopus Roam.lnk'
    $definition = Get-OctopusShortcutDefinition
    $exists = Test-Path -LiteralPath $shortcutPath
    if (-not $exists -and $Mode -ne 'Enable') {
        Write-Host 'Octopus Roam automatic startup is disabled.'
        return
    }

    if ($Mode -eq 'Enable') { [IO.Directory]::CreateDirectory($startupDirectory) | Out-Null }
    $shell = New-Object -ComObject WScript.Shell
    $shortcut = $null
    try {
        $shortcut = $shell.CreateShortcut($shortcutPath)
        if ($exists) {
            $owned = [string]::Equals($shortcut.TargetPath, $definition.TargetPath, [StringComparison]::OrdinalIgnoreCase) `
                -and [string]::Equals($shortcut.Arguments, $definition.Arguments, [StringComparison]::Ordinal) `
                -and [string]::Equals($shortcut.WorkingDirectory, $definition.WorkingDirectory, [StringComparison]::OrdinalIgnoreCase)
            if (-not $owned) {
                throw 'The Octopus Roam startup shortcut belongs to another deployment or was modified. Manage that shortcut before retrying.'
            }
        }

        switch ($Mode) {
            'Enable' {
                if (-not $exists) {
                    $shortcut.TargetPath = $definition.TargetPath
                    $shortcut.Arguments = $definition.Arguments
                    $shortcut.WorkingDirectory = $definition.WorkingDirectory
                    $shortcut.WindowStyle = 7
                    $shortcut.Description = 'Start this local Octopus Roam deployment after Windows sign-in.'
                    $shortcut.Save()
                }
                Write-Host 'Automatic startup enabled for the current user. Octopus Roam will start after your next Windows sign-in.'
            }
            'Disable' {
                # Remove only the verified shortcut, never the executable or data.
                Remove-Item -LiteralPath $shortcutPath -Force
                Write-Host 'Automatic startup disabled. Any running Octopus Roam process is left running.'
            }
            'Status' {
                Write-Host "Automatic startup is enabled for: $($definition.WorkingDirectory)"
            }
        }
    } finally {
        if ($null -ne $shortcut) { [Runtime.InteropServices.Marshal]::FinalReleaseComObject($shortcut) | Out-Null }
        [Runtime.InteropServices.Marshal]::FinalReleaseComObject($shell) | Out-Null
    }
}

if ($MyInvocation.InvocationName -ne '.') {
    try {
        Invoke-OctopusAutostart -Mode $Action
    } catch {
        Write-Error $_.Exception.Message -ErrorAction Continue
        exit 1
    }
}
