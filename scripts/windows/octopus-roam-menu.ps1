# Windows PowerShell 5.1 reads the Chinese menu using this file's UTF-8 BOM.
param([string]$RuntimeDirectory = (Join-Path $PSScriptRoot '..'))

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'octopus-roam-autostart.ps1') -RuntimeDirectory $RuntimeDirectory

function Get-OctopusAccessUrl {
    $endpoint = [pscustomobject]@{ Host = '127.0.0.1'; Port = 8080 }
    if (Test-Path -LiteralPath (Join-Path $script:OctopusDirectory 'data\config.json')) {
        $endpoint = Get-OctopusEndpoint
    }
    $browserHost = $endpoint.Host
    if ($browserHost -in @('', '0.0.0.0', '::')) { $browserHost = '127.0.0.1' }
    return (New-Object UriBuilder('http', $browserHost, $endpoint.Port)).Uri.AbsoluteUri.TrimEnd('/')
}

function Invoke-OctopusMenuAction {
    param([Parameter(Mandatory = $true)][ValidateSet('1', '2', '3', '4')][string]$Choice)
    # One operation also covers the combined start/autostart and stop/disable choices.
    # The start/stop functions acquire the same reentrant Windows mutex.
    $mutex = New-OctopusMutex
    $ownsMutex = $false
    try {
        try { $ownsMutex = $mutex.WaitOne(10000) } catch [Threading.AbandonedMutexException] { $ownsMutex = $true }
        if (-not $ownsMutex) { throw '另一个启动、关闭或更新操作正在进行，请稍后重试。' }
        switch ($Choice) {
            '1' { Start-Octopus }
            '2' {
                if ((Get-OctopusAutostartState) -eq 'Conflict') { throw '同名自启项属于另一个部署，请先在原部署中关闭自启。' }
                Start-Octopus
                Invoke-OctopusAutostart -Mode Enable
            }
            '3' { Stop-Octopus }
            '4' {
                # Disable future sign-in launches even when graceful shutdown times out.
                Invoke-OctopusAutostart -Mode Disable
                Stop-Octopus
            }
        }
        if ($Choice -in @('1', '2')) {
            Write-Host ('服务已启动，管理页面：' + (Get-OctopusAccessUrl)) -ForegroundColor Green
        } else {
            Write-Host '服务已关闭。' -ForegroundColor Green
        }
    } finally {
        if ($ownsMutex) { $mutex.ReleaseMutex() }
        $mutex.Dispose()
    }
}

function Show-OctopusMenu {
    while ($true) {
        Write-Host ''
        Write-Host '========== Octopus Roam ==========' -ForegroundColor Cyan
        $running = @(Get-OctopusProcess)
        $serviceState = if ($running.Count -eq 0) { '已停止' } elseif ($running.Count -eq 1) { '运行中' } else { '检测到多个进程' }
        $autostartState = switch (Get-OctopusAutostartState) {
            'Enabled' { '已开启' }
            'Disabled' { '已关闭' }
            'Conflict' { '同名自启项属于另一个部署' }
        }
        Write-Host "服务：$serviceState    开机自启：$autostartState"
        Write-Host '开机自启在当前 Windows 用户登录后生效。'
        Write-Host ''
        Write-Host '  1. 启动服务'
        Write-Host '  2. 启动服务并添加开机自启'
        Write-Host '  3. 关闭服务'
        Write-Host '  4. 关闭服务并关闭开机自启'
        Write-Host '  0. 退出菜单'
        Write-Host ''
        Write-Host '关闭菜单窗口后，已启动的服务会继续在后台运行。'
        if ([Console]::IsInputRedirected) {
            $choice = [Console]::ReadLine()
        } else {
            $choice = Read-Host '请输入选项 [0-4]，然后按 Enter'
        }
        if ($null -eq $choice -or $choice.Trim() -eq '0') { return }
        $choice = $choice.Trim()
        if ($choice -notin @('1', '2', '3', '4')) {
            Write-Host '请输入 0、1、2、3 或 4。' -ForegroundColor Yellow
            continue
        }
        try {
            if ($choice -in @('3', '4')) { Write-Host '正在关闭服务并等待数据保存，请稍候……' }
            Invoke-OctopusMenuAction -Choice $choice
        } catch {
            Write-Host ('操作未完成：' + $_.Exception.Message) -ForegroundColor Red
            Write-Host '启动失败时请检查 logs 文件夹。端口被占用时，先关闭占用该端口的应用，或修改 data\config.json 中的端口。'
        }
    }
}

if ($MyInvocation.InvocationName -ne '.') {
    try {
        [Console]::OutputEncoding = New-Object Text.UTF8Encoding($false)
        Show-OctopusMenu
    } catch {
        Write-Host ('无法打开菜单：' + $_.Exception.Message) -ForegroundColor Red
        exit 1
    }
}
