@echo off
setlocal DisableDelayedExpansion
if not exist "%~dp0octopus-roam.exe" goto missing
for /f "tokens=2 delims=:" %%C in ('chcp') do set "OCTOPUS_ROAM_CODEPAGE=%%C"
chcp 65001 >nul
:menu
echo.
echo ========== Octopus Roam ==========
"%~dp0octopus-roam.exe" desktop status --runtime "%~dp0."
echo.
echo   1. 启动服务
echo   2. 启动服务并添加登录自启
echo   3. 关闭服务
echo   4. 关闭服务并关闭登录自启
echo   0. 退出菜单
echo.
echo 关闭菜单窗口后，服务仍在后台运行。自启在当前用户登录后生效。
"%SystemRoot%\System32\choice.exe" /c 01234 /n /m "请选择 [0-4]："
if errorlevel 255 goto failed
if errorlevel 5 goto stop_disable
if errorlevel 4 goto stop
if errorlevel 3 goto start_enable
if errorlevel 2 goto start
goto done
:start
"%~dp0octopus-roam.exe" desktop start --runtime "%~dp0."
goto result
:start_enable
"%~dp0octopus-roam.exe" desktop start --runtime "%~dp0." --autostart
goto result
:stop
"%~dp0octopus-roam.exe" desktop stop --runtime "%~dp0."
goto result
:stop_disable
"%~dp0octopus-roam.exe" desktop stop --runtime "%~dp0." --autostart
:result
if errorlevel 1 echo 操作未完成，请查看上方提示及 logs 文件夹。
goto menu
:missing
echo Extract the entire Windows ZIP into one folder, then double-click octopus-roam.cmd.
pause
exit /b 1
:failed
if defined OCTOPUS_ROAM_CODEPAGE chcp %OCTOPUS_ROAM_CODEPAGE% >nul
exit /b 1
:done
if defined OCTOPUS_ROAM_CODEPAGE chcp %OCTOPUS_ROAM_CODEPAGE% >nul
exit /b 0
