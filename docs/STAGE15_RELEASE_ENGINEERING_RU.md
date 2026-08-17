# Этап 15 — Release Engineering / OmniBridge 1.0 RC

Версия: `1.0.0-rc1`.

## Цель

Свести архитектурную ветку 0.4.x в первый релизный кандидат поколения 1.0 без очередного редизайна и без ослабления rollback/security гарантий.

## Release identity

Пакет содержит `VERSION.txt` и машиночитаемый `RELEASE.json` с channel=`rc`, версиями config/SQLite schema, Protocol Engine/Router runtime и минимальной поддерживаемой точкой upgrade (`0.3.2-rc6`). Все runtime-файлы покрываются `SHA256SUMS.txt`.

## Readiness

Добавлены:

- `GET /readyz` — безопасный readiness без API-ключа: версия, SQLite quick_check, network bind policy и целостность релизного runtime;
- `GET /bridge/release-readiness` — тот же отчёт через Admin plane.

`/health` остаётся дешёвым liveness endpoint.

## Installer/update hardening

`Test-NewCore` больше не принимает любой HTTP 200 на `127.0.0.1:4142`. Он обязан увидеть точную версию из `VERSION.txt`, живой только что запущенный процесс и успешный `/readyz`. Это закрывает сценарий, когда старый Core удерживает порт, новый EXE завершается, а maintenance ошибочно считает update успешным.

RC package verification проверяет VERSION/RELEASE identity, SHA-256 manifest и отсутствие credential-like файлов. Старые rollback snapshots остаются совместимыми и не обязаны иметь `RELEASE.json`.

`New-AppSnapshot` теперь включает `backup.cmd`, `RELEASE.json`, validation report и SHA manifest вместе с остальным maintenance payload.

## SQLite corruption policy

Release tests подтверждают fail-closed поведение: файл, который не является валидной SQLite DB, отклоняется при `storage.Open` и не перезаписывается. Автоматического «лечения» ценой тихой потери данных нет. Restore продолжает использовать проверенный backup + health/readiness + automatic rollback.

## Security gates

- runtime `secrets.json` остаётся только в `data/` и защищается DPAPI на Windows;
- release package не может содержать `secrets.json`, `.env`, `credentials.json`, `.pfx`, `.p12`, `.key`;
- manifest path traversal и hash tampering отклоняются;
- Remote mode по-прежнему fail-closed и требует TLS + CIDR allow-list.

## UI policy

UI 3.0 не перерабатывается. Shell остаётся `1260×740`, Tray Dashboard — `1000×672`, существующая внутренняя разметка и Theme сохраняются.
