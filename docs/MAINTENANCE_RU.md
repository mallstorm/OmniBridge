# OmniBridge — установка, backup, restore, update и rollback

`OmniBridge.exe` остаётся основным приложением. `OmniBridgeSetup.exe` — отдельная утилита обслуживания релиза и не участвует в обычной работе Gateway/UI.

## Portable
Распакуйте ZIP и запускайте `OmniBridge.exe` из папки. `data/` и `logs/` остаются рядом.

## Installed mode
Запустите `install.cmd` или `OmniBridgeSetup.exe --install`. Файлы приложения устанавливаются в `%LOCALAPPDATA%\OmniBridge`. Уже существующая `data/` не заменяется. Перед заменой файлов создаётся rollback snapshot.

## Repair
`repair.cmd` повторно копирует бинарники, UI, scripts и assets из текущего release package, не стирая пользовательские данные.

## Backup
`backup.cmd` или `OmniBridgeSetup.exe --backup` создаёт переносимую резервную копию данных. Резервная копия поколения Persistence 2.0 содержит `omnibridge.db` (и WAL/SHM, если они существуют), а также совместимые зеркала `config.json`, `model-registry.json`, `usage-stats.json` и `connection-health.json`. Перед ручным backup работающая копия кратковременно останавливается и затем автоматически запускается снова, чтобы SQLite/WAL были согласованы. `secrets.json` сознательно не попадает в переносимый backup: он защищён Windows DPAPI CurrentUser.

## Restore
`restore.cmd` открывает выбор ZIP backup. Перед восстановлением автоматически создаётся ещё один backup текущего состояния. Secrets не перезаписываются. Если восстанавливается старый JSON-only backup, существующий `omnibridge.db` удаляется и на следующем старте выполняется безопасный JSON → SQLite import; если backup уже содержит `omnibridge.db`, stale WAL/SHM предыдущей установки предварительно удаляются.

## Update
`update.cmd <release.zip>` или `OmniBridgeSetup.exe --update <release.zip>`:
1. Распаковывает пакет во временный staging.
2. Проверяет наличие `OmniBridge.exe` и `SHA256SUMS.txt`.
3. Создаёт pre-update rollback snapshot текущих программных файлов.
4. Останавливает установленную копию.
5. Заменяет программные файлы, сохраняя `data/` и `logs/`.
6. Запускает новую версию и проверяет `GET /health` на `127.0.0.1:4142`.
7. При неуспешном health-check возвращает предыдущие файлы и запускает предыдущую версию.

## Uninstall
`uninstall.cmd` сначала сохраняет переносимый backup данных, затем удаляет установленную копию и ярлык Start Menu.

## 1.0 RC: verify и transactional rollback

`OmniBridgeSetup.exe --verify` выполняет offline-проверку RC package: `VERSION.txt`/`RELEASE.json`, SHA-256 manifest и secret-hygiene.

Начиная с `1.0.0-rc1`, install/repair/update перед заменой файлов останавливает Core и при наличии существующей установки создаёт **две** точки отката:

1. app snapshot — EXE/UI/scripts/docs/maintenance payload;
2. data snapshot — SQLite + WAL/SHM + compatibility JSON (без `secrets.json`).

Это важно для миграций SQLite. Если новая версия успела поднять schema, но затем не прошла release readiness, rollback восстанавливает не только старый EXE, но и старую БД. После возврата запускается старый Core и снова проходит version-aware health/readiness gate.

`Test-NewCore` больше не считает любой ответ на `127.0.0.1:4142` успехом. Версия `/health` должна точно совпасть с `VERSION.txt`, процесс должен оставаться живым, после чего `/readyz` должен вернуть `ok=true` той же версии.
