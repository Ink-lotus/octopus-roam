# Isolated console fixtures only; never points the updater at the real runtime.
param([ValidateSet('Flat', 'Packaged')][string]$Layout = 'Packaged')

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$fixtureRoot = Join-Path ([IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..\build'))) ('update-check-' + [guid]::NewGuid().ToString('N'))
$fixtureRuntime = Join-Path $fixtureRoot "runtime [test] & O'Brien"
$fixturePrepared = Join-Path $fixtureRoot 'prepared'
[IO.Directory]::CreateDirectory((Join-Path $fixtureRuntime 'data')) | Out-Null
[IO.Directory]::CreateDirectory($fixturePrepared) | Out-Null

function Assert-True([bool]$Condition, [string]$Message) {
    if (-not $Condition) { throw $Message }
}

$fakeSource = @'
using System;
using System.IO;
using System.Threading;
public static class UPDATE_CLASS {
    public static void Main() {
        var done = new ManualResetEvent(false);
        Console.CancelKeyPress += (sender, e) => {
            e.Cancel = true;
            File.WriteAllText("data/last-shutdown.txt", "graceful");
            done.Set();
        };
        File.WriteAllText("started-version.txt", "UPDATE_VERSION");
        Console.WriteLine("Update test fixture ready.");
        done.WaitOne(90000);
    }
}
'@
$oldFixture = Join-Path $fixtureRoot 'old.exe'
$newFixture = Join-Path $fixtureRoot 'new.exe'
Add-Type -TypeDefinition $fakeSource.Replace('UPDATE_CLASS', 'OldUpdateFixture').Replace('UPDATE_VERSION', 'old') -OutputAssembly $oldFixture -OutputType ConsoleApplication
Add-Type -TypeDefinition $fakeSource.Replace('UPDATE_CLASS', 'NewUpdateFixture').Replace('UPDATE_VERSION', 'new') -OutputAssembly $newFixture -OutputType ConsoleApplication
Copy-Item -LiteralPath $oldFixture -Destination (Join-Path $fixtureRuntime 'octopus-roam.exe')
Copy-Item -LiteralPath $newFixture -Destination (Join-Path $fixturePrepared 'octopus-roam.exe')
$launcherFiles = @('octopus-roam-menu.ps1', 'octopus-roam-start.ps1', 'octopus-roam-autostart.ps1', 'send-stop-signal.ps1')
[IO.Directory]::CreateDirectory((Join-Path $fixturePrepared 'windows')) | Out-Null
Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'octopus-roam.cmd') -Destination $fixturePrepared
foreach ($file in $launcherFiles) { Copy-Item -LiteralPath (Join-Path $PSScriptRoot $file) -Destination (Join-Path $fixturePrepared 'windows') }
if ($Layout -eq 'Packaged') {
    [IO.Directory]::CreateDirectory((Join-Path $fixtureRuntime 'windows')) | Out-Null
    foreach ($file in $launcherFiles) { Copy-Item -LiteralPath (Join-Path $PSScriptRoot $file) -Destination (Join-Path $fixtureRuntime 'windows') }
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'octopus-roam.cmd') -Destination $fixtureRuntime
    Add-Content -LiteralPath (Join-Path $fixtureRuntime 'windows\octopus-roam-menu.ps1') -Value '# Previous launcher fixture.' -Encoding UTF8
    $oldMenuHash = (Get-FileHash -LiteralPath (Join-Path $fixtureRuntime 'windows\octopus-roam-menu.ps1')).Hash
    $initialLauncher = Join-Path $fixtureRuntime 'windows\octopus-roam-start.ps1'
} else {
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'octopus-roam-start.ps1') -Destination $fixtureRuntime
    $initialLauncher = Join-Path $fixtureRuntime 'octopus-roam-start.ps1'
}
$legacyEntries = @('enable-autostart.cmd', 'disable-autostart.cmd')
$legacyContent = '@echo off' + [Environment]::NewLine + 'rem Previous launcher fixture.' + [Environment]::NewLine
foreach ($entry in $legacyEntries) { [IO.File]::WriteAllText((Join-Path $fixtureRuntime $entry), $legacyContent) }
$portProbe = New-Object Net.Sockets.TcpListener([Net.IPAddress]::Loopback, 0)
$portProbe.Start()
$fixturePort = $portProbe.LocalEndpoint.Port
$portProbe.Stop()
[IO.File]::WriteAllText((Join-Path $fixtureRuntime 'data\config.json'), ('{"server":{"host":"127.0.0.1","port":' + $fixturePort + '}}'))
[IO.File]::WriteAllText((Join-Path $fixtureRuntime 'data\data.db'), 'database-sentinel')
[IO.File]::WriteAllText((Join-Path $fixtureRuntime 'data\data.db-wal'), 'wal-sentinel')
$oldHash = (Get-FileHash -LiteralPath $oldFixture -Algorithm SHA256).Hash
$newHash = (Get-FileHash -LiteralPath $newFixture -Algorithm SHA256).Hash
@{ version = 'fixture-new'; sha256 = $newHash } | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $fixturePrepared 'build-info.json') -Encoding UTF8
$shellPath = Join-Path ([Environment]::GetFolderPath('System')) 'WindowsPowerShell\v1.0\powershell.exe'
$updaterPath = Join-Path $PSScriptRoot 'update-local.ps1'

& $shellPath -NoProfile -ExecutionPolicy RemoteSigned -File $updaterPath -RuntimeDirectory $fixtureRuntime -PreparedDirectory $fixturePrepared
Assert-True ($LASTEXITCODE -eq 0) 'Build-only validation failed.'
Assert-True ((Get-FileHash -LiteralPath (Join-Path $fixtureRuntime 'octopus-roam.exe')).Hash -eq $oldHash) 'Build-only mode replaced the runtime.'
foreach ($entry in $legacyEntries) {
    Assert-True ([IO.File]::ReadAllText((Join-Path $fixtureRuntime $entry)) -eq $legacyContent) 'Build-only mode changed an existing launcher.'
}

& $shellPath -NoProfile -ExecutionPolicy RemoteSigned -File $initialLauncher -RuntimeDirectory $fixtureRuntime
Assert-True ($LASTEXITCODE -eq 0) 'Fixture launch failed.'
& $shellPath -NoProfile -ExecutionPolicy RemoteSigned -File $updaterPath -RuntimeDirectory $fixtureRuntime -PreparedDirectory $fixturePrepared -Apply
Assert-True ($LASTEXITCODE -eq 0) 'Fixture update failed.'
Assert-True ((Get-FileHash -LiteralPath (Join-Path $fixtureRuntime 'octopus-roam.exe')).Hash -eq $newHash) 'Executable was not replaced.'
Assert-True ((Get-Content -LiteralPath (Join-Path $fixtureRuntime 'started-version.txt') -Raw) -eq 'new') 'New executable was not started.'
$backup = @(Get-ChildItem -LiteralPath (Join-Path $fixtureRuntime 'backups') -Directory)
Assert-True ($backup.Count -eq 1) 'Expected one backup.'
Assert-True ((Get-FileHash -LiteralPath (Join-Path $backup[0].FullName 'octopus-roam.exe')).Hash -eq $oldHash) 'Backup does not contain the old executable.'
foreach ($relativePath in @('octopus-roam.cmd') + @($launcherFiles | ForEach-Object { "windows\$_" })) {
    Assert-True ((Get-FileHash -LiteralPath (Join-Path $fixtureRuntime $relativePath)).Hash -eq (Get-FileHash -LiteralPath (Join-Path $fixturePrepared $relativePath)).Hash) "Launcher was not updated: $relativePath"
}
foreach ($entry in $legacyEntries) {
    Assert-True (-not (Test-Path -LiteralPath (Join-Path $fixtureRuntime $entry))) "Superseded launcher was not removed: $entry"
    Assert-True ([IO.File]::ReadAllText((Join-Path $backup[0].FullName $entry)) -eq $legacyContent) "Superseded launcher was not backed up: $entry"
}
if ($Layout -eq 'Packaged') {
    Assert-True ((Get-FileHash -LiteralPath (Join-Path $backup[0].FullName 'windows\octopus-roam-menu.ps1')).Hash -eq $oldMenuHash) 'Previous launcher was not backed up.'
}
foreach ($directory in @($fixtureRuntime, $backup[0].FullName)) {
    Assert-True ((Get-Content -LiteralPath (Join-Path $directory 'data\data.db') -Raw) -eq 'database-sentinel') 'Database content changed.'
    Assert-True ((Get-Content -LiteralPath (Join-Path $directory 'data\data.db-wal') -Raw) -eq 'wal-sentinel') 'WAL file was not preserved.'
    Assert-True ((Get-Content -LiteralPath (Join-Path $directory 'data\last-shutdown.txt') -Raw) -eq 'graceful') 'Backup occurred before graceful shutdown.'
}

# Shut down only the synthetic process created by this test.
$fixtureExe = Join-Path $fixtureRuntime 'octopus-roam.exe'
$fixtureProcess = @(Get-Process -Name octopus-roam | Where-Object { $_.Path -eq $fixtureExe })
Assert-True ($fixtureProcess.Count -eq 1) 'Expected exactly one restarted fixture.'
$stopArguments = '-NoProfile -ExecutionPolicy RemoteSigned -File "{0}" -TargetProcessId {1} -ExpectedExecutable "{2}" -ResultPath "{3}"' -f (Join-Path $PSScriptRoot 'send-stop-signal.ps1'), $fixtureProcess[0].Id, $fixtureExe, (Join-Path $fixtureRoot 'cleanup-stop.json')
$stopper = Start-Process -FilePath $shellPath -ArgumentList $stopArguments -WindowStyle Hidden -PassThru
Assert-True ($stopper.WaitForExit(15000)) 'Fixture stop helper timed out.'
Assert-True ($fixtureProcess[0].WaitForExit(10000)) 'Fixture did not stop gracefully.'
Write-Host "PASS: $Layout layout, build-only mode, graceful shutdown, complete data backup, executable and launcher replacement, legacy entry cleanup, restart. Fixture: $fixtureRoot"
