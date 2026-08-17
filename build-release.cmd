@echo off
setlocal
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\build-release.ps1" -Root "%~dp0"
if errorlevel 1 (
  echo.
  echo Build failed.
  exit /b 1
)
echo.
echo Build completed successfully.
