@echo off
cd /d "%~dp0"
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\model-doctor.ps1" -Root "%~dp0"
pause
