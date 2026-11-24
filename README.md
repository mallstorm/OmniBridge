# OmniBridge — Universal AI Gateway

**OmniBridge** — локальный Windows AI Gateway для подключения разных AI-провайдеров и аккаунтов через единый OpenAI-compatible endpoint, Model Registry, Model Doctor, Router и интеграцию с VS Code.

## Текущий baseline

**Каноническая версия: `1.0.0-rc8`** (`VERSION.txt`, `RELEASE.json`).

С 17 августа 2026 года проект (тогда RC5) развёрнут как полноценный исходный проект непосредственно в ветке **`main`**. Для разработки больше не требуется скачивать и распаковывать ZIP-архив.

Главная рабочая база теперь — **исходники в корне репозитория**.

```text
OmniBridge/
├─ cmd/                     # точки входа приложений и Windows binaries/resources
├─ internal/                # Core, providers, router, protocol, storage, stats, security
├─ assets/                  # иконки, provider/model assets, certificates
├─ scripts/                 # PowerShell setup/build/validation/integration scripts
├─ ui/                      # WPF XAML UI 3.0 и Tray Dashboard
├─ docs/                    # ТЗ, история этапов, RC/stage documentation
├─ data/                    # runtime data placeholder
├─ logs/                    # runtime logs placeholder
├─ .github/workflows/       # CI для проверки проекта прямо из main
├─ go.mod
├─ RELEASE.json
├─ IMPLEMENTATION_MATRIX.md
├─ README_RU.md
├─ VERSION.txt
└─ *.cmd                    # Windows launch/setup/service helpers
```

## Архивы

Каталог `archives/` оставлен только как резервная история исходных baseline:

- `archives/OmniBridge_1.0.0-rc4_SOURCE.zip` — immutable recovery baseline;
- `archives/OmniBridge_1.0.0-rc5_SOURCE.zip` — архивная копия текущего RC5.

**Архив RC5 больше не является основной точкой разработки.** Все следующие изменения должны вноситься непосредственно в исходное дерево ветки `main`.

RC4 SHA-256:

`da6f35c3adba1b12e59cace307f6991836e4c4e345ce79e49eb48b460c830623`

RC5 source archive SHA-256:

`6c4062471e3b95bff710e5a2ff639eeb412f3ab9999e0edc464bfb01ded776d0`

Сохранённый ранее `OmniBridge_1.0.0-rc4_SOURCE(1).zip` был сверен с GitHub RC4 и совпал байт-в-байт: размер `4 364 015` байт и Git blob SHA-1 `10c306789bcb4e8c93d32cd3477f30d3e0aef97d`.

## RC5 — Yandex AI Studio

В Core присутствует native provider `yandex`:

- `Authorization: Api-Key` формируется внутри OmniBridge;
- Folder ID передаётся отдельно через `OpenAI-Project`;
- API key хранится через Secret Store / `CredentialRef`;
- Models API;
- Responses API;
- Chat Completions fallback;
- model URI сохраняется без подмены;
- cached-input telemetry учитывается для Chat и Responses;
- Model Doctor проверяет preferred protocol и предупреждает о платных probes.

Основная реализация находится в `internal/providers/yandex.go`, тесты — в `internal/providers/yandex_test.go` и связанных server/provider tests.

## Heavy-load stabilization

RC5 ограничивает конкурентные тяжёлые операции вместо бесконтрольного роста нагрузки:

- generation / embeddings / WebSocket — до `16` параллельных операций;
- Model Doctor — до `2` probes;
- model discovery — до `4` операций;
- при насыщении возвращается HTTP `429 gateway_busy` + `Retry-After`;
- request/background/tray panic recovery;
- пакетная запись snapshots статистики;
- немедленная запись usage events;
- `Stats.Flush()` при штатном завершении.

## Архитектура

OmniBridge включает:

1. **Provider Registry** — API-провайдеры и локальные runtime.
2. **Account Registry** — подключения через пользовательские сессии.
3. **Model Registry** — discovery, capabilities, состояние и единый каталог моделей.
4. **Model Doctor** — проверки chat, streaming, tools, reasoning, protocol и context limits.
5. **Router** — direct, priority, fastest, agent/local/cloud и автоматические стратегии.
6. **Protocol Engine** — OpenAI Chat/Responses, Anthropic Messages, Gemini, GigaChat и compatible endpoints.
7. **VS Code Connector** — локальный OmniBridge endpoint без раскрытия provider credentials.
8. **Security / Persistence / Observability** — Secret Store, SQLite/WAL, statistics, logs, backup/restore/update/rollback.
9. **Desktop UI + Tray Dashboard**.

Основной поток:

`API key / account → discovery → Model Doctor → Model Registry → Router → localhost OmniBridge endpoint → VS Code / client`

## CI / GitHub Actions

Основной workflow: `.github/workflows/validate.yml`.

Он работает **не с архивом**, а непосредственно с исходниками текущего `main` и на Windows выполняет:

1. checkout исходников;
2. регенерацию Windows resources;
3. `go test ./...`;
4. `go vet ./...`;
5. проверку XAML и PowerShell;
6. сборку Windows x64 через `scripts/build-release.ps1`;
7. загрузку Windows release artifact.

Таким образом, изменение кода в `main` сразу проверяется как настоящий проект.

## История базовых линий

- `0.3.0-alpha27` — последний старый рабочий UI baseline до native Win32 экспериментов.
- `0.4.0-alpha28`, `alpha29`, `alpha30` — **отброшенная линия**, не использовать как baseline.
- `0.3.1-alpha31` — восстановленная и закреплённая UI 3.0 линия.
- `0.3.2-rc1` — Router/Core/Protocol release candidate.
- `0.3.2-rc2` — невалидная сборка из-за UTF-8 без BOM в `desktop.ps1`, не использовать.
- `0.3.2-rc3`–`rc6` — стабилизация alpha31-derived ветки.
- `0.4.1-alpha1` … `0.4.13-alpha13` — Persistence, Provider Engine, Resilience, Router 3.0, Doctor-aware routing, Protocol Engine, transport, quota/context/integration/MCP/observability.
- `1.0.0-rc1`–`rc4` — release engineering, VS Code picker/auth, GigaChat TLS и Model UI fixes.
- `1.0.0-rc5` — Yandex AI Studio + heavy-load stabilization.
- `1.0.0-rc6` — Yandex AI Studio: читаемые имена моделей, VS Code, маршрутизация по native-URI (`docs/RC6_VSCODE_YANDEX_FIX_RU.md`).
- `1.0.0-rc7` — Verified Models: раздельные состояния Discovery / Chat Verified / Agent Ready / Identity Verified, фактическая identity в ответах (`docs/RC7_VERIFIED_MODELS_IDENTITY_RU.md`).
- `1.0.0-rc8` — **текущий source baseline в `main`**: Cline Bridge (проверка Cline API, копирование Local Bridge Key), встроенное обслуживание в `OmniBridge.exe`, раздельные Runtime/Source-архивы (`docs/RC8_CLINE_BRIDGE_RU.md`).

## UI contract

Основная визуальная линия сохраняется от alpha31 и не должна самопроизвольно редизайниться.

- Desktop UI: UI 3.0 / alpha31-derived.
- Рекомендуемый размер главного окна при расширении сервисов: `1260×740`.
- Tray Dashboard: `1000×672`.
- Допускаются точечные функциональные дополнения без изменения утверждённого визуального лица приложения.

## Документация

Полное исходное ТЗ и архитектура: `docs/OMNIBRIDGE_PRODUCT_SPEC.md`.

История разработки и контекст Yandex: `docs/PROJECT_HISTORY_AND_YANDEX_CONTEXT_RU.md`.

Дополнительно в `docs/` сохранены документы всех крупных Stage 1–15, RC2/RC3/RC4/RC5, Router, Tray Dashboard, UI reference и эксплуатационная документация.

## Правило дальнейшей разработки

**Разрабатываем OmniBridge непосредственно в `main` от текущего RC8 source tree.**

`archives/` — только резервные snapshots. RC4 остаётся recovery-копией. Экспериментальные `0.4.0-alpha28`–`alpha30` не должны использоваться для восстановления или дальнейшей разработки.
