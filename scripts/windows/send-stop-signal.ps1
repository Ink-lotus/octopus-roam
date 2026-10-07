# Run only in a separate PowerShell process: attaching detaches its own console.
param(
    [Parameter(Mandatory = $true)][int]$TargetProcessId,
    [Parameter(Mandatory = $true)][string]$ExpectedExecutable,
    [Parameter(Mandatory = $true)][string]$ResultPath
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

try {
    $target = Get-Process -Id $TargetProcessId -ErrorAction Stop
    if (-not [string]::Equals($target.Path, [IO.Path]::GetFullPath($ExpectedExecutable), [StringComparison]::OrdinalIgnoreCase)) {
        throw 'The process executable does not match the deployment.'
    }

    Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class OctopusConsoleSignal {
    public delegate bool ControlHandler(uint controlEvent);
    public static readonly ControlHandler IgnoreSignal = controlEvent => true;
    [DllImport("kernel32.dll", SetLastError = true)]
    public static extern bool FreeConsole();
    [DllImport("kernel32.dll", SetLastError = true)]
    public static extern bool AttachConsole(uint processId);
    [DllImport("kernel32.dll", SetLastError = true)]
    public static extern bool SetConsoleCtrlHandler(ControlHandler handler, bool add);
    [DllImport("kernel32.dll", SetLastError = true)]
    public static extern uint GetConsoleProcessList([Out] uint[] processes, uint count);
    [DllImport("kernel32.dll", SetLastError = true)]
    public static extern bool GenerateConsoleCtrlEvent(uint controlEvent, uint processGroupId);
}
'@

    [OctopusConsoleSignal]::FreeConsole() | Out-Null
    if (-not [OctopusConsoleSignal]::AttachConsole([uint32]$TargetProcessId)) {
        throw 'Cannot attach to the server console. Stop Octopus with Ctrl+C in its original terminal, then retry.'
    }
    try {
        $consoleProcesses = New-Object uint32[] 64
        $count = [OctopusConsoleSignal]::GetConsoleProcessList($consoleProcesses, $consoleProcesses.Length)
        if ($count -eq 0 -or $count -gt $consoleProcesses.Length) {
            throw 'Cannot verify which processes share the server console.'
        }
        $shellCount = 0
        $systemShell = Join-Path ([Environment]::GetFolderPath('System')) 'cmd.exe'
        for ($index = 0; $index -lt $count; $index++) {
            $consoleProcessId = $consoleProcesses[$index]
            if ($consoleProcessId -eq $PID -or $consoleProcessId -eq $TargetProcessId) { continue }
            $other = Get-Process -Id $consoleProcessId -ErrorAction Stop
            if (-not [string]::Equals($other.Path, $systemShell, [StringComparison]::OrdinalIgnoreCase)) {
                throw 'The server shares a console with another application. Stop Octopus with Ctrl+C in its original terminal, then retry.'
            }
            $shellCount++
        }
        if ($shellCount -gt 1) { throw 'The server console contains multiple shells; refusing to broadcast a control signal.' }
        if (-not [OctopusConsoleSignal]::SetConsoleCtrlHandler([OctopusConsoleSignal]::IgnoreSignal, $true)) {
            throw 'Cannot configure the signal helper.'
        }
        # Go maps CTRL_BREAK_EVENT to SIGINT too; it works when a launcher
        # inherited the Windows flag that ignores ordinary Ctrl+C events.
        if (-not [OctopusConsoleSignal]::GenerateConsoleCtrlEvent(1, 0)) {
            throw 'Cannot send Ctrl+Break to the server.'
        }
        Start-Sleep -Milliseconds 300
    } finally {
        [OctopusConsoleSignal]::FreeConsole() | Out-Null
    }
    @{ success = $true } | ConvertTo-Json | Set-Content -LiteralPath $ResultPath -Encoding UTF8
    exit 0
} catch {
    @{ success = $false; error = $_.Exception.Message } | ConvertTo-Json | Set-Content -LiteralPath $ResultPath -Encoding UTF8
    Write-Error $_.Exception.Message -ErrorAction Continue
    exit 1
}
