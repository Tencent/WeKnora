@echo off
setlocal

where powershell.exe >nul 2>nul
if errorlevel 1 (
  echo [ERROR] Windows PowerShell 5.1 or later is required.
  exit /b 1
)

powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0start_all.ps1" %*
exit /b %ERRORLEVEL%
