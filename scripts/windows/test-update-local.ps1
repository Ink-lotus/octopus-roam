# Isolated console fixtures only; never points the updater at the real runtime.
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
Copy-Item -LiteralPath $oldFixture -Destination (Join-Path $fixtureRuntime 'octopus.exe')
Copy-Item -LiteralPath $newFixture -Destination (Join-Path $fixturePrepared 'octopus.exe')
Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'octopus-start.ps1') -Destination $fixtureRuntime
[IO.File]::WriteAllText((Join-Path $fixtureRuntime 'data\config.json'), '{}')
[IO.File]::WriteAllText((Join-Path $fixtureRuntime 'data\data.db'), 'database-sentinel')
[IO.File]::WriteAllText((Join-Path $fixtureRuntime 'data\data.db-wal'), 'wal-sentinel')
$oldHash = (Get-FileHash -LiteralPath $oldFixture -Algorithm SHA256).Hash
$newHash = (Get-FileHash -LiteralPath $newFixture -Algorithm SHA256).Hash
@{ version = 'fixture-new'; sha256 = $newHash } | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $fixturePrepared 'build-info.json') -Encoding UTF8
$shellPath = Join-Path ([Environment]::GetFolderPath('System')) 'WindowsPowerShell\v1.0\powershell.exe'
$updaterPath = Join-Path $PSScriptRoot 'update-local.ps1'

& $shellPath -NoProfile -ExecutionPolicy RemoteSigned -File $updaterPath -RuntimeDirectory $fixtureRuntime -PreparedDirectory $fixturePrepared
Assert-True ($LASTEXITCODE -eq 0) 'Build-only validation failed.'
Assert-True ((Get-FileHash -LiteralPath (Join-Path $fixtureRuntime 'octopus.exe')).Hash -eq $oldHash) 'Build-only mode replaced the runtime.'

& $shellPath -NoProfile -ExecutionPolicy RemoteSigned -File (Join-Path $fixtureRuntime 'octopus-start.ps1')
Assert-True ($LASTEXITCODE -eq 0) 'Fixture launch failed.'
& $shellPath -NoProfile -ExecutionPolicy RemoteSigned -File $updaterPath -RuntimeDirectory $fixtureRuntime -PreparedDirectory $fixturePrepared -Apply
Assert-True ($LASTEXITCODE -eq 0) 'Fixture update failed.'
Assert-True ((Get-FileHash -LiteralPath (Join-Path $fixtureRuntime 'octopus.exe')).Hash -eq $newHash) 'Executable was not replaced.'
Assert-True ((Get-Content -LiteralPath (Join-Path $fixtureRuntime 'started-version.txt') -Raw) -eq 'new') 'New executable was not started.'
$backup = @(Get-ChildItem -LiteralPath (Join-Path $fixtureRuntime 'backups') -Directory)
Assert-True ($backup.Count -eq 1) 'Expected one backup.'
Assert-True ((Get-FileHash -LiteralPath (Join-Path $backup[0].FullName 'octopus.exe')).Hash -eq $oldHash) 'Backup does not contain the old executable.'
foreach ($directory in @($fixtureRuntime, $backup[0].FullName)) {
    Assert-True ((Get-Content -LiteralPath (Join-Path $directory 'data\data.db') -Raw) -eq 'database-sentinel') 'Database content changed.'
    Assert-True ((Get-Content -LiteralPath (Join-Path $directory 'data\data.db-wal') -Raw) -eq 'wal-sentinel') 'WAL file was not preserved.'
    Assert-True ((Get-Content -LiteralPath (Join-Path $directory 'data\last-shutdown.txt') -Raw) -eq 'graceful') 'Backup occurred before graceful shutdown.'
}

# Shut down only the synthetic process created by this test.
$fixtureExe = Join-Path $fixtureRuntime 'octopus.exe'
$fixtureProcess = @(Get-Process -Name octopus | Where-Object { $_.Path -eq $fixtureExe })
Assert-True ($fixtureProcess.Count -eq 1) 'Expected exactly one restarted fixture.'
$stopArguments = '-NoProfile -ExecutionPolicy RemoteSigned -File "{0}" -TargetProcessId {1} -ExpectedExecutable "{2}" -ResultPath "{3}"' -f (Join-Path $PSScriptRoot 'send-stop-signal.ps1'), $fixtureProcess[0].Id, $fixtureExe, (Join-Path $fixtureRoot 'cleanup-stop.json')
$stopper = Start-Process -FilePath $shellPath -ArgumentList $stopArguments -WindowStyle Hidden -PassThru
Assert-True ($stopper.WaitForExit(15000)) 'Fixture stop helper timed out.'
Assert-True ($fixtureProcess[0].WaitForExit(10000)) 'Fixture did not stop gracefully.'
Write-Host "PASS: build-only mode, graceful shutdown, complete data backup, executable replacement, restart. Fixture: $fixtureRoot"
