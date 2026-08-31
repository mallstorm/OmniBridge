@echo off
chcp 65001 >nul
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0interface-ru.ps1"
if errorlevel 1 (
  echo.
  echo Графический интерфейс не запустился. Откройте резервное консольное меню.
  if exist "%~dp0menu.ps1" powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0menu.ps1"
)
