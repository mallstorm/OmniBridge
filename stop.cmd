@echo off
cd /d "%~dp0"
powershell.exe -NoProfile -ExecutionPolicy Bypass -Command "$c=Get-Content -Raw '%~dp0data\config.json'|ConvertFrom-Json; try {Invoke-RestMethod -Method Post 'http://127.0.0.1:4142/control/shutdown' -Headers @{Authorization=('Bearer '+$c.admin_api_key)} -TimeoutSec 2|Out-Null}catch{}; Get-Process -Name 'OmniBridge' -ErrorAction SilentlyContinue|Stop-Process -Force"
