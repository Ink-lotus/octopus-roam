@echo off
setlocal DisableDelayedExpansion
"%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe" -NoLogo -NoProfile -ExecutionPolicy RemoteSigned -File "%~dp0octopus-autostart.ps1" -Action Enable
set "OCTOPUS_LAUNCHER_EXIT=%ERRORLEVEL%"
echo.
pause
exit /b %OCTOPUS_LAUNCHER_EXIT%
