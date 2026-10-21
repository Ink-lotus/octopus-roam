# PowerShell 5.1 compatible offline build helper for the first local release round.
param(
    [string]$OutputRoot,
    [string]$Version = ('v0.1.0-local.' + (Get-Date -Format 'yyyyMMddHHmmss')),
    [string[]]$Target = @('windows/amd64', 'linux/amd64', 'linux/arm64', 'linux/arm', 'linux/386', 'darwin/amd64', 'darwin/arm64'),
    [string]$LicenseReportPath,
    [string]$VerificationScript
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$sourceRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
if (-not $LicenseReportPath) { $LicenseReportPath = Join-Path $sourceRoot 'build\THIRD_PARTY_LICENSES.csv' }
if (-not $OutputRoot) {
    $OutputRoot = Join-Path $sourceRoot ('build\release-local\' + (Get-Date -Format 'yyyyMMdd-HHmmss-fff') + '-' + [guid]::NewGuid().ToString('N').Substring(0, 8))
}
$OutputRoot = [IO.Path]::GetFullPath($OutputRoot)
if (Test-Path -LiteralPath $OutputRoot) {
    $existing = @(Get-ChildItem -LiteralPath $OutputRoot -Force -ErrorAction Stop)
    if ($existing.Count -gt 0) { throw "OutputRoot already contains files: $OutputRoot" }
} else {
    New-Item -ItemType Directory -Force -Path $OutputRoot | Out-Null
}
$sourceCopy = Join-Path $OutputRoot 'source'
$binaryRoot = Join-Path $OutputRoot 'bin'
$archiveRoot = Join-Path $OutputRoot 'archives'
$packageRoot = Join-Path $OutputRoot 'packages'
$dockerContext = Join-Path $OutputRoot 'docker-context'
$sourceInputManifest = Join-Path $OutputRoot 'source-input-sha256sums.txt'
$archiveCheckRoot = Join-Path $OutputRoot 'checks\extracted'
$archiveStageRoot = Join-Path $OutputRoot 'checks\archive-stage'
$logRoot = Join-Path $OutputRoot 'checks\logs'
$logSequence = 0
$bsdTar = Join-Path $env:SystemRoot 'System32\tar.exe'

Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem

function Get-RelativePath([string]$BasePath, [string]$Path) {
    $base = [IO.Path]::GetFullPath($BasePath).TrimEnd('\', '/') + [IO.Path]::DirectorySeparatorChar
    $full = [IO.Path]::GetFullPath($Path)
    if (-not $full.StartsWith($base, [StringComparison]::OrdinalIgnoreCase)) {
        throw "路径不在预期目录内: $Path"
    }
    return $full.Substring($base.Length).Replace('\', '/')
}

function Test-SnapshotExcluded([string]$RelativePath) {
    $normalized = $RelativePath.Replace('\', '/')
    return $normalized -match '(^|/)node_modules(/|$)' -or
        $normalized -match '(^|/)static/out(/|$)' -or
        $normalized -match '(^|/)\.git(/|$)'
}

function Invoke-Checked([string]$FilePath, [string[]]$ArgumentList, [string]$WorkingDirectory) {
    Push-Location -LiteralPath $WorkingDirectory
    try {
        $script:logSequence++
        $logPath = Join-Path $logRoot ('{0:D2}-{1}.log' -f $script:logSequence, [IO.Path]::GetFileNameWithoutExtension($FilePath))
        New-Item -ItemType File -Path $logPath | Out-Null
        [ordered]@{ tool = $FilePath; arguments = $ArgumentList; working_directory = $WorkingDirectory; output = (Get-RelativePath $OutputRoot $logPath) } |
            ConvertTo-Json -Compress | Add-Content -LiteralPath (Join-Path $logRoot 'commands.jsonl') -Encoding UTF8
        $nativePreference = $ErrorActionPreference
        $ErrorActionPreference = 'Continue'
        try { & $FilePath @ArgumentList 2>&1 | Tee-Object -FilePath $logPath | Out-Host } finally { $ErrorActionPreference = $nativePreference }
        if ($LASTEXITCODE -ne 0) { throw "命令失败: $FilePath $($ArgumentList -join ' ')" }
    } finally {
        Pop-Location
    }
}

function Copy-SourceTree([string]$Source, [string]$Destination) {
    New-Item -ItemType Directory -Force -Path $Destination | Out-Null
    foreach ($item in @(Get-ChildItem -LiteralPath $Source -Force)) {
        $relative = Get-RelativePath $sourceRoot $item.FullName
        if (Test-SnapshotExcluded $relative) { continue }
        $target = Join-Path $sourceCopy ($relative.Replace('/', '\'))
        if ($item.PSIsContainer) {
            Copy-SourceTree $item.FullName $target
        } else {
            New-Item -ItemType Directory -Force -Path (Split-Path -Parent $target) | Out-Null
            Copy-Item -LiteralPath $item.FullName -Destination $target -Force
        }
    }
}

function Copy-SourceDirectory([string]$RelativePath) {
    $source = Join-Path $sourceRoot $RelativePath
    $destination = Join-Path $sourceCopy ($RelativePath.Replace('/', '\'))
    if ((Get-Item -LiteralPath $source).PSIsContainer) {
        Copy-SourceTree $source $destination
    } else {
        New-Item -ItemType Directory -Force -Path (Split-Path -Parent $destination) | Out-Null
        Copy-Item -LiteralPath $source -Destination $destination -Force
    }
}

function Get-SourceInputFiles {
    @(Get-ChildItem -LiteralPath $sourceCopy -Recurse -File -Force | Where-Object {
        $relative = Get-RelativePath $sourceCopy $_.FullName
        -not (Test-SnapshotExcluded $relative)
    } | Sort-Object FullName)
}

function New-ZipArchiveWithModes([string]$SourceDirectory, [string]$ArchivePath, [bool]$WindowsPackage) {
    if (Test-Path -LiteralPath $ArchivePath) { throw "归档已存在，拒绝覆盖: $ArchivePath" }
    $executable = if ($WindowsPackage) { 'octopus-roam.exe' } else { 'octopus-roam' }
    $stage = Join-Path $archiveStageRoot (([IO.Path]::GetFileNameWithoutExtension($ArchivePath)) + '.tar')
    # GNU tar supplies explicit Unix metadata; libarchive preserves the Unix
    # creator platform when converting to ZIP, which .NET on Windows does not.
    Invoke-Checked 'bash' @('-lc', 'exec /usr/bin/tar --force-local --format=ustar --owner=0 --group=0 --mode=0755 -cf "$1" -C "$2" "$3"', 'archive', $stage.Replace('\', '/'), $SourceDirectory.Replace('\', '/'), $executable) $sourceRoot
    $otherFiles = @(Get-ChildItem -LiteralPath $SourceDirectory -Recurse -File | ForEach-Object { Get-RelativePath $SourceDirectory $_.FullName } | Where-Object { $_ -ne $executable } | Sort-Object)
    Invoke-Checked 'bash' (@('-lc', 'stage="$1"; package="$2"; shift 2; exec /usr/bin/tar --force-local --owner=0 --group=0 --mode=0644 -rf "$stage" -C "$package" "$@"', 'archive', $stage.Replace('\', '/'), $SourceDirectory.Replace('\', '/')) + $otherFiles) $sourceRoot
    Invoke-Checked $bsdTar @('-c', '--format', 'zip', '-f', $ArchivePath, ('@' + $stage)) $sourceRoot
    Invoke-Checked 'bash' @('-lc', 'exec /usr/bin/unzip -Z -v "$1"', 'archive', $ArchivePath.Replace('\', '/')) $sourceRoot
}

function Test-ZipArchive([string]$ArchivePath, [string]$PackagePath, [string]$ExtractPath, [bool]$UnixPackage) {
    New-Item -ItemType Directory -Force -Path $ExtractPath | Out-Null
    $stream = [IO.File]::OpenRead($ArchivePath)
    $archive = [IO.Compression.ZipArchive]::new($stream, [IO.Compression.ZipArchiveMode]::Read, $false)
    try {
        $names = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::OrdinalIgnoreCase)
        $entries = @($archive.Entries)
        $executableName = if ($UnixPackage) { 'octopus-roam' } else { 'octopus-roam.exe' }
        $executableMode = $null
        foreach ($entry in $entries) {
            $name = $entry.FullName.Replace('\', '/')
            if ([string]::IsNullOrWhiteSpace($name) -or $name.StartsWith('/') -or $name.Contains(':') -or ($name -split '/') -contains '..') {
                throw "归档路径不安全: $name"
            }
            if (-not $names.Add($name)) { throw "归档包含重复路径: $name" }
            $raw = [int64]$entry.ExternalAttributes
            if ($raw -lt 0) { $raw += 4294967296 }
            $mode = (([uint64]$raw -shr 16) -band 0xFFFF)
            if (($mode -band 0xF000) -eq 0xA000) { throw "归档包含符号链接: $name" }
            $expectedMode = if ($name -eq $executableName) { 0x81ED } else { 0x81A4 }
            if ($mode -ne $expectedMode) {
                throw "归档文件的 Unix 权限不符合 0755/0644 合同: $name"
            }
            if ($name -eq $executableName) { $executableMode = '0' + [Convert]::ToString([int]$mode, 8) }
        }
        if (-not $executableMode) { throw "归档缺少可执行文件: $executableName" }
    } finally {
        $archive.Dispose()
        $stream.Dispose()
    }
    Expand-Archive -LiteralPath $ArchivePath -DestinationPath $ExtractPath -Force
    $expected = @(Get-ChildItem -LiteralPath $PackagePath -Recurse -File -Force | ForEach-Object { Get-RelativePath $PackagePath $_.FullName } | Sort-Object)
    $actual = @(Get-ChildItem -LiteralPath $ExtractPath -Recurse -File -Force | ForEach-Object { Get-RelativePath $ExtractPath $_.FullName } | Sort-Object)
    if (($expected -join "`n") -ne ($actual -join "`n")) {
        throw "归档解压后的文件清单与包目录不一致: $ArchivePath"
    }
    foreach ($relative in $expected) {
        $expectedHash = (Get-FileHash -LiteralPath (Join-Path $PackagePath $relative) -Algorithm SHA256).Hash
        $actualHash = (Get-FileHash -LiteralPath (Join-Path $ExtractPath $relative) -Algorithm SHA256).Hash
        if ($expectedHash -ne $actualHash) { throw "归档内容哈希不一致: $relative" }
    }
    [ordered]@{ archive = (Split-Path -Leaf $ArchivePath); files = $actual.Count; executable_mode = $executableMode; creator = 'Unix (libarchive)'; extraction = (Get-RelativePath $OutputRoot $ExtractPath); license_report_present = $licenseAvailable }
}

function Get-CandidateFiles {
    @(Get-ChildItem -LiteralPath $OutputRoot -Recurse -File -Force | Where-Object {
        $_.Name -ne 'file-sha256sums.txt' -and $_.FullName -notmatch '[\\/]web[\\/]node_modules[\\/]'
    } | Sort-Object FullName)
}

New-Item -ItemType Directory -Force -Path $sourceCopy, $binaryRoot, $archiveRoot, $packageRoot, $archiveStageRoot, $logRoot | Out-Null
$sourceHead = (& git -C $sourceRoot rev-parse HEAD).Trim()
$sourceState = (& git -C $sourceRoot status --short --branch | Out-String).Trim()
$sourceState | Set-Content -LiteralPath (Join-Path $logRoot 'git-status.txt') -Encoding UTF8
& git -C $sourceRoot diff --stat 2>$null | Set-Content -LiteralPath (Join-Path $logRoot 'git-diff-stat.txt') -Encoding UTF8
& git -C $sourceRoot diff --binary 2>$null | Set-Content -LiteralPath (Join-Path $logRoot 'tracked-diff.patch') -Encoding UTF8
& git -C $sourceRoot ls-files --others --exclude-standard 2>$null | Set-Content -LiteralPath (Join-Path $logRoot 'untracked-files.txt') -Encoding UTF8
& git -C $sourceRoot diff -- .github/workflows 2>$null | Set-Content -LiteralPath (Join-Path $logRoot 'workflow-diff.txt') -Encoding UTF8
foreach ($entry in @('cmd', 'internal', 'main.go', 'go.mod', 'go.sum', 'static', 'web', 'scripts', 'docker-compose.yml', 'LICENSE', 'README.md', 'README_zh.md')) {
    Copy-SourceDirectory $entry
}
if (-not (Test-Path -LiteralPath (Join-Path $sourceRoot 'web\node_modules'))) {
    throw 'web/node_modules 不存在，无法在离线模式下从冻结源码构建前端'
}
Get-SourceInputFiles | ForEach-Object {
    "{0}  {1}" -f (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant(), (Get-RelativePath $sourceCopy $_.FullName)
} | Set-Content -LiteralPath $sourceInputManifest -Encoding ascii

$saved = @{
    VITE_APP_VERSION = $env:VITE_APP_VERSION
    GOTOOLCHAIN = $env:GOTOOLCHAIN
    GOPROXY = $env:GOPROXY
    GOSUMDB = $env:GOSUMDB
    GOCACHE = $env:GOCACHE
    GOMODCACHE = $env:GOMODCACHE
    GOOS = $env:GOOS
    GOARCH = $env:GOARCH
    GOARM = $env:GOARM
    CGO_ENABLED = $env:CGO_ENABLED
    GOFLAGS = $env:GOFLAGS
}
$licenseAvailable = $false
if (Test-Path -LiteralPath $LicenseReportPath -PathType Leaf) {
    $licenseAvailable = (Get-Item -LiteralPath $LicenseReportPath).Length -gt 0
}
$licenseStatus = if ($licenseAvailable) { 'available' } else { 'unavailable: go-licenses report is missing or empty in the offline environment' }
try {
    $env:VITE_APP_VERSION = $Version
    $env:GOTOOLCHAIN = 'local'
    $env:GOPROXY = 'off'
    # Preserve the caller's checksum database configuration. Existing module
    # caches and -mod=readonly provide the offline build inputs.
    $env:GOCACHE = Join-Path $sourceRoot '.tmp-go-cache'
    $env:GOMODCACHE = Join-Path $sourceRoot '.tmp-go-mod'
    $env:GOFLAGS = '-mod=readonly'

    $frontendOutput = Join-Path $sourceCopy 'static\out'
    New-Item -ItemType Directory -Force -Path $frontendOutput | Out-Null
    $frontendWorkParent = Join-Path $OutputRoot 'checks\frontend-work'
    New-Item -ItemType Directory -Path $frontendWorkParent | Out-Null
    Copy-Item -LiteralPath (Join-Path $sourceCopy 'web') -Destination $frontendWorkParent -Recurse
    $frontendWork = Join-Path $frontendWorkParent 'web'
    New-Item -ItemType Junction -Path (Join-Path $frontendWork 'node_modules') -Target (Join-Path $sourceRoot 'web\node_modules') | Out-Null
    Invoke-Checked 'node' @((Join-Path $sourceRoot 'web\node_modules\typescript\bin\tsc'), '--noEmit', '--project', (Join-Path $frontendWork 'tsconfig.json')) $frontendWork
    Invoke-Checked 'node' @((Join-Path $sourceRoot 'web\node_modules\vite\bin\vite.js'), 'build', '--outDir', $frontendOutput, '--emptyOutDir', 'false') $frontendWork

    $buildTime = (Get-Date).ToString('yyyy-MM-ddTHH:mm:sszzz')
    $modulePath = 'github.com/Ink-lotus/octopus-roam'
    $binaries = @()
    $targets = @($Target | ForEach-Object { $_ -split ',' } | Where-Object { $_ })
    foreach ($targetName in $targets) {
        $parts = $targetName -split '/'
        if ($parts.Count -ne 2) { throw "目标必须使用 os/arch 格式: $targetName" }
        $goos, $goarch = $parts
        $binaryName = "octopus-roam-$goos-$goarch"
        $outputName = if ($goos -eq 'windows') { "$binaryName.exe" } else { $binaryName }
        $outputPath = Join-Path $binaryRoot $outputName
        $env:GOOS = $goos
        $env:GOARCH = $goarch
        $env:CGO_ENABLED = '0'
        $env:GOARM = if ($goarch -eq 'arm') { '7' } else { '' }
        $commit = $sourceHead.Substring(0, 12)
        if ($sourceState -match '(?m)^ [MADRCU]|^\?\?') { $commit += '-dirty' }
        $ldflags = "-s -w -X $modulePath/internal/conf.Version=$Version -X $modulePath/internal/conf.BuildTime=$buildTime -X $modulePath/internal/conf.Commit=$commit -X $modulePath/internal/conf.Repo=https://github.com/Ink-lotus/octopus-roam"
        Invoke-Checked 'go' @('build', '-trimpath', '-tags=jsoniter', "-ldflags=$ldflags", '-o', $outputPath, '.') $sourceCopy
        $hash = (Get-FileHash -LiteralPath $outputPath -Algorithm SHA256).Hash.ToLowerInvariant()
        $binaries += [ordered]@{ target = $targetName; path = (Get-RelativePath $OutputRoot $outputPath); sha256 = $hash }

        $package = Join-Path $packageRoot $targetName.Replace('/', '-')
        New-Item -ItemType Directory -Force -Path $package | Out-Null
        $archiveExecutable = if ($goos -eq 'windows') { 'octopus-roam.exe' } else { 'octopus-roam' }
        Copy-Item -LiteralPath $outputPath -Destination (Join-Path $package $archiveExecutable)
        foreach ($file in @('README.md', 'README_zh.md', 'LICENSE')) {
            Copy-Item -LiteralPath (Join-Path $sourceCopy $file) -Destination $package
        }
        if ($licenseAvailable) {
            Copy-Item -LiteralPath $LicenseReportPath -Destination (Join-Path $package 'THIRD_PARTY_LICENSES.csv')
        }
        if ($goos -eq 'windows') {
            Copy-Item -LiteralPath (Join-Path $sourceCopy 'scripts\windows\octopus-roam.cmd') -Destination $package
            New-Item -ItemType Directory -Force -Path (Join-Path $package 'windows') | Out-Null
            foreach ($file in @('octopus-roam-menu.ps1', 'octopus-roam-start.ps1', 'octopus-roam-autostart.ps1', 'send-stop-signal.ps1')) {
                Copy-Item -LiteralPath (Join-Path $sourceCopy "scripts\windows\$file") -Destination (Join-Path $package 'windows')
            }
        }
        $archivePath = Join-Path $archiveRoot "$binaryName.zip"
        New-ZipArchiveWithModes $package $archivePath ($goos -eq 'windows')
    }

    $sums = foreach ($archive in Get-ChildItem -LiteralPath $archiveRoot -Filter '*.zip' -File | Sort-Object Name) {
        "{0}  ./{1}" -f (Get-FileHash -LiteralPath $archive.FullName -Algorithm SHA256).Hash.ToLowerInvariant(), $archive.Name
    }
    $sums | Set-Content -LiteralPath (Join-Path $archiveRoot 'SHA256SUMS') -Encoding ascii

    foreach ($line in @(Get-Content -LiteralPath (Join-Path $archiveRoot 'SHA256SUMS'))) {
        if ($line -notmatch '^([0-9a-fA-F]{64})\s+\./(.+\.zip)$') { throw "SHA256SUMS 格式无效: $line" }
        $expectedHash = $Matches[1].ToLowerInvariant()
        $archiveName = $Matches[2]
        $archiveFile = Join-Path $archiveRoot $archiveName
        if (-not (Test-Path -LiteralPath $archiveFile)) { throw "SHA256SUMS 引用了不存在的归档: $archiveName" }
        $actualHash = (Get-FileHash -LiteralPath $archiveFile -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($actualHash -ne $expectedHash) { throw "归档哈希不匹配: $archiveName" }
    }

    # Build Docker input only for Linux targets that were actually requested.
    # The license report remains an explicit prerequisite and is never synthesized here.
    $dockerContextPlatforms = @()
    foreach ($targetName in $targets) {
        $targetParts = $targetName -split '/'
        if ($targetParts.Count -ne 2 -or $targetParts[0] -ne 'linux') { continue }
        $dockerPlatform = switch ($targetParts[1]) {
            'amd64' { 'linux/amd64'; break }
            'arm64' { 'linux/arm64'; break }
            '386' { 'linux/386'; break }
            'arm' { 'linux/arm/v7'; break }
            default { throw "不支持为 Docker context 映射的 Linux 架构: $($targetParts[1])" }
        }
        $linuxPackage = Join-Path $packageRoot $targetName.Replace('/', '-')
        $linuxExecutable = Join-Path $linuxPackage 'octopus-roam'
        if (-not (Test-Path -LiteralPath $linuxExecutable -PathType Leaf)) {
            throw "Linux 目标缺少 Docker context 可执行文件: $targetName"
        }
        $dockerDestination = Join-Path $dockerContext (Join-Path 'build\docker' ($dockerPlatform.Replace('/', '\')))
        New-Item -ItemType Directory -Force -Path $dockerDestination | Out-Null
        Copy-Item -LiteralPath $linuxExecutable -Destination (Join-Path $dockerDestination 'octopus-roam')
        $dockerContextPlatforms += $dockerPlatform
    }
    $dockerContextPlatforms = @($dockerContextPlatforms | Select-Object -Unique)
    New-Item -ItemType Directory -Force -Path $dockerContext | Out-Null
    if ($dockerContextPlatforms.Count -gt 0) {
        New-Item -ItemType Directory -Force -Path (Join-Path $dockerContext 'scripts\dockerfile') | Out-Null
        Copy-Item -LiteralPath (Join-Path $sourceCopy 'scripts\dockerfile\Dockerfile') -Destination (Join-Path $dockerContext 'scripts\dockerfile\Dockerfile')
        Copy-Item -LiteralPath (Join-Path $sourceCopy 'scripts\dockerfile\entrypoint.sh') -Destination (Join-Path $dockerContext 'scripts\dockerfile\entrypoint.sh')
        Copy-Item -LiteralPath (Join-Path $sourceCopy 'LICENSE') -Destination (Join-Path $dockerContext 'LICENSE')
        Copy-Item -LiteralPath (Join-Path $sourceCopy 'docker-compose.yml') -Destination (Join-Path $dockerContext 'docker-compose.yml')
        if ($licenseAvailable) {
            New-Item -ItemType Directory -Force -Path (Join-Path $dockerContext 'build') | Out-Null
            Copy-Item -LiteralPath $LicenseReportPath -Destination (Join-Path $dockerContext 'build\THIRD_PARTY_LICENSES.csv')
        }
        $dockerStatusLine = if ($licenseAvailable) { '- `build/THIRD_PARTY_LICENSES.csv` is present.' } else { '- `build/THIRD_PARTY_LICENSES.csv` is intentionally absent because the offline license report is unavailable.' }
        $dockerBuildLine = if ($licenseAvailable) { '- Docker input still requires normal daemon and base-image validation.' } else { '- Building the image remains blocked until that report is generated and reviewed.' }
        @(
            '# Docker context status',
            '',
            ('- Linux platforms included: ' + ($dockerContextPlatforms -join ', ') + '.'),
            $dockerStatusLine,
            $dockerBuildLine
        ) | Set-Content -LiteralPath (Join-Path $dockerContext 'DOCKER-BUILD-STATUS.md') -Encoding UTF8
    } else {
        @(
            '# Docker context status',
            '',
            '- No Linux target was built; Docker context is not applicable.',
            '- Request at least one Linux target to prepare Docker inputs.'
        ) | Set-Content -LiteralPath (Join-Path $dockerContext 'DOCKER-BUILD-STATUS.md') -Encoding UTF8
    }

    $archiveChecks = @()
    New-Item -ItemType Directory -Force -Path $archiveCheckRoot | Out-Null
    foreach ($archive in @(Get-ChildItem -LiteralPath $archiveRoot -Filter '*.zip' -File | Sort-Object Name)) {
        $key = [IO.Path]::GetFileNameWithoutExtension($archive.Name).Substring('octopus-roam-'.Length)
        $package = Join-Path $packageRoot $key
        $extract = Join-Path $archiveCheckRoot $key
        $archiveChecks += Test-ZipArchive $archive.FullName $package $extract ($key -notlike 'windows-*')
    }

    foreach ($line in @(Get-Content -LiteralPath $sourceInputManifest)) {
        if ($line -notmatch '^([0-9a-fA-F]{64})\s+(.+)$') { throw "源码输入哈希格式无效: $line" }
        $expectedHash = $Matches[1].ToLowerInvariant()
        $relative = $Matches[2]
        $file = Join-Path $sourceCopy ($relative.Replace('/', '\'))
        if (-not (Test-Path -LiteralPath $file -PathType Leaf)) { throw "源码输入缺失: $relative" }
        $actualHash = (Get-FileHash -LiteralPath $file -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($actualHash -ne $expectedHash) { throw "源码输入哈希不匹配: $relative" }
        $rootFile = Join-Path $sourceRoot ($relative.Replace('/', '\'))
        if (-not (Test-Path -LiteralPath $rootFile -PathType Leaf)) { throw "工作区源码输入缺失: $relative" }
        $rootHash = (Get-FileHash -LiteralPath $rootFile -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($rootHash -ne $expectedHash) { throw "工作区源码输入在冻结后发生变化: $relative" }
    }

    $dockerContextStatus = if ($dockerContextPlatforms.Count -eq 0) {
        'not-applicable: no linux target was built'
    } elseif ($licenseAvailable) {
        'license-report-present; daemon-not-run'
    } else {
        'incomplete-license-report'
    }
    $licenseResult = if ($licenseAvailable) { 'available' } else { 'blocked' }
    $verificationResults = @()
    if ($VerificationScript) {
        $verificationTools = Join-Path $OutputRoot 'checks\verification-tools'
        New-Item -ItemType Directory -Path $verificationTools | Out-Null
        Get-ChildItem -LiteralPath (Split-Path -Parent $VerificationScript) -File | ForEach-Object { Copy-Item -LiteralPath $_.FullName -Destination $verificationTools }
        $verificationCopy = Join-Path $verificationTools (Split-Path -Leaf $VerificationScript)
        Invoke-Checked 'pwsh' @('-NoProfile', '-File', $verificationCopy, '-CandidateRoot', $OutputRoot, '-RepositoryRoot', $sourceRoot) $sourceRoot
        $verificationResults = @(Get-Content -Raw -LiteralPath (Join-Path $OutputRoot 'checks\verification-results.json') -Encoding UTF8 | ConvertFrom-Json)
    }
    [ordered]@{
        version = $Version
        built_at = $buildTime
        source_head = $sourceHead
        source_state = $sourceState
        source_snapshot = 'source'
        source_input_manifest = 'source-input-sha256sums.txt'
        license_report = $licenseStatus
        binaries = $binaries
        archives = @(Get-ChildItem -LiteralPath $archiveRoot -Filter '*.zip' -File | ForEach-Object {
            [ordered]@{ name = $_.Name; sha256 = (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant(); package = [IO.Path]::GetFileNameWithoutExtension($_.Name).Substring('octopus-roam-'.Length) }
        })
        archive_checks = $archiveChecks
        verification = $verificationResults
        docker_context = [ordered]@{ path = 'docker-context'; status = $dockerContextStatus; platforms = @($dockerContextPlatforms) }
    } | ConvertTo-Json -Depth 10 | Set-Content -LiteralPath (Join-Path $OutputRoot 'build-info.json') -Encoding UTF8

    @(
        '# v0.1.0 local candidate evidence',
        '',
        "- Candidate version: $Version",
        ('- Source HEAD: ' + $sourceHead),
        '- The source snapshot includes Go, web, static, scripts and Docker build inputs. Frontend tools run against a copy of the frozen web inputs under `checks/frontend-work/web`; its `node_modules` junction references the existing offline cache and is excluded from file hashes.',
        "- License report: $licenseStatus",
        "- Archive checks: $($archiveChecks.Count) archives listed, extracted, path-checked and compared with package contents.",
        '- Archive SHA-256 list: `archives/SHA256SUMS`.',
        '- Full candidate file hashes: `file-sha256sums.txt` (excluding itself and external dependency junction targets).',
        '- Actual build and regression output: `checks/logs/`.',
        '- Regression and runtime outcomes: `checks/verification-results.json` when a verifier is supplied.'
    ) | Set-Content -LiteralPath (Join-Path $OutputRoot 'evidence-index.md') -Encoding UTF8
    @(
        '# Verification log',
        '',
        '| Check | Result | Evidence |',
        '| --- | --- | --- |',
        '| Frozen source input hashes | passed | `source-input-sha256sums.txt` |',
        '| Archive SHA-256 list | passed | `archives/SHA256SUMS` |',
        "| Archive path, duplicate and symlink checks | passed | $($archiveChecks.Count) archives |",
        "| Unix executable mode checks | passed | $($archiveChecks.Count) archives (where applicable) |",
        "| Third-party license report | $licenseResult | $licenseStatus |",
        '| Docker image build | not run | no daemon/base-image acceptance in L6.5 |',
        $(foreach ($result in $verificationResults) { '| {0} | {1} | {2} |' -f $result.name, $result.status, $result.evidence })
    ) | Set-Content -LiteralPath (Join-Path $OutputRoot 'verification-log.md') -Encoding UTF8

    $manifestLines = foreach ($file in Get-CandidateFiles) {
        "{0}  {1}" -f (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash.ToLowerInvariant(), (Get-RelativePath $OutputRoot $file.FullName)
    }
    $manifestLines | Set-Content -LiteralPath (Join-Path $OutputRoot 'file-sha256sums.txt') -Encoding ascii
    foreach ($line in @(Get-Content -LiteralPath (Join-Path $OutputRoot 'file-sha256sums.txt'))) {
        if ($line -notmatch '^([0-9a-fA-F]{64})\s+(.+)$') { throw "文件哈希格式无效: $line" }
        $file = Join-Path $OutputRoot ($Matches[2].Replace('/', '\'))
        if (-not (Test-Path -LiteralPath $file -PathType Leaf)) { throw "文件哈希引用缺失: $($Matches[2])" }
        if ((Get-FileHash -LiteralPath $file -Algorithm SHA256).Hash.ToLowerInvariant() -ne $Matches[1].ToLowerInvariant()) { throw "文件哈希不匹配: $($Matches[2])" }
    }
    Write-Host "Local build completed: $OutputRoot"
} finally {
    foreach ($key in $saved.Keys) {
        if ($null -eq $saved[$key]) {
            Remove-Item -Path ("Env:" + $key) -ErrorAction SilentlyContinue
        } else {
            Set-Item -Path ("Env:" + $key) -Value $saved[$key]
        }
    }
}
