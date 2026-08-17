# OmniBridge 0.4.1-alpha1 — Этап 2: Persistence 2.0

## Цель

Перевести долговременное состояние OmniBridge с набора независимых JSON-файлов на SQLite, сохранив обратную совместимость, безопасную миграцию и возможность rollback.

## Основной файл

`data/omnibridge.db`

На Windows Core использует системный `winsqlite3.dll`, поэтому для release x64 не требуется отдельная DLL и не требуется CGO. В Linux/dev validation используется системная `libsqlite3` через CGO. В `go.mod` не добавлены сторонние SQLite-модули.

## Режим SQLite

При открытии БД Core устанавливает:

- `journal_mode=WAL`
- `synchronous=NORMAL`
- `foreign_keys=ON`
- `busy_timeout=5000`

Схема имеет собственный `PRAGMA user_version` и таблицу `schema_migrations`.

## Versioned migrations

`internal/storage/migrations.go` является последовательным migration registry. Перед изменением уже существующей SQLite schema Core делает `wal_checkpoint(TRUNCATE)` и создаёт рядом backup вида:

`omnibridge.db.schema<N>.<UTC timestamp>.bak`

БД с schema version новее, чем понимает текущий Core, не открывается, чтобы старая версия приложения не испортила данные новой.

## Доменные таблицы поколения 1.0

Schema v1 заранее создаёт каркас:

- `providers`
- `accounts`
- `routing_state`
- `models`
- `model_capabilities`
- `doctor_results`
- `eval_results`
- `usage_events`
- `usage_aggregates`
- `connection_health`
- `quota_snapshots`
- `pricing`
- `circuit_breakers`
- `account_sessions`
- `settings`
- `legacy_documents`
- `schema_migrations`

Не все будущие домены уже используются бизнес-логикой этапа 2; таблицы созданы как стабильная основа следующих этапов ТЗ.

## Миграция rc6 JSON → SQLite

При первом запуске, если `legacy_import_completed` ещё не установлен, Core ищет:

- `config.json`
- `model-registry.json`
- `usage-stats.json`
- `connection-health.json`

До импорта создаётся каталог:

`data/migration-backups/pre-sqlite-<UTC timestamp>/`

с копиями найденных JSON.

`secrets.json` намеренно не копируется и не импортируется в SQLite.

После проверки JSON импорт выполняется одной SQLite transaction. При любой ошибке применяется `ROLLBACK`, а исходные JSON и pre-migration backup остаются нетронутыми.

Повторный запуск не дублирует события: импорт помечается `legacy_import_completed=1`, а historical event IDs детерминированы.

## Compatibility layer

На этапе 2 SQLite становится каноническим persistence backend для runtime, однако старые JSON продолжают обновляться как compatibility mirrors:

- это сохраняет читаемость существующих инструментов;
- упрощает rollback к rc6;
- позволяет старому backup/diagnostics tooling продолжить работу во время перехода.

При `NewPersistent(...)` Store сначала читает snapshot из `omnibridge.db`; JSON используется как fallback только когда SQLite-документа ещё нет.

Подключены к SQLite:

- Config Store — public configuration metadata + normalized providers/accounts/routing;
- Model Registry — snapshot + normalized `models`;
- Statistics — snapshot + append-only `usage_events`;
- Connection Health — snapshot + append-only `connection_health`.

DPAPI SecretStore остаётся отдельным и не переносится в SQLite.

## Транзакционная запись

Config/Registry/Stats/Health сначала фиксируют каноническое состояние в SQLite transaction и затем обновляют JSON mirror. Для Statistics и Health запись snapshot и соответствующего event выполняется одной SQLite transaction.

## Диагностика

Добавлены admin endpoints:

- `GET /bridge/storage/status`
- `POST /bridge/storage/checkpoint`

Status возвращает:

- storage schema version;
- `journal_mode=WAL`;
- `PRAGMA quick_check`;
- количество записей по основным доменным таблицам.

Секреты и локальные credential values endpoint не возвращает.

## Backup / Restore

`OmniBridgeSetup` теперь включает в data backup:

- `omnibridge.db`
- `omnibridge.db-wal`, если существует;
- `omnibridge.db-shm`, если существует;
- JSON compatibility mirrors.

Для ручного backup запущенная installed-копия кратковременно останавливается, затем запускается снова. Это исключает копирование активно изменяющегося WAL.

При восстановлении JSON-only backup существующая SQLite БД и stale WAL/SHM удаляются; на следующем запуске данные заново импортируются из JSON.

При восстановлении SQLite backup stale DB/WAL/SHM предыдущего состояния также сначала удаляются, затем копируются файлы backup.

`secrets.json` по-прежнему не входит в portable data backup.

## Windows runtime dependency

Windows 10/11 содержит `winsqlite3.dll`. `OmniBridge.exe` вызывает SQLite C API напрямую через системную DLL. Поэтому release остаётся одним runtime EXE без отдельного sqlite3.dll.

## Границы этапа

Persistence 2.0 не меняет UI и не переносит пока всю бизнес-логику на SQL queries. Runtime stores сохраняют in-memory snapshots для быстрого чтения, а SQLite является durable source of truth. Более глубокие domain repositories будут использоваться следующими этапами Provider Engine / Resilience / Router / Doctor.
