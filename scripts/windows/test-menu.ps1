# Run with Windows PowerShell 5.1. Uses synthetic processes and an isolated Startup folder.
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Assert-True([bool]$Condition, [string]$Message) {
    if (-not $Condition) { throw $Message }
}

function Assert-Fails([scriptblock]$Operation, [string]$Expected = '') {
    $failed = $false
    try { & $Operation } catch {
        $failed = $true
        if ($Expected) { Assert-True ($_.Exception.Message.Contains($Expected)) "Unexpected failure: $($_.Exception.Message)" }
    }
    Assert-True $failed "Expected failure containing: $Expected"
}

function Get-FreePort {
    $probe = New-Object Net.Sockets.TcpListener([Net.IPAddress]::Loopback, 0)
    try {
        $probe.Start()
        return $probe.LocalEndpoint.Port
    } finally { $probe.Stop() }
}

$fixtureRoot = Join-Path ([IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..\build'))) ('menu-check-' + [guid]::NewGuid().ToString('N'))
$fixtureName = "runtime [test] & O'Brien % ! " + [char]0x6D4B + [char]0x8BD5
$fixtureRuntime = Join-Path $fixtureRoot $fixtureName
$foreignRuntime = Join-Path $fixtureRoot 'other deployment'
$script:TestStartupDirectory = Join-Path $fixtureRoot 'startup'
$shellPath = Join-Path ([Environment]::GetFolderPath('System')) 'WindowsPowerShell\v1.0\powershell.exe'
$savedHostOverride = $env:OCTOPUS_ROAM_SERVER_HOST
$savedPortOverride = $env:OCTOPUS_ROAM_SERVER_PORT
$env:OCTOPUS_ROAM_SERVER_HOST = $null
$env:OCTOPUS_ROAM_SERVER_PORT = $null

foreach ($runtime in @($fixtureRuntime, $foreignRuntime)) {
    [IO.Directory]::CreateDirectory((Join-Path $runtime 'windows')) | Out-Null
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'octopus-roam.cmd') -Destination $runtime
    foreach ($file in @('octopus-roam-menu.ps1', 'octopus-roam-start.ps1', 'octopus-roam-autostart.ps1', 'send-stop-signal.ps1')) {
        Copy-Item -LiteralPath (Join-Path $PSScriptRoot $file) -Destination (Join-Path $runtime 'windows')
    }
}

$fakeSource = @'
using System;
using System.IO;
using System.Threading;
public static class OctopusMenuFixture {
    public static void Main(string[] args) {
        if (File.Exists("exit-early")) return;
        var done = new ManualResetEvent(false);
        Console.CancelKeyPress += (sender, e) => {
            e.Cancel = true;
            File.AppendAllText("data/shutdown.txt", "graceful\n");
            done.Set();
        };
        File.WriteAllText("data/cwd.txt", Environment.CurrentDirectory);
        File.WriteAllText("data/args.txt", string.Join("|", args));
        Console.WriteLine("Menu fixture ready.");
        done.WaitOne(120000);
    }
}
'@
$fixtureExecutable = Join-Path $fixtureRoot 'fixture.exe'
Add-Type -TypeDefinition $fakeSource -OutputAssembly $fixtureExecutable -OutputType ConsoleApplication
foreach ($runtime in @($fixtureRuntime, $foreignRuntime)) {
    Copy-Item -LiteralPath $fixtureExecutable -Destination (Join-Path $runtime 'octopus-roam.exe')
}

. (Join-Path $fixtureRuntime 'windows\octopus-roam-menu.ps1') -RuntimeDirectory $fixtureRuntime
function Get-OctopusStartupDirectory { return $script:TestStartupDirectory }
$shortcutPath = Join-Path $script:TestStartupDirectory 'Octopus Roam.lnk'
$configPath = Join-Path $fixtureRuntime 'data\config.json'

try {
    # First-use preparation is independent of whichever real application owns 8080.
    Initialize-OctopusDeployment
    $defaultConfig = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
    Assert-True ($defaultConfig.server.host -eq '127.0.0.1' -and $defaultConfig.server.port -eq 8080) 'Incorrect first-use listen address.'
    Assert-True ($defaultConfig.database.type -eq 'sqlite' -and $defaultConfig.database.path -eq 'data/data.db') 'Incorrect first-use database.'
    Assert-True (-not (Test-Path -LiteralPath (Join-Path $fixtureRuntime 'data\data.db'))) 'Preparation initialized a database.'
    $firstConfig = [IO.File]::ReadAllText($configPath)
    Initialize-OctopusDeployment
    Assert-True ([IO.File]::ReadAllText($configPath) -eq $firstConfig) 'Preparation rewrote an existing configuration.'
    Assert-True ((Get-OctopusAutostartState) -eq 'Disabled') 'A fresh deployment enabled autostart.'

    $occupied = New-Object Net.Sockets.TcpListener([Net.IPAddress]::Loopback, 0)
    try {
        $occupied.Start()
        $fixturePort = $occupied.LocalEndpoint.Port
        $fixtureConfig = '{"server":{"host":"127.0.0.1","port":' + $fixturePort + '},"database":{"type":"sqlite","path":"data/data.db"}}'
        [IO.File]::WriteAllText($configPath, $fixtureConfig)
        Assert-Fails { Invoke-OctopusMenuAction '2' } 'Port '
        Assert-True (@(Get-OctopusProcess).Count -eq 0) 'Occupied port still launched a process.'
        Assert-True (-not (Test-Path -LiteralPath $shortcutPath)) 'Failed start enabled autostart.'
        $env:OCTOPUS_ROAM_SERVER_PORT = [string](Get-FreePort)
        Assert-OctopusPortAvailable
        $env:OCTOPUS_ROAM_SERVER_PORT = $null
    } finally { $occupied.Stop() }

    [IO.File]::WriteAllText((Join-Path $fixtureRuntime 'data\data.db'), 'database-sentinel')
    Invoke-OctopusMenuAction '1'
    $running = @(Get-OctopusProcess)
    Assert-True ($running.Count -eq 1) 'Option 1 did not start exactly one process.'
    Invoke-OctopusMenuAction '1'
    Assert-True (@(Get-OctopusProcess).Count -eq 1) 'Repeated option 1 duplicated the process.'
    Assert-True ((Get-OctopusAutostartState) -eq 'Disabled') 'Option 1 enabled autostart.'
    Assert-True ([IO.File]::ReadAllText((Join-Path $fixtureRuntime 'data\cwd.txt')) -eq $fixtureRuntime) 'Incorrect runtime working directory.'
    Assert-True ([IO.File]::ReadAllText((Join-Path $fixtureRuntime 'data\args.txt')) -eq 'start|--config|data\config.json') 'Incorrect application arguments.'
    Assert-True ((Get-OctopusAccessUrl) -eq "http://127.0.0.1:$fixturePort") 'Incorrect management URL.'

    $script:OctopusDirectory = $foreignRuntime
    Initialize-OctopusDeployment
    [IO.File]::WriteAllText((Join-Path $foreignRuntime 'data\config.json'), ('{"server":{"host":"127.0.0.1","port":' + (Get-FreePort) + '}}'))
    Start-Octopus
    $foreignProcess = @(Get-OctopusProcess)[0]
    $script:OctopusDirectory = $fixtureRuntime

    Invoke-OctopusMenuAction '2'
    Assert-True ((Get-OctopusAutostartState) -eq 'Enabled') 'Option 2 did not enable autostart.'
    $firstShortcut = [Convert]::ToBase64String([IO.File]::ReadAllBytes($shortcutPath))
    Invoke-OctopusMenuAction '2'
    Assert-True ([Convert]::ToBase64String([IO.File]::ReadAllBytes($shortcutPath)) -eq $firstShortcut) 'Repeated option 2 rewrote the shortcut.'
    Assert-True (@(Get-OctopusProcess).Count -eq 1) 'Repeated option 2 duplicated the process.'

    Invoke-OctopusMenuAction '3'
    Assert-True (@(Get-OctopusProcess).Count -eq 0) 'Option 3 left the process running.'
    Assert-True ((Get-OctopusAutostartState) -eq 'Enabled') 'Option 3 disabled autostart.'
    Assert-True (-not $foreignProcess.HasExited) 'Stopping one deployment affected another deployment.'
    Assert-True (@(Get-Content -LiteralPath (Join-Path $fixtureRuntime 'data\shutdown.txt')).Count -eq 1) 'Option 3 did not shut down gracefully.'

    # Run exactly the packaged shortcut command, from an unrelated working directory.
    $shell = New-Object -ComObject WScript.Shell
    $shortcut = $null
    try {
        $shortcut = $shell.CreateShortcut($shortcutPath)
        Assert-True ($shortcut.Arguments.Contains('windows\octopus-roam-start.ps1')) 'Shortcut does not use the packaged launcher.'
        Assert-True ($shortcut.Arguments.Contains('-RuntimeDirectory')) 'Shortcut omits the runtime directory.'
        $launcher = Start-Process -FilePath $shortcut.TargetPath -ArgumentList $shortcut.Arguments -WorkingDirectory $PSScriptRoot -WindowStyle Hidden -PassThru
        Assert-True ($launcher.WaitForExit(15000)) 'Packaged startup launcher timed out.'
        Assert-True ($launcher.ExitCode -eq 0) 'Packaged startup launcher failed.'
    } finally {
        if ($null -ne $shortcut) { [Runtime.InteropServices.Marshal]::FinalReleaseComObject($shortcut) | Out-Null }
        [Runtime.InteropServices.Marshal]::FinalReleaseComObject($shell) | Out-Null
    }
    Assert-True (@(Get-OctopusProcess).Count -eq 1) 'Saved startup shortcut did not launch the deployment.'
    Invoke-OctopusMenuAction '4'
    Assert-True (@(Get-OctopusProcess).Count -eq 0) 'Option 4 left the process running.'
    Assert-True ((Get-OctopusAutostartState) -eq 'Disabled') 'Option 4 left autostart enabled.'
    Assert-True (@(Get-Content -LiteralPath (Join-Path $fixtureRuntime 'data\shutdown.txt')).Count -eq 2) 'Option 4 did not shut down gracefully.'
    Assert-True ([IO.File]::ReadAllText((Join-Path $fixtureRuntime 'data\data.db')) -eq 'database-sentinel') 'Stopping changed application data.'
    Assert-True ([IO.File]::ReadAllText($configPath) -eq $fixtureConfig) 'Menu operations changed existing configuration.'
    Invoke-OctopusMenuAction '3'
    Invoke-OctopusMenuAction '4'

    [IO.File]::WriteAllText($configPath, '{')
    Assert-Fails { Invoke-OctopusMenuAction '1' } 'valid JSON'
    Assert-True ([IO.File]::ReadAllText($configPath) -eq '{') 'Invalid configuration was overwritten.'
    [IO.File]::WriteAllText($configPath, $fixtureConfig)
    [IO.File]::WriteAllText((Join-Path $fixtureRuntime 'exit-early'), '')
    Assert-Fails { Invoke-OctopusMenuAction '2' } 'exited during startup'
    Assert-True ((Get-OctopusAutostartState) -eq 'Disabled') 'An early-exiting application enabled autostart.'

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
    Assert-True ((Get-OctopusAutostartState) -eq 'Conflict') 'Foreign startup shortcut was treated as owned.'
    Assert-Fails { Invoke-OctopusMenuAction '2' }
    Assert-Fails { Invoke-OctopusMenuAction '4' } 'another deployment'
    Assert-True ([Convert]::ToBase64String([IO.File]::ReadAllBytes($shortcutPath)) -eq $foreignShortcut) 'Foreign startup shortcut was modified.'

    # Real menu rendering and EOF/exit handling; option 0 performs no service action.
    $menuInfo = New-Object Diagnostics.ProcessStartInfo
    $menuInfo.FileName = $shellPath
    $menuInfo.Arguments = '-NoProfile -ExecutionPolicy RemoteSigned -File "{0}" -RuntimeDirectory "{1}"' -f (Join-Path $fixtureRuntime 'windows\octopus-roam-menu.ps1'), $fixtureRuntime
    $menuInfo.UseShellExecute = $false
    $menuInfo.CreateNoWindow = $true
    $menuInfo.RedirectStandardInput = $true
    $menuInfo.RedirectStandardOutput = $true
    $menuInfo.RedirectStandardError = $true
    $menuInfo.StandardOutputEncoding = [Text.Encoding]::UTF8
    $menuProcess = [Diagnostics.Process]::Start($menuInfo)
    $menuProcess.StandardInput.WriteLine('invalid')
    $menuProcess.StandardInput.WriteLine('0')
    $menuProcess.StandardInput.Close()
    Assert-True ($menuProcess.WaitForExit(15000)) 'Menu did not exit after option 0.'
    $menuOutput = $menuProcess.StandardOutput.ReadToEnd()
    Assert-True ($menuProcess.ExitCode -eq 0) ('Menu process failed: ' + $menuProcess.StandardError.ReadToEnd())
    foreach ($number in @('1.', '2.', '3.', '4.', '0.')) { Assert-True ($menuOutput.Contains($number)) "Menu omits option $number" }
    Assert-True (@(Get-OctopusProcess).Count -eq 0) 'Opening or exiting the menu started a service.'

    # The .cmd wrapper must quote a literal extraction path and preserve exit codes.
    $wrapperRuntime = Join-Path $fixtureRoot ($fixtureName + ' wrapper')
    [IO.Directory]::CreateDirectory((Join-Path $wrapperRuntime 'windows')) | Out-Null
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'octopus-roam.cmd') -Destination $wrapperRuntime
    $wrapperFixture = @'
param([string]$RuntimeDirectory)
[IO.File]::WriteAllText((Join-Path $PSScriptRoot 'runtime.txt'), [IO.Path]::GetFullPath($RuntimeDirectory))
exit 17
'@
    [IO.File]::WriteAllText((Join-Path $wrapperRuntime 'windows\octopus-roam-menu.ps1'), $wrapperFixture)
    $wrapperCommand = '""' + (Join-Path $wrapperRuntime 'octopus-roam.cmd') + '" <NUL >NUL 2>&1"'
    $wrapper = Start-Process -FilePath (Join-Path ([Environment]::GetFolderPath('System')) 'cmd.exe') -ArgumentList @('/d', '/v:off', '/s', '/c', $wrapperCommand) -WindowStyle Hidden -PassThru
    Assert-True ($wrapper.WaitForExit(15000)) 'Double-click wrapper timed out.'
    Assert-True ($wrapper.ExitCode -eq 17) 'Double-click wrapper lost the menu exit code.'
    Assert-True ([IO.File]::ReadAllText((Join-Path $wrapperRuntime 'windows\runtime.txt')).TrimEnd('\') -eq $wrapperRuntime) 'Double-click wrapper changed the literal runtime path.'
    Write-Host "PASS: four menu actions, first-use config, occupied port, environment overrides, graceful shutdown, data preservation, process ownership, packaged autostart, Unicode paths, and double-click entry. Fixture: $fixtureRoot"
} finally {
    foreach ($runtime in @($fixtureRuntime, $foreignRuntime)) {
        $script:OctopusDirectory = $runtime
        if (@(Get-OctopusProcess).Count -gt 0) {
            try { Stop-Octopus -TimeoutSeconds 10 } catch { Write-Warning "Synthetic fixture cleanup: $($_.Exception.Message)" }
        }
    }
    $env:OCTOPUS_ROAM_SERVER_HOST = $savedHostOverride
    $env:OCTOPUS_ROAM_SERVER_PORT = $savedPortOverride
}
