# OmniBridge — Universal AI Gateway

**OmniBridge** — локальный Windows AI Gateway для подключения разных AI-провайдеров и аккаунтов через единый OpenAI-compatible endpoint, Model Registry, Model Doctor, Router и интеграцию с VS Code.

## Текущий канонический baseline

**Актуальная проверенная исходная база: `1.0.0-rc5`.**

- Source archive: `archives/OmniBridge_1.0.0-rc5_SOURCE.zip`
- SHA-256: `6c4062471e3b95bff710e5a2ff639eeb412f3ab9999e0edc464bfb01ded776d0`
- Previous recovery baseline: `1.0.0-rc4`
- RC4 SHA-256: `da6f35c3adba1b12e59cace307f6991836e4c4e345ce79e49eb48b460c830623`
- Canonical branch: `main`

Повторная сверка выполнена **17 августа 2026 года**.

Сохранённый в ChatGPT/Library файл `OmniBridge_1.0.0-rc4_SOURCE(1).zip` сравнен с `archives/OmniBridge_1.0.0-rc4_SOURCE.zip` в репозитории. Совпадают одновременно:

- размер: `4 364 015` байт;
- SHA-256: `da6f35c3adba1b12e59cace307f6991836e4c4e345ce79e49eb48b460c830623`;
- Git blob SHA-1: `10c306789bcb4e8c93d32cd3477f30d3e0aef97d`.

То есть RC4 на GitHub сохранён **байт-в-байт без изменений**.

При этом GitHub уже содержит более новую исходную сборку `1.0.0-rc5`, поэтому накладывать локальный RC4 поверх `main` не требуется и нельзя: это было бы откатом.

## Статус RC5

`1.0.0-rc5` больше не считается только неподтверждённым кандидатом. GitHub Actions на Windows успешно выполнил полный validation/build pipeline:

- распаковка RC5 source archive;
- регенерация Windows resources;
- `go test`;
- `go vet`;
- проверка XAML и PowerShell;
- сборка Windows x64;
- публикация release-candidate artifact.

Workflow `Validate OmniBridge release archives` завершён со статусом **success**.

## Что добавлено в 1.0.0-rc5

### Yandex AI Studio

В Core добавлен native provider `yandex`:

- `Authorization: Api-Key` формируется внутри OmniBridge;
- Folder ID передаётся отдельно через `OpenAI-Project`;
- API key остаётся в Secret Store / `CredentialRef`;
- поддерживаются Models API, Responses API и Chat Completions;
- Responses используется как preferred protocol, Chat Completions — как совместимый fallback;
- model URI сохраняется без подмены;
- cached-input telemetry учитывает поля Chat и Responses;
- Model Doctor проверяет preferred protocol и предупреждает о платных probes.

### Heavy-load stabilization

RC5 ограничивает конкурентные тяжёлые операции вместо бесконтрольного роста нагрузки:

- generation / embeddings / WebSocket: до `16` параллельных операций;
- Model Doctor: до `2` probes;
- model discovery: до `4` операций;
- при насыщении возвращается HTTP `429 gateway_busy` + `Retry-After`;
- добавлены panic recovery для request/background/tray путей;
- статистика пишет полные snapshots пакетно;
- usage events сохраняются сразу;
- при штатном завершении выполняется `Stats.Flush()`.

## Основная архитектура

OmniBridge включает:

1. **Provider Registry** — API-провайдеры и локальные runtime.
2. **Account Registry** — подключения по пользовательским сессиям.
3. **Model Registry** — discovery, capabilities, состояние и единый каталог моделей.
4. **Model Doctor** — проверка chat, streaming, tools, reasoning, protocol и context limits.
5. **Router** — direct, priority, fastest, agent/local/cloud и автоматические стратегии.
6. **Protocol Engine** — OpenAI Chat/Responses, Anthropic Messages, Gemini, GigaChat и совместимые API.
7. **VS Code Connector** — экспортирует локальный endpoint OmniBridge без раскрытия внешних provider secrets.
8. **Security / Persistence / Observability** — credentials, SQLite/WAL, statistics, logs, backup/restore/update/rollback.
9. **Desktop UI + Tray Dashboard**.

Основной пользовательский поток:

`API key / account → discovery → Model Doctor → Model Registry → Router → localhost OmniBridge endpoint → VS Code / client`.

## История базовых линий

- `0.3.0-alpha27` — последний старый рабочий UI baseline до неудачной native Win32 миграции.
- `0.4.0-alpha28`, `alpha29`, `alpha30` — **отброшенная линия**, не использовать как baseline.
- `0.3.1-alpha31` — восстановленная и закреплённая UI 3.0 линия.
- `0.3.2-rc1` — Router/Core/Protocol release candidate.
- `0.3.2-rc2` — невалидная сборка из-за UTF-8 без BOM в `desktop.ps1`, не использовать.
- `0.3.2-rc3`–`rc6` — последовательная стабилизация alpha31-derived ветки.
- `0.4.1-alpha1` … `0.4.13-alpha13` — Persistence, Provider Engine, Resilience, Router 3.0, Doctor-aware routing, Protocol Engine, transport, quota/context/integration/MCP/observability.
- `1.0.0-rc1`–`rc4` — release engineering, VS Code picker/auth, GigaChat TLS и Model UI fixes.
- `1.0.0-rc5` — **текущий validated baseline**: Yandex AI Studio + heavy-load stabilization.

## UI contract

Основная визуальная линия сохраняется от alpha31 и не должна самопроизвольно редизайниться.

- Desktop UI: UI 3.0 / alpha31-derived.
- Рекомендуемый размер главного окна при расширении сервисов: `1260×740`.
- Tray Dashboard: `1000×672`.
- Допускаются точечные функциональные дополнения без изменения утверждённого визуального лица приложения.

## Репозиторий

```text
archives/
  OmniBridge_1.0.0-rc4_SOURCE.zip   immutable recovery baseline
  OmniBridge_1.0.0-rc5_SOURCE.zip   current validated source baseline

docs/
  PROJECT_HISTORY_AND_YANDEX_CONTEXT_RU.md

.github/workflows/
  validate-release-archives.yml
```

Полная история разработки, исходное ТЗ и контекст интеграции Yandex находятся в `docs/PROJECT_HISTORY_AND_YANDEX_CONTEXT_RU.md`.

## Правило дальнейшей разработки

Каноническая база дальнейшей разработки — **`OmniBridge_1.0.0-rc5_SOURCE.zip`** из ветки `main`.

RC4 хранится как неизменяемая recovery-копия. Старые экспериментальные `0.4.0-alpha28`–`alpha30` не должны использоваться для восстановления или дальнейшей разработки. Любой найденный в других ветках более поздний source archive сначала сравнивается с RC5 по содержимому и проверяется сборкой/тестами до смены canonical baseline.
