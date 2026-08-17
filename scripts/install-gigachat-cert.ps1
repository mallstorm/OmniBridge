param([string]$Root)
$ErrorActionPreference = 'Stop'
if (-not $Root) { $Root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path) }
$bundled = Join-Path $Root 'assets\certs\russian_trusted_root_ca.pem'
$target = Join-Path $Root 'data\russian_trusted_root_ca_pem.crt'
if(-not(Test-Path $bundled)){throw 'Встроенный сертификат GigaChat не найден в release package.'}
New-Item -ItemType Directory -Force -Path (Split-Path -Parent $target) | Out-Null
Copy-Item -LiteralPath $bundled -Destination $target -Force
Write-Host 'OmniBridge — GigaChat TLS' -ForegroundColor Cyan
Write-Host 'Russian Trusted Root CA уже встроен непосредственно в Core.' -ForegroundColor Green
Write-Host 'Дополнительная локальная копия CA подготовлена для совместимости старых конфигураций:'
Write-Host $target
Write-Host 'Системное хранилище сертификатов Windows не изменяется; TLS verification остаётся включённой.' -ForegroundColor Green
