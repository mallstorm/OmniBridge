@echo off
cd /d "%~dp0"
powershell.exe -NoProfile -STA -ExecutionPolicy Bypass -File "%~dp0scripts\desktop.ps1" -Root "%~dp0" -Page dashboard
pause
