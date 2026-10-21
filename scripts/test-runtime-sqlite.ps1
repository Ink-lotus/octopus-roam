# Runs the isolated SQLite acceptance flow without contacting an upstream
# service. The caller supplies a built local executable. Two independent
# runtimes are used: the first creates data and an encrypted backup; the second
# starts with an empty database and restores that backup.
param(
    [Parameter(Mandatory = $true)]
    [string]$Executable,
    [int]$Port = 18080,
    [string]$RuntimeRoot,
    [string]$ExpectedVersion,
    [switch]$PreserveRuntime
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$repositoryRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$executablePath = [IO.Path]::GetFullPath($Executable)
if (-not (Test-Path -LiteralPath $executablePath -PathType Leaf)) {
    throw "executable does not exist: $executablePath"
}
if (-not $RuntimeRoot) {
    $RuntimeRoot = Join-Path ([IO.Path]::GetTempPath()) ('octopus-roam-sqlite-' + [guid]::NewGuid().ToString('N'))
}
$RuntimeRoot = [IO.Path]::GetFullPath($RuntimeRoot)
if (Test-Path -LiteralPath $RuntimeRoot) {
    $existing = @(Get-ChildItem -LiteralPath $RuntimeRoot -Force -ErrorAction Stop)
    if ($existing.Count -gt 0) { throw "RuntimeRoot must be new or empty: $RuntimeRoot" }
} else {
    New-Item -ItemType Directory -Force -Path $RuntimeRoot | Out-Null
}

$primary = $null
$empty = $null
$backup = Join-Path $RuntimeRoot 'primary\backup.encrypted.json'
$rollbackBackup = Join-Path $RuntimeRoot 'primary\invalid-rollback.json'
$savedPriceSkip = $env:OCTOPUS_ROAM_SKIP_STARTUP_PRICE_UPDATE

function Write-Utf8NoBom([string]$Path, [string]$Content) {
    $encoding = New-Object System.Text.UTF8Encoding($false)
    [IO.File]::WriteAllText($Path, $Content, $encoding)
}

function Invoke-Json([string]$Method, [string]$Uri, [object]$Body, [Microsoft.PowerShell.Commands.WebRequestSession]$Session) {
    $params = @{ Method = $Method; Uri = $Uri; WebSession = $Session; ErrorAction = 'Stop'; UseBasicParsing = $true; TimeoutSec = 15 }
    if ($null -ne $Body) {
        $params.ContentType = 'application/json'
        $params.Body = ($Body | ConvertTo-Json -Compress -Depth 12)
    }
    $response = Invoke-WebRequest @params
    if ($response.StatusCode -ne 200) { throw "HTTP $($response.StatusCode) from $Uri" }
    if ([string]::IsNullOrWhiteSpace($response.Content)) { return $null }
    return ($response.Content | ConvertFrom-Json)
}

function Invoke-ExpectedFailure([string]$Method, [string]$Uri, [string]$Body, [Microsoft.PowerShell.Commands.WebRequestSession]$Session) {
    try {
        $response = Invoke-WebRequest -Method $Method -Uri $Uri -WebSession $Session -UseBasicParsing -TimeoutSec 15 -ContentType 'application/json' -Body $Body -ErrorAction Stop
        if ($response.StatusCode -lt 400) { throw "expected a failed request, got HTTP $($response.StatusCode) from $Uri" }
    } catch {
        $webResponse = $_.Exception.Response
        if ($null -eq $webResponse) { throw }
        $status = [int]$webResponse.StatusCode
        if ($status -lt 400) { throw "expected a failed request, got HTTP $status from $Uri" }
    }
}

function Invoke-MultipartFile([string]$Uri, [Microsoft.PowerShell.Commands.WebRequestSession]$Session, [string]$FilePath, [string]$Password) {
    # Build multipart bytes directly so the same acceptance script works in
    # Windows PowerShell 5.1 and PowerShell 7 (Invoke-WebRequest -Form is not
    # available in 5.1).
    $boundary = '----------------octopusroam' + [guid]::NewGuid().ToString('N')
    $utf8 = New-Object System.Text.UTF8Encoding($false)
    $stream = New-Object System.IO.MemoryStream
    try {
        $writeText = {
            param([string]$Text)
            $bytes = $utf8.GetBytes($Text)
            $stream.Write($bytes, 0, $bytes.Length)
        }
        & $writeText ("--$boundary`r`nContent-Disposition: form-data; name=`"file`"; filename=`"backup.encrypted.json`"`r`nContent-Type: application/json`r`n`r`n")
        $fileBytes = [IO.File]::ReadAllBytes($FilePath)
        $stream.Write($fileBytes, 0, $fileBytes.Length)
        & $writeText ("`r`n--$boundary`r`nContent-Disposition: form-data; name=`"password`"`r`n`r`n$Password`r`n--$boundary--`r`n")
        $body = $stream.ToArray()
    } finally {
        $stream.Dispose()
    }

    $request = [Net.HttpWebRequest]::Create($Uri)
    $request.Method = 'POST'
    $request.ContentType = "multipart/form-data; boundary=$boundary"
    $request.ContentLength = $body.Length
    $request.Timeout = 15000
    $request.CookieContainer = New-Object Net.CookieContainer
    foreach ($cookie in $Session.Cookies.GetCookies([Uri]$Uri)) {
        $request.CookieContainer.Add([Uri]$Uri, $cookie)
    }
    $requestStream = $request.GetRequestStream()
    try { $requestStream.Write($body, 0, $body.Length) } finally { $requestStream.Dispose() }
    try {
        $response = $request.GetResponse()
    } catch {
        $webResponse = $_.Exception.Response
        if ($null -eq $webResponse) { throw }
        $status = [int]$webResponse.StatusCode
        $webResponse.Close()
        throw "HTTP $status from $Uri"
    }
    try {
        if ([int]$response.StatusCode -ne 200) { throw "HTTP $($response.StatusCode) from $Uri" }
    } finally {
        $response.Close()
    }
}

function Wait-Endpoint([string]$Uri) {
    for ($attempt = 0; $attempt -lt 100; $attempt++) {
        try {
            $response = Invoke-WebRequest -Uri $Uri -UseBasicParsing -TimeoutSec 2 -ErrorAction Stop
            if ($response.StatusCode -ge 200 -and $response.StatusCode -lt 500) { return }
        } catch { }
        Start-Sleep -Milliseconds 250
    }
    throw "server did not start: $Uri"
}

function Get-Setting([object]$Settings, [string]$Key) {
    $item = @($Settings.data | Where-Object { $_.key -eq $Key }) | Select-Object -First 1
    if ($null -eq $item) { throw "setting was not returned: $Key" }
    return $item.value
}

function New-Runtime([string]$Name, [int]$RuntimePort) {
    $runtimePath = Join-Path $RuntimeRoot $Name
    $dataPath = Join-Path $runtimePath 'data'
    New-Item -ItemType Directory -Force -Path $dataPath | Out-Null
    $configPath = Join-Path $dataPath 'config.json'
    $config = [ordered]@{
        server = @{ host = '127.0.0.1'; port = $RuntimePort }
        database = @{ type = 'sqlite'; path = [IO.Path]::GetFullPath((Join-Path $dataPath 'data.db')) }
        log = @{ level = 'warn' }
    }
    Write-Utf8NoBom $configPath ($config | ConvertTo-Json -Depth 5)
    return [pscustomobject]@{
        Name = $Name
        Root = $runtimePath
        Data = $dataPath
        Config = $configPath
        Port = $RuntimePort
        Process = $null
        Session = $null
    }
}

function Start-Runtime([psobject]$Runtime) {
    $stdoutPath = Join-Path $Runtime.Root 'runtime.stdout.log'
    $stderrPath = Join-Path $Runtime.Root 'runtime.stderr.log'
    $Runtime.Process = Start-Process -FilePath $executablePath -ArgumentList @('start', '--config', $Runtime.Config) -WorkingDirectory $repositoryRoot -RedirectStandardOutput $stdoutPath -RedirectStandardError $stderrPath -PassThru -WindowStyle Hidden
    Wait-Endpoint "http://127.0.0.1:$($Runtime.Port)/"
    $Runtime.Session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
    $null = Invoke-Json 'Post' "http://127.0.0.1:$($Runtime.Port)/api/v1/user/login" @{ username = 'admin'; password = 'admin'; expire = -1 } $Runtime.Session
}

function Stop-Runtime([psobject]$Runtime) {
    if ($null -ne $Runtime -and $null -ne $Runtime.Process) {
        Stop-Process -Id $Runtime.Process.Id -Force -ErrorAction SilentlyContinue
        try { $Runtime.Process.WaitForExit(10000) | Out-Null } catch { }
        $Runtime.Process = $null
    }
}

function Assert-PersistedEntities([psobject]$Runtime, [string]$ExpectedSetting) {
    $settings = Invoke-Json 'Get' "http://127.0.0.1:$($Runtime.Port)/api/v1/setting/list" $null $Runtime.Session
    if ((Get-Setting $settings 'stats_save_interval') -ne $ExpectedSetting) { throw "setting was not persisted: expected $ExpectedSetting" }
    $channels = Invoke-Json 'Get' "http://127.0.0.1:$($Runtime.Port)/api/v1/channel/stats" $null $Runtime.Session
    $groups = Invoke-Json 'Get' "http://127.0.0.1:$($Runtime.Port)/api/v1/group/list" $null $Runtime.Session
    $apiKeys = Invoke-Json 'Get' "http://127.0.0.1:$($Runtime.Port)/api/v1/apikey/list" $null $Runtime.Session
    $channel = @($channels.data | Where-Object { $_.channel_name -eq 'local-sqlite-channel' }) | Select-Object -First 1
    $group = @($groups.data | Where-Object { $_.name -eq 'local-sqlite-model' }) | Select-Object -First 1
    $apiKey = @($apiKeys.data | Where-Object { $_.name -eq 'local-sqlite-api-key' }) | Select-Object -First 1
    if ($null -eq $channel -or $channel.enabled) { throw 'channel state was not restored' }
    if ($null -eq $group) { throw 'group state was not restored' }
    if ($null -eq $apiKey -or $apiKey.enabled) { throw 'API key state was not restored' }
}

try {
    $env:OCTOPUS_ROAM_SKIP_STARTUP_PRICE_UPDATE = '1'
    $primary = New-Runtime 'primary' $Port
    Start-Runtime $primary

    if ($ExpectedVersion) {
        $version = Invoke-Json 'Get' "http://127.0.0.1:$($primary.Port)/api/v1/update/now-version" $null $primary.Session
        if ($version.data -ne $ExpectedVersion) { throw "archive backend version mismatch: $($version.data)" }
        $updateStatus = Invoke-Json 'Get' "http://127.0.0.1:$($primary.Port)/api/v1/update" $null $primary.Session
        if ($updateStatus.data.status -ne 'development') { throw "local version update status mismatch: $($updateStatus.data.status)" }
    }

    $settings = Invoke-Json 'Get' "http://127.0.0.1:$($primary.Port)/api/v1/setting/list" $null $primary.Session
    if ((Get-Setting $settings 'stats_save_interval') -ne '10') { throw 'default settings were not initialized' }
    $null = Invoke-Json 'Post' "http://127.0.0.1:$($primary.Port)/api/v1/setting/set" @{ key = 'stats_save_interval'; value = '11' } $primary.Session
    $channel = Invoke-Json 'Post' "http://127.0.0.1:$($primary.Port)/api/v1/channel/create" @{
        name = 'local-sqlite-channel'; base_url = 'http://127.0.0.1:1'; enabled = $true; allowed_protocols = 4
        keys = @(@{ name = 'synthetic-key'; key = 'synthetic-channel-secret'; enabled = $false })
        models = @('synthetic-model'); grants = @(@{ model_name = 'synthetic-model'; key_name = 'synthetic-key'; protocols = 4 })
    } $primary.Session
    if ($null -eq $channel.data.id) { throw 'channel create did not return an id' }
    $null = Invoke-Json 'Post' "http://127.0.0.1:$($primary.Port)/api/v1/channel/enable" @{ id = $channel.data.id; enabled = $false } $primary.Session
    $grants = Invoke-Json 'Get' "http://127.0.0.1:$($primary.Port)/api/v1/channel/grants" $null $primary.Session
    $grant = @($grants.data | Where-Object { $_.channel_name -eq 'local-sqlite-channel' -and $_.model_name -eq 'synthetic-model' }) | Select-Object -First 1
    if ($null -eq $grant) { throw 'channel grant was not persisted' }
    $group = Invoke-Json 'Post' "http://127.0.0.1:$($primary.Port)/api/v1/group/create" @{ name = 'local-sqlite-model'; mode = 'manual'; items = @(@{ channel_grant_id = $grant.id }) } $primary.Session
    if ($null -eq $group.data.id) { throw 'group create did not return an id' }
    $apiKey = Invoke-Json 'Post' "http://127.0.0.1:$($primary.Port)/api/v1/apikey/create" @{ name = 'local-sqlite-api-key'; api_key = 'synthetic-local-api-key'; enabled = $true } $primary.Session
    if ($null -eq $apiKey.data.id) { throw 'API key create did not return an id' }
    $null = Invoke-Json 'Post' "http://127.0.0.1:$($primary.Port)/api/v1/apikey/update" @{ id = $apiKey.data.id; name = 'local-sqlite-api-key'; api_key = 'synthetic-local-api-key'; enabled = $false } $primary.Session

    $null = Invoke-WebRequest -Method Post -Uri "http://127.0.0.1:$($primary.Port)/api/v1/setting/export" -WebSession $primary.Session -UseBasicParsing -ContentType 'application/json' -Body '{"password":"synthetic backup password"}' -OutFile $backup -ErrorAction Stop
    if ((Get-Item -LiteralPath $backup).Length -lt 100) { throw 'encrypted backup was not written' }
    if (Select-String -LiteralPath $backup -Pattern 'synthetic backup password' -Quiet) { throw 'backup leaked its password' }

    Stop-Runtime $primary
    Start-Sleep -Milliseconds 300
    Start-Runtime $primary
    Assert-PersistedEntities $primary '11'

    $null = Invoke-Json 'Post' "http://127.0.0.1:$($primary.Port)/api/v1/setting/set" @{ key = 'stats_save_interval'; value = '12' } $primary.Session
    $invalidDump = [ordered]@{
        product = 'octopus-roam'; version = 1000
        groups = @(@{ id = 987654321; name = 'rollback-only-group'; mode = 'manual'; items = @() })
        group_items = @(@{ id = 987654322; group_id = 987654321; channel_grant_id = 987654323; priority = 0 })
    }
    Write-Utf8NoBom $rollbackBackup ($invalidDump | ConvertTo-Json -Depth 12)
    Invoke-ExpectedFailure 'Post' "http://127.0.0.1:$($primary.Port)/api/v1/setting/import" (Get-Content -Raw -LiteralPath $rollbackBackup -Encoding UTF8) $primary.Session
    $afterFailure = Invoke-Json 'Get' "http://127.0.0.1:$($primary.Port)/api/v1/setting/list" $null $primary.Session
    if ((Get-Setting $afterFailure 'stats_save_interval') -ne '12') { throw 'failed import changed an existing setting' }
    $afterGroups = Invoke-Json 'Get' "http://127.0.0.1:$($primary.Port)/api/v1/group/list" $null $primary.Session
    if (@($afterGroups.data | Where-Object { $_.name -eq 'rollback-only-group' }).Count -ne 0) { throw 'failed import left a partial group behind' }

    Stop-Runtime $primary
    $empty = New-Runtime 'empty-restore' ($Port + 1)
    Start-Runtime $empty
    $emptySettings = Invoke-Json 'Get' "http://127.0.0.1:$($empty.Port)/api/v1/setting/list" $null $empty.Session
    if ((Get-Setting $emptySettings 'stats_save_interval') -ne '10') { throw 'empty SQLite database was not initialized independently' }
    Invoke-MultipartFile "http://127.0.0.1:$($empty.Port)/api/v1/setting/import" $empty.Session $backup 'synthetic backup password'
    Assert-PersistedEntities $empty '11'

    Write-Output (ConvertTo-Json -Compress ([ordered]@{
        status = 'passed'
        runtime_root = $RuntimeRoot
        primary_database = (Join-Path $primary.Data 'data.db')
        empty_database = (Join-Path $empty.Data 'data.db')
        checks = @('sqlite_initialize', 'login', 'setting_write', 'channel_create', 'group_create', 'api_key_create', 'encrypted_backup', 'restart_persistence', 'failed_import_rollback', 'empty_sqlite_restore', 'restored_disabled_state')
    }))
} finally {
    Stop-Runtime $primary
    Stop-Runtime $empty
    if ($null -eq $savedPriceSkip) {
        Remove-Item Env:OCTOPUS_ROAM_SKIP_STARTUP_PRICE_UPDATE -ErrorAction SilentlyContinue
    } else {
        $env:OCTOPUS_ROAM_SKIP_STARTUP_PRICE_UPDATE = $savedPriceSkip
    }
    if (-not $PreserveRuntime) {
        Remove-Item -LiteralPath $RuntimeRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
}
