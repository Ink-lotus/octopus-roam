# Build/rehearse the opt-in integration driver against a frozen candidate.
# This script never reads a real profile or connects to MySQL/PostgreSQL.
# The resulting executable requires a separate explicit --connect invocation.
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Candidate,
    [Parameter(Mandatory = $true)][string]$OutputRoot
)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$repository = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$candidatePath = [IO.Path]::GetFullPath($Candidate)
$outputPath = [IO.Path]::GetFullPath($OutputRoot)
$releaseRoot = Join-Path $repository 'build/release-local'
if (-not $outputPath.StartsWith($releaseRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
    throw 'OutputRoot must be under build/release-local.'
}
if ($outputPath.Equals($candidatePath, [StringComparison]::OrdinalIgnoreCase) -or
    $outputPath.StartsWith($candidatePath + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
    throw 'A frozen candidate cannot be used as OutputRoot.'
}
if (Test-Path -LiteralPath $outputPath) { throw 'OutputRoot must be new.' }
$source = Join-Path $candidatePath 'source'
$manifest = Join-Path $candidatePath 'source-input-sha256sums.txt'
$metadata = Get-Content -LiteralPath (Join-Path $candidatePath 'build-info.json') -Raw | ConvertFrom-Json
$files = foreach ($line in Get-Content -LiteralPath $manifest) {
    if ($line -notmatch '^([0-9a-f]{64})  (.+)$') { throw 'Invalid frozen input manifest.' }
    $hash, $relative = $Matches[1], $Matches[2]
    $absolute = [IO.Path]::GetFullPath((Join-Path $source $relative))
    if (-not $absolute.StartsWith($source + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) { throw 'Unsafe frozen input path.' }
    if ((Get-FileHash -LiteralPath $absolute -Algorithm SHA256).Hash.ToLowerInvariant() -ne $hash) { throw 'Frozen input mismatch.' }
    [pscustomobject]@{ path = $relative; sha256 = $hash; absolute = $absolute }
}
$productExecutable = Join-Path $candidatePath 'bin/octopus-roam-windows-amd64.exe'
$productHash = (Get-FileHash -LiteralPath $productExecutable -Algorithm SHA256).Hash.ToLowerInvariant()
$productEntry = @(Get-Content -LiteralPath (Join-Path $candidatePath 'file-sha256sums.txt') | Where-Object { $_ -match '^([0-9a-f]{64})  bin/octopus-roam-windows-amd64.exe$' })
if ($productEntry.Count -ne 1 -or $productEntry[0].Substring(0,64) -ne $productHash) { throw 'Frozen product executable mismatch.' }
$stopScript = Join-Path $repository 'scripts/windows/send-stop-signal.ps1'
$stopHash = (Get-FileHash -LiteralPath $stopScript -Algorithm SHA256).Hash.ToLowerInvariant()
$stopEntry = @($files | Where-Object { $_.path -eq 'scripts/windows/send-stop-signal.ps1' })
if ($stopEntry.Count -ne 1 -or $stopEntry[0].sha256 -ne $stopHash) { throw 'Normal-stop helper differs from frozen source.' }
New-Item -ItemType Directory -Path $outputPath -ErrorAction Stop | Out-Null
$toolSource = Join-Path $outputPath 'source'
$driverSource = Join-Path $toolSource 'scripts/acceptance/database'
New-Item -ItemType Directory -Path $driverSource -Force | Out-Null
# Only the existing database/business packages and module files are needed;
# no working-tree product code, frontend build, dependency or CI change.
$linked = @($files | Where-Object { $_.path -in @('go.mod','go.sum') -or $_.path.StartsWith('internal/') })
foreach ($file in $linked) {
    $destination = Join-Path $toolSource $file.path
    $null = New-Item -ItemType Directory -Path ([IO.Path]::GetDirectoryName($destination)) -Force
    Copy-Item -LiteralPath $file.absolute -Destination $destination -ErrorAction Stop
}
$driverFiles = @(Get-ChildItem -LiteralPath (Join-Path $PSScriptRoot 'acceptance/database') -File -Filter '*.go')
$driverInputs = foreach ($file in $driverFiles) {
    Copy-Item -LiteralPath $file.FullName -Destination (Join-Path $driverSource $file.Name) -ErrorAction Stop
    [pscustomobject]@{ path = 'scripts/acceptance/database/' + $file.Name; sha256 = (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash.ToLowerInvariant() }
}
$environment = @{
    GOTOOLCHAIN = 'local'; GOPROXY = 'off'; GOFLAGS = ''; GOOS = 'windows'; GOARCH = 'amd64'; GOARM = '';
    CGO_ENABLED = '0'; GOCACHE = (Join-Path $repository '.tmp-go-cache'); GOMODCACHE = (Join-Path $repository '.tmp-go-mod')
}
$saved = @{}
$executable = Join-Path $outputPath 'database-acceptance.exe'
try {
    foreach ($key in $environment.Keys) {
        $saved[$key] = [Environment]::GetEnvironmentVariable($key, 'Process')
        [Environment]::SetEnvironmentVariable($key, $environment[$key], 'Process')
    }
    Push-Location -LiteralPath $toolSource
    try {
        & go test -mod=readonly -tags=jsoniter -count=1 ./scripts/acceptance/database *> (Join-Path $outputPath 'driver-tests.log')
        if ($LASTEXITCODE -ne 0) { throw 'Driver tests failed; see driver-tests.log.' }
        & go build -mod=readonly -trimpath -tags=jsoniter -o $executable ./scripts/acceptance/database *> (Join-Path $outputPath 'driver-build.log')
        if ($LASTEXITCODE -ne 0) { throw 'Driver build failed; see driver-build.log.' }
    } finally { Pop-Location }
} finally {
    foreach ($key in $saved.Keys) { [Environment]::SetEnvironmentVariable($key, $saved[$key], 'Process') }
}
$rehearsalOutput = & $executable --self-test --output (Join-Path $outputPath 'sqlite-rehearsal') --executable $productExecutable --executable-sha256 $productHash --stop-script $stopScript --expected-version $metadata.version
$rehearsalCode = $LASTEXITCODE
$rehearsalOutput | Set-Content -LiteralPath (Join-Path $outputPath 'sqlite-rehearsal-summary.json') -Encoding utf8NoBOM
if ($rehearsalCode -ne 0) { throw 'Isolated driver rehearsal failed; see sqlite-rehearsal-summary.json.' }
$build = [ordered]@{
    role = 'database-integration-driver'; candidate_version = $metadata.version;
    candidate_metadata_sha256 = (Get-FileHash -LiteralPath (Join-Path $candidatePath 'build-info.json') -Algorithm SHA256).Hash.ToLowerInvariant();
    candidate_input_manifest_sha256 = (Get-FileHash -LiteralPath $manifest -Algorithm SHA256).Hash.ToLowerInvariant();
    frozen_inputs_verified = $files.Count; linked_inputs = @($linked | Select-Object path,sha256);
    driver_inputs = @($driverInputs); driver_sha256 = (Get-FileHash -LiteralPath $executable -Algorithm SHA256).Hash.ToLowerInvariant();
    tests = 'passed'; sqlite_rehearsal = 'passed'; real_database_connected = $false;
    product_executable = $productExecutable; product_sha256 = $productHash; stop_script = $stopScript; stop_script_sha256 = $stopHash;
    runtime_boundary = 'frozen database/business functions plus original Windows executable API and two normal starts/stops; named pipe configuration stays in memory; no browser or Unix runtime test';
    created_at = (Get-Date).ToString('o')
}
$build | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath (Join-Path $outputPath 'driver-build-info.json') -Encoding utf8NoBOM
[pscustomobject]@{ status = 'prepared'; frozen_inputs_verified = $files.Count; linked_inputs = $linked.Count; output = $outputPath; driver_sha256 = $build.driver_sha256 } | ConvertTo-Json -Compress
