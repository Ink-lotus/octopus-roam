# Run with Windows PowerShell 5.1. All shortcut changes use a temporary folder.
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'octopus-roam-autostart.ps1')

function Assert-True {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) { throw $Message }
}

function Assert-Fails {
    param([scriptblock]$Operation, [string]$Expected)
    $failed = $false
    try { & $Operation } catch {
        $failed = $true
        Assert-True ($_.Exception.Message.Contains($Expected)) "Unexpected failure: $($_.Exception.Message)"
    }
    Assert-True $failed "Expected failure containing: $Expected"
}

# Spaces, brackets, apostrophes, ampersands and non-ASCII characters must be literal.
$fixtureName = "octopus-roam-startup [test] & O'Brien " + [char]0x6D4B + [char]0x8BD5 + '-' + [guid]::NewGuid().ToString('N')
$fixture = Join-Path (Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'Temp') $fixtureName
$script:OctopusDirectory = Join-Path $fixture 'runtime'
$script:TestStartupDirectory = Join-Path $fixture 'startup'
function Get-OctopusStartupDirectory { return $script:TestStartupDirectory }
[IO.Directory]::CreateDirectory($script:OctopusDirectory) | Out-Null
$shortcutPath = Join-Path $script:TestStartupDirectory 'Octopus Roam.lnk'

Invoke-OctopusAutostart -Mode Disable
Assert-Fails { Invoke-OctopusAutostart -Mode Enable } 'Missing octopus-roam.exe'
Assert-True (-not (Test-Path -LiteralPath $shortcutPath)) 'An incomplete deployment created a startup shortcut.'

$fakeProgram = @'
using System;
using System.IO;
using System.Threading;
public static class OctopusLauncherFixture {
    public static void Main(string[] args) {
        Console.OutputEncoding = new System.Text.UTF8Encoding(false);
        Console.WriteLine("cwd=" + Environment.CurrentDirectory);
        Console.WriteLine("args=" + string.Join("|", args));
        Console.Error.WriteLine("fixture stderr");
        if (!File.Exists("exit-early")) Thread.Sleep(7000);
    }
}
'@
$fixtureExecutable = Join-Path ([IO.Path]::GetTempPath()) ('octopus-roam-launcher-fixture-' + [guid]::NewGuid().ToString('N') + '.exe')
Add-Type -TypeDefinition $fakeProgram -OutputAssembly $fixtureExecutable -OutputType ConsoleApplication
Copy-Item -LiteralPath $fixtureExecutable -Destination (Join-Path $script:OctopusDirectory 'octopus-roam.exe')
$dataDirectory = Join-Path $script:OctopusDirectory 'data'
[IO.Directory]::CreateDirectory($dataDirectory) | Out-Null
$configPath = Join-Path $dataDirectory 'config.json'
[IO.File]::WriteAllText($configPath, '{')
Assert-Fails { Invoke-OctopusAutostart -Mode Enable } 'valid JSON'
Assert-True (-not (Test-Path -LiteralPath $shortcutPath)) 'Invalid JSON created a startup shortcut.'
[IO.File]::WriteAllText($configPath, '{"server":{"host":"127.0.0.1","port":8080},"database":{"type":"sqlite","path":"data/data.db"}}')

foreach ($file in @('octopus-roam-start.ps1', 'octopus-roam-autostart.ps1', 'enable-autostart.cmd', 'disable-autostart.cmd')) {
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot $file) -Destination (Join-Path $script:OctopusDirectory $file)
}
Invoke-OctopusAutostart -Mode Enable
Assert-True (Test-Path -LiteralPath $shortcutPath -PathType Leaf) 'Enable did not create the shortcut.'
$firstShortcut = [Convert]::ToBase64String([IO.File]::ReadAllBytes($shortcutPath))
Invoke-OctopusAutostart -Mode Enable
Assert-True ($firstShortcut -eq [Convert]::ToBase64String([IO.File]::ReadAllBytes($shortcutPath))) 'Repeated enable rewrote the shortcut.'
Invoke-OctopusAutostart -Mode Status

$shell = New-Object -ComObject WScript.Shell
$shortcut = $null
$launched = $null
try {
    $shortcut = $shell.CreateShortcut($shortcutPath)
    Assert-True ($shortcut.WorkingDirectory -eq $script:OctopusDirectory) 'Shortcut working directory is incorrect.'
    Assert-True ($shortcut.Arguments.Contains('-WindowStyle Hidden')) 'Startup launcher must be hidden.'
    # Execute exactly the saved shortcut target and arguments, from an unrelated cwd.
    $launched = Start-Process -FilePath $shortcut.TargetPath -ArgumentList $shortcut.Arguments `
        -WorkingDirectory $PSScriptRoot -WindowStyle Hidden -PassThru
    Assert-True ($launched.WaitForExit(15000)) 'Launcher did not finish.'
    Assert-True ($launched.ExitCode -eq 0) 'Saved shortcut command failed.'
    $running = @(Get-OctopusProcess)
    Assert-True ($running.Count -eq 1) 'Launcher did not start exactly one fixture process.'
    Start-Octopus
    Assert-True (@(Get-OctopusProcess).Count -eq 1) 'Duplicate launcher started another process.'
    Invoke-OctopusAutostart -Mode Disable
    Assert-True (-not (Test-Path -LiteralPath $shortcutPath)) 'Disable did not remove the shortcut.'
    Assert-True (-not $running[0].HasExited) 'Disable stopped the running process.'
    Assert-True ($running[0].WaitForExit(15000)) 'Fixture did not exit by itself.'
} finally {
    if ($null -ne $shortcut) { [Runtime.InteropServices.Marshal]::FinalReleaseComObject($shortcut) | Out-Null }
    [Runtime.InteropServices.Marshal]::FinalReleaseComObject($shell) | Out-Null
}

$logsDirectory = Join-Path $script:OctopusDirectory 'logs'
$stdoutFile = @(Get-ChildItem -LiteralPath $logsDirectory -Filter '*.out.log')
Assert-True ($stdoutFile.Count -eq 1) 'Duplicate start created extra logs.'
$stdout = Get-Content -LiteralPath $stdoutFile[0].FullName -Raw -Encoding UTF8
Assert-True ($stdout.Contains("cwd=$script:OctopusDirectory")) 'Child did not use the deployment working directory.'
Assert-True ($stdout.Contains('args=start|--config|data\config.json')) 'Child received incorrect arguments.'
$stderrFile = @(Get-ChildItem -LiteralPath $logsDirectory -Filter '*.err.log')
Assert-True ((Get-Content -LiteralPath $stderrFile[0].FullName -Raw).Contains('fixture stderr')) 'Child stderr was not captured.'
[IO.File]::WriteAllText((Join-Path $script:OctopusDirectory 'exit-early'), '')
Assert-Fails { Start-Octopus } 'exited during startup'

# An unrelated shortcut with the same name must survive both enable and disable.
$shell = New-Object -ComObject WScript.Shell
$shortcut = $null
try {
    $shortcut = $shell.CreateShortcut($shortcutPath)
    $shortcut.TargetPath = Join-Path ([Environment]::GetFolderPath('System')) 'notepad.exe'
    $shortcut.Save()
} finally {
    if ($null -ne $shortcut) { [Runtime.InteropServices.Marshal]::FinalReleaseComObject($shortcut) | Out-Null }
    [Runtime.InteropServices.Marshal]::FinalReleaseComObject($shell) | Out-Null
}
$foreignShortcut = [Convert]::ToBase64String([IO.File]::ReadAllBytes($shortcutPath))
Assert-Fails { Invoke-OctopusAutostart -Mode Enable } 'another deployment'
Assert-Fails { Invoke-OctopusAutostart -Mode Disable } 'another deployment'
Assert-True ($foreignShortcut -eq [Convert]::ToBase64String([IO.File]::ReadAllBytes($shortcutPath))) 'Another deployment shortcut was modified.'
Assert-True (Test-Path -LiteralPath $configPath) 'Deployment data was removed.'
Assert-True (@(Get-OctopusProcess).Count -eq 0) 'A fixture process is still running.'

# Exercise the double-click wrappers with a harmless fixture instead of changing
# the real user's startup folder. Verify argument forwarding and exit codes.
$wrapperDirectory = Join-Path $fixture 'wrappers % !'
[IO.Directory]::CreateDirectory($wrapperDirectory) | Out-Null
$wrapperFixture = @'
param([string]$Action)
[IO.File]::WriteAllText((Join-Path $PSScriptRoot 'action.txt'), $Action)
if ($Action -eq 'Disable') { exit 17 }
'@
[IO.File]::WriteAllText((Join-Path $wrapperDirectory 'octopus-roam-autostart.ps1'), $wrapperFixture)
foreach ($wrapperMode in @('Enable', 'Disable')) {
    $wrapperName = $wrapperMode.ToLowerInvariant() + '-autostart.cmd'
    $wrapperPath = Join-Path $wrapperDirectory $wrapperName
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot $wrapperName) -Destination $wrapperPath
    $wrapperCommand = '""' + $wrapperPath + '" <NUL >NUL 2>&1"'
    $wrapperProcess = Start-Process -FilePath (Join-Path ([Environment]::GetFolderPath('System')) 'cmd.exe') `
        -ArgumentList @('/d', '/v:off', '/s', '/c', $wrapperCommand) -WindowStyle Hidden -PassThru
    Assert-True ($wrapperProcess.WaitForExit(15000)) 'Double-click wrapper did not finish.'
    $expectedExit = if ($wrapperMode -eq 'Disable') { 17 } else { 0 }
    Assert-True ($wrapperProcess.ExitCode -eq $expectedExit) 'Wrapper lost the PowerShell exit code.'
    Assert-True ([IO.File]::ReadAllText((Join-Path $wrapperDirectory 'action.txt')) -eq $wrapperMode) 'Wrapper passed an incorrect action.'
}
Write-Host 'PASS: shortcuts, idempotence, path quoting, hidden launch, cwd, logs, duplicate start, early exit, ownership, and double-click wrappers.'
Write-Host "Temporary test artifacts: $fixture"
