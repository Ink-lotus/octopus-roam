# Build/rehearse the opt-in integration driver against a frozen candidate.
# This script never reads a real profile or connects to MySQL/PostgreSQL.
# The resulting executable requires a separate explicit --connect invocation.
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Candidate,
    [Parameter(Mandatory = $true)][string]$OutputRoot,
    [string]$ExpectedVersion,
    [string]$ExpectedCommit,
    [string]$ArtifactSha256,
    [string]$MaterialsSha256,
    [string]$GoExecutable = 'go',
    [string]$PythonExecutable = 'python',
    [string]$ModuleCache,
    [switch]$ValidateOnly,
    [switch]$SkipRehearsal
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
if (Test-Path -LiteralPath (Join-Path $candidatePath 'build.json')) {
    throw 'Use release_acceptance.py prepare to verify and derive the Actions artifact layout first.'
}
$metadata = Get-Content -LiteralPath (Join-Path $candidatePath 'build-info.json') -Raw | ConvertFrom-Json
$derived = $null -ne $metadata.PSObject.Properties['role'] -and $metadata.role -eq 'derived-acceptance-layout'
$goPath = (Get-Command $GoExecutable -CommandType Application -ErrorAction Stop).Source
$goBin = Split-Path -Parent $goPath
$goRoot = Split-Path -Parent $goBin
$binding = $null
if ($derived) {
    if ($ExpectedVersion -notmatch '^v0\.1\.0(?:-rc\.[1-9][0-9]*)?$' -or
        $ExpectedCommit -cnotmatch '^[0-9a-f]{40}$' -or
        $ArtifactSha256 -cnotmatch '^[0-9a-f]{64}$' -or $MaterialsSha256 -cnotmatch '^[0-9a-f]{64}$') {
        throw 'An artifact-derived candidate requires the explicitly reviewed version, commit, artifact and material hashes.'
    }
    $bindingEnvironment = @{ GOTOOLCHAIN = 'local'; GOPROXY = 'off'; GOFLAGS = '-mod=readonly';
        GOROOT = $goRoot; PATH = $goBin + [IO.Path]::PathSeparator + $env:PATH }
    $bindingSaved = @{}
    try {
        foreach ($key in $bindingEnvironment.Keys) {
            $bindingSaved[$key] = [Environment]::GetEnvironmentVariable($key, 'Process')
            [Environment]::SetEnvironmentVariable($key, $bindingEnvironment[$key], 'Process')
        }
        Push-Location -LiteralPath $repository
        try {
            $verified = & $PythonExecutable -B (Join-Path $PSScriptRoot 'release_acceptance.py') verify-layout `
                --candidate $candidatePath --version $ExpectedVersion --commit $ExpectedCommit `
                --artifact-sha $ArtifactSha256 --materials-sha $MaterialsSha256
            if ($LASTEXITCODE -ne 0) { throw 'Artifact-derived candidate verification failed.' }
            $binding = ($verified -join "`n") | ConvertFrom-Json
            if ($binding.status -ne 'layout_verified') { throw 'Missing artifact verification result.' }
        } finally { Pop-Location }
    } finally {
        foreach ($key in $bindingSaved.Keys) { [Environment]::SetEnvironmentVariable($key, $bindingSaved[$key], 'Process') }
    }
} elseif ($ExpectedVersion -or $ExpectedCommit -or $ArtifactSha256 -or $MaterialsSha256) {
    throw 'A legacy local candidate cannot satisfy the new artifact identity contract.'
}
$seen = [Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
$files = foreach ($line in Get-Content -LiteralPath $manifest) {
    if ($line -notmatch '^([0-9a-f]{64})  (.+)$') { throw 'Invalid frozen input manifest.' }
    $hash, $relative = $Matches[1], $Matches[2]
    if (-not $seen.Add($relative)) { throw 'Duplicate frozen input.' }
    $absolute = [IO.Path]::GetFullPath((Join-Path $source $relative))
    if (-not $absolute.StartsWith($source + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) { throw 'Unsafe frozen input path.' }
    if ((Get-FileHash -LiteralPath $absolute -Algorithm SHA256).Hash.ToLowerInvariant() -ne $hash) { throw 'Frozen input mismatch.' }
    [pscustomobject]@{ path = $relative; sha256 = $hash; absolute = $absolute }
}
$productExecutable = Join-Path $candidatePath 'bin/octopus-roam-windows-amd64.exe'
$productHash = (Get-FileHash -LiteralPath $productExecutable -Algorithm SHA256).Hash.ToLowerInvariant()
$productEntry = @(Get-Content -LiteralPath (Join-Path $candidatePath 'file-sha256sums.txt') | Where-Object { $_ -match '^([0-9a-f]{64})  bin/octopus-roam-windows-amd64.exe$' })
if ($productEntry.Count -ne 1 -or $productEntry[0].Substring(0,64) -ne $productHash) { throw 'Frozen product executable mismatch.' }
$stopScript = Join-Path $(if ($derived) { $source } else { $repository }) 'scripts/windows/send-stop-signal.ps1'
$stopHash = (Get-FileHash -LiteralPath $stopScript -Algorithm SHA256).Hash.ToLowerInvariant()
$stopEntry = @($files | Where-Object { $_.path -eq 'scripts/windows/send-stop-signal.ps1' })
if ($stopEntry.Count -ne 1 -or $stopEntry[0].sha256 -ne $stopHash) { throw 'Normal-stop helper differs from frozen source.' }
if ($ValidateOnly) {
    [pscustomobject]@{ status = 'inputs_verified'; version = $metadata.version; artifact_bound = $derived;
        artifact = $binding; source_files = $files.Count; product_sha256 = $productHash;
        driver_built = $false; product_started = $false; real_database_connected = $false } | ConvertTo-Json -Depth 6
    return
}
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
$driverDirectory = if ($derived) { Join-Path $source 'scripts/acceptance/database' } else { Join-Path $PSScriptRoot 'acceptance/database' }
$driverFiles = @(Get-ChildItem -LiteralPath $driverDirectory -File -Filter '*.go')
if ($driverFiles.Count -eq 0) { throw 'Frozen acceptance driver source is missing.' }
$driverInputs = foreach ($file in $driverFiles) {
    Copy-Item -LiteralPath $file.FullName -Destination (Join-Path $driverSource $file.Name) -ErrorAction Stop
    [pscustomobject]@{ path = 'scripts/acceptance/database/' + $file.Name; sha256 = (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash.ToLowerInvariant() }
}
$environment = @{
    GOTOOLCHAIN = 'local'; GOPROXY = 'off'; GOFLAGS = '-mod=readonly'; GOOS = 'windows'; GOARCH = 'amd64'; GOARM = '';
    GOROOT = $goRoot; PATH = $goBin + [IO.Path]::PathSeparator + $env:PATH;
    CGO_ENABLED = '0'; GOCACHE = (Join-Path $outputPath 'go-cache');
    GOMODCACHE = $(if ($ModuleCache) { [IO.Path]::GetFullPath($ModuleCache) } else { Join-Path $repository '.tmp-go-mod' })
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
        $actualGo = & $goPath env GOVERSION
        if ($LASTEXITCODE -ne 0 -or ($derived -and $actualGo -ne 'go1.27.3')) { throw 'The bound acceptance driver requires Go 1.27.3.' }
        & $goPath test -mod=readonly -tags=jsoniter -count=1 ./scripts/acceptance/database *> (Join-Path $outputPath 'driver-tests.log')
        if ($LASTEXITCODE -ne 0) { throw 'Driver tests failed; see driver-tests.log.' }
        & $goPath build -mod=readonly -trimpath -tags=jsoniter -o $executable ./scripts/acceptance/database *> (Join-Path $outputPath 'driver-build.log')
        if ($LASTEXITCODE -ne 0) { throw 'Driver build failed; see driver-build.log.' }
    } finally { Pop-Location }
} finally {
    foreach ($key in $saved.Keys) { [Environment]::SetEnvironmentVariable($key, $saved[$key], 'Process') }
}
$rehearsal = 'not_run'
if (-not $SkipRehearsal) {
    $rehearsalOutput = & $executable --self-test --output (Join-Path $outputPath 'sqlite-rehearsal') --executable $productExecutable --executable-sha256 $productHash --stop-script $stopScript --expected-version $metadata.version
    $rehearsalCode = $LASTEXITCODE
    $rehearsalOutput | Set-Content -LiteralPath (Join-Path $outputPath 'sqlite-rehearsal-summary.json') -Encoding utf8NoBOM
    if ($rehearsalCode -ne 0) { throw 'Isolated driver rehearsal failed; see sqlite-rehearsal-summary.json.' }
    $rehearsal = 'passed'
}
$build = [ordered]@{
    role = 'database-integration-driver'; candidate_version = $metadata.version;
    candidate_metadata_sha256 = (Get-FileHash -LiteralPath (Join-Path $candidatePath 'build-info.json') -Algorithm SHA256).Hash.ToLowerInvariant();
    candidate_input_manifest_sha256 = (Get-FileHash -LiteralPath $manifest -Algorithm SHA256).Hash.ToLowerInvariant();
    frozen_inputs_verified = $files.Count; linked_inputs = @($linked | Select-Object path,sha256);
    driver_inputs = @($driverInputs); driver_sha256 = (Get-FileHash -LiteralPath $executable -Algorithm SHA256).Hash.ToLowerInvariant();
    tests = 'passed'; sqlite_rehearsal = $rehearsal; real_database_connected = $false;
    actual_go_version = $actualGo; artifact_binding = $binding; product_started = -not $SkipRehearsal;
    product_executable = $productExecutable; product_sha256 = $productHash; stop_script = $stopScript; stop_script_sha256 = $stopHash;
    runtime_boundary = 'frozen database/business functions plus original Windows executable API and two normal starts/stops; named pipe configuration stays in memory; no browser or Unix runtime test';
    created_at = (Get-Date).ToString('o')
}
$build | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath (Join-Path $outputPath 'driver-build-info.json') -Encoding utf8NoBOM
[pscustomobject]@{ status = 'prepared'; frozen_inputs_verified = $files.Count; linked_inputs = $linked.Count; output = $outputPath; driver_sha256 = $build.driver_sha256 } | ConvertTo-Json -Compress
