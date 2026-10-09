@echo off
setlocal DisableDelayedExpansion
"%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe" -NoLogo -NoProfile -ExecutionPolicy RemoteSigned -File "%~dp0octopus-roam-autostart.ps1" -Action Disable
set "OCTOPUS_ROAM_LAUNCHER_EXIT=%ERRORLEVEL%"
echo.
pause
exit /b %OCTOPUS_ROAM_LAUNCHER_EXIT%
