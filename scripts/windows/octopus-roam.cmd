@echo off
setlocal DisableDelayedExpansion
"%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe" -NoLogo -NoProfile -ExecutionPolicy RemoteSigned -File "%~dp0windows\octopus-roam-menu.ps1" -RuntimeDirectory "%~dp0."
set "OCTOPUS_ROAM_MENU_EXIT=%ERRORLEVEL%"
if not "%OCTOPUS_ROAM_MENU_EXIT%"=="0" (
    echo.
    echo Extract the entire Windows ZIP into one folder, then double-click octopus-roam.cmd.
    pause
)
exit /b %OCTOPUS_ROAM_MENU_EXIT%
