# OmniBridge 1.0.0-rc6 — Universal AI Gateway

## 1.0.0-rc6 — Yandex model identity, VS Code и release hardening

`1.0.0-rc6` нормализует Yandex model URI и читаемые имена моделей, разрешает запросы по полному native URI и короткому slug, исключает не-чатовые Yandex endpoints из VS Code Chat export и исправляет PowerShell-конфликт с зарезервированной переменной `$PID`. Дополнительно синхронизированы версия Core, стартовые XAML-экраны и Windows manifest, исправлено чтение SQLite `REAL` на Windows и добавлены автоматические regression-тесты. Подробнее: `docs/RC6_VSCODE_YANDEX_FIX_RU.md` и `docs/AUDIT_2026-08-29_RU.md`.

## 1.0.0-rc5 — Yandex AI Studio + Stability

`1.0.0-rc5` добавляет Yandex AI Studio как отдельный native provider, а не как ручной Custom Endpoint. В форме API-подключения пользователь вводит Yandex API Key и Folder ID; Core формирует `Authorization: Api-Key`, `OpenAI-Project`, получает модели через `/v1/models` и маршрутизирует как Responses API, так и Chat Completions. API key хранится в защищённом Secret Store, Folder ID — в конфигурации профиля.

Prompt cache Yandex применяется самим AI Studio. OmniBridge сохраняет стабильные system/tool-префиксы, не создаёт скрытых повторных LLM-запросов и теперь учитывает `cached_tokens` из Chat/Responses usage в статистике и расчёте стоимости. Для Deep Model Doctor по Yandex проверяется основной протокол и показывается предупреждение о платных диагностических вызовах.

Под тяжёлой нагрузкой Core ограничивает одновременно выполняемые генерации и глубокие probes, discovery провайдеров выполняется максимум в четыре потока, HTTP panic изолируется и логируется без остановки процесса, а полная статистика записывается пакетно при немедленном сохранении отдельных usage events. UI 3.0 и Tray сохранены без редизайна. Подробнее: `docs/RC5_YANDEX_STABILITY_RU.md`.


## 1.0.0-rc4 — VS Code Auth + GigaChat TLS + Model UI

`1.0.0-rc4` исправляет пользовательские проблемы, найденные на реальном RC2: `invalid local bridge key` в VS Code, необходимость вручную устанавливать Russian Trusted Root CA для GigaChat и пустые/неочевидные Model Doctor scores. VS Code sync проверяет ключ против работающего Core, сверяет fingerprint, записывает literal `Authorization: Bearer` и `x-bridge-key` и очищает stale OmniBridge entries. GigaChat Core использует встроенный Russian Trusted Root CA при обычной TLS validation. Models/Doctor получили family/provider icons, корректное обновление четырёх score и явное состояние `Не проверено`. Весь UI стал светлее, а шрифт увеличен на +1 без изменения компоновки 1260×740. Подробнее: `docs/RC4_VSCODE_EXPLICIT_AUTH_FIX_RU.md`.

## Первый кандидат поколения 1.0

`1.0.0-rc1` сводит этапы 1–15 в релизную ветку. Добавлены self-described `RELEASE.json`, строгая offline-проверка пакета, публичный локальный readiness `GET /readyz`, admin `GET /bridge/release-readiness`, version-aware installer health gate и fail-closed проверка целостности SQLite/релизного manifest. Installer больше не может принять ответ старого процесса на `127.0.0.1:4142` за успешный запуск новой версии: health-check сверяет точную версию и затем выполняет расширенный readiness. Rollback snapshots сохраняют полный maintenance payload, включая `backup.cmd`.

RC не меняет лицо UI 3.0: главное окно остаётся `1260×740`, Tray — `1000×672`.

## Пакет этапов 10–14 — Core 1.0 services

`0.4.13-alpha13` объединяет пять взаимосвязанных этапов: Quota & Cost Engine, Context Guard, Integration Hub, MCP Control Plane и Observability & Remote. Новая страница **«Сервисы»** показывает их состояние в UI 3.0 без редизайна. Quota/cost использует только явно подтверждённые/операторские данные; Context Guard не делает скрытых LLM-вызовов; Integration Hub не перезаписывает конфигурацию сторонних клиентов вслепую; MCP stdio process launch остаётся fail-closed; Remote mode требует TLS и CIDR allow-list. Подробнее: `docs/BATCH_STAGES10_14_RU.md`.

## Gateway
- `GET /health`
- `GET /readyz` — release/storage readiness
- `GET /v1/models`
- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/messages`
- `POST /v1/embeddings`
- `GET /v1/ws` — WebSocket Stream Transport v1

## Что добавлено после 0.3.1-alpha31

## Resilience 3.0

В `0.4.3-alpha3` runtime-only breaker заменён на persistent state machine `CLOSED -> DEGRADED -> OPEN -> HALF_OPEN`. Ошибки разделяются на rate-limit/quota/timeout/network/5xx/auth/model/client categories; client cancellation и invalid request не отравляют upstream health. Состояние хранится в SQLite schema 2 и переживает restart. Несколько connections с одинаковым native model ID образуют connection-pool foundation. Параллельные Model Registry/account refresh схлопываются single-flight. Diagnostics: `GET /bridge/resilience`, reset: `POST /bridge/resilience/reset`. Подробнее: `docs/STAGE4_RESILIENCE3_RU.md`.


## Provider Engine 3.0

В `0.4.2-alpha2` provider layer переведён на data-driven registry: `ProviderDefinition -> TargetPlan -> Executor -> Request Sanitizer`. Embedded catalog содержит 91 definition. Фиксированные native/preset/local подключения отделены от `template` providers, у которых endpoint зависит от аккаунта или deployment. Template перед сохранением проходит same-origin endpoint/protocol probe; OmniBridge не выдумывает универсальный Base URL. Добавлен `GET /bridge/provider-engine`. Подробнее: `docs/STAGE3_PROVIDER_ENGINE3_RU.md`.

## Persistence 2.0

Начиная с `0.4.1-alpha1`, каноническое долговременное состояние хранится в `data/omnibridge.db` (SQLite WAL). При первом запуске существующие `config.json`, `model-registry.json`, `usage-stats.json` и `connection-health.json` безопасно импортируются после создания pre-migration backup. JSON пока продолжают обновляться как compatibility mirrors. DPAPI `secrets.json` в SQLite не переносится. Подробнее: `docs/STAGE2_PERSISTENCE2_RU.md`.

Диагностика: `GET /bridge/storage/status`, checkpoint: `POST /bridge/storage/checkpoint`.


### Router 3.0
В `0.4.4-alpha4` маршрутизация вынесена в отдельный runtime engine. Поддерживаются `weighted`, `round_robin`, `least_used`, `p2c`, `quota_optimized`, `cost_optimized`, `auto` плюс прежние Direct/Priority/Fastest/Agent/Local/Cloud policies. `/v1/models` публикует виртуальные `auto/smart`, `auto/coding`, `auto/fast`, `auto/cheap`, `auto/local`, `auto/reasoning`, `auto/vision`, `auto/agent`. Добавлена hashed Session Affinity и `GET /bridge/router/diagnostics`. Quota/Cost routing до отдельного engine использует только явные operator hints и не выдаёт неизвестные значения за реальные. Подробнее: `docs/STAGE5_ROUTER3_RU.md`.

### Doctor-Aware Routing
В `0.4.5-alpha5` Router анализирует требования конкретного запроса до выбора upstream: Tools, Parallel Tools, Vision, Structured Output, Reasoning, Streaming, System Prompt и context/output budget. Deep Model Doctor сохраняет `DoctorScore`, `AgentScore`, `CodingScore`, `ReliabilityScore` и `doctor_verified`; эти результаты переживают Model Registry refresh. Для capability-sensitive `auto/*` используется fail-closed Doctor proof. Добавлен dry-run `POST /bridge/router/evaluate`. Подробнее: `docs/STAGE6_DOCTOR_AWARE_ROUTING_RU.md`.



### Streaming + WebSocket
В `0.4.8-alpha8` cross-protocol stream больше не буферизуется целиком: SSE события `chat-completions`, `messages` и `responses` декодируются в `CanonicalStreamEvent` и сразу кодируются в выбранный downstream protocol. Поддерживаются text/reasoning/tool argument deltas, usage, finish и done. `GET /v1/ws` использует subprotocol `omnibridge.stream.v1` (если клиент его предлагает): клиент отправляет один JSON request frame и получает canonical event frames. Это downstream WebSocket transport; upstream-native WS включается только для provider APIs с официально определённым WebSocket contract. Подробнее: `docs/STAGE9_STREAMING_WEBSOCKET_RU.md`.

### Protocol Engine 3.0
В `0.4.7-alpha7` protocol translation выделен в отдельный canonical layer. Добавлены `CanonicalRequest/Response/StreamEvent`, Translator Registry, lossless same-protocol normalization, stable Tool Call IDs, tool-result sequence repair, JSON Schema coercion, Chat/Responses Structured Output mapping и reasoning replay fields. Diagnostics: `GET /bridge/protocol-engine`. В `0.4.8-alpha8` Stage 9 подключает к этому контракту реальный live cross-protocol SSE и WebSocket transport; buffered synthetic SSE остаётся только fallback для upstream, который проигнорировал `stream=true` и вернул обычный JSON. Подробнее: `docs/STAGE8_PROTOCOL_ENGINE3_RU.md`.

### Functional UI Expansion
В `0.4.6-alpha6` главное окно увеличено до согласованных `1260×740` без смены дизайн-системы UI 3.0. Добавлена отдельная страница «Маршрутизация» с Auto Router diagnostics, Connection Pool/Resilience и безопасным dry-run анализа запроса. Страница «Модели» показывает Doctor/Agent/Coding/Reliability Score, расширенные capability и context/output limits; Model Doctor выводит четыре score прямо в отчёте. Подробнее: `docs/STAGE7_FUNCTIONAL_UI_EXPANSION_RU.md`.

### Account Reliability
ChatGPT/Codex получает automatic refresh attempt через официальный app-server; Gemini OAuth автоматически обновляет access token по refresh token. Состояние аккаунтов проверяется в фоне раз в 5 минут и сохраняет telemetry reauth/error/last connected.

#
## Вход через аккаунты

### ChatGPT / Codex
Перед входом OmniBridge выполняет secret-safe preflight официального Codex CLI (`--version` + app-server initialize). Обычный browser login в rc6 по умолчанию запускается через официальный `codex login`, который сам управляет browser/callback. После завершения OmniBridge проверяет сессию через `account/read`. Device Flow остаётся отдельным app-server `chatgptDeviceCode` fallback.

Важно: серверная ошибка rc5/rc4-линии, где `login/start` фактически запускал Codex login **дважды подряд**, устранена; теперь создаётся ровно одна login-сессия. Для совместимости можно явно задать `browser_backend=app-server`.

### Google Gemini
Gemini Account Connector — **Gemini Developer API OAuth**. Требуется OAuth Client JSON типа **Desktop app** из Google Cloud проекта. Перед браузером проверяются `client_id`, `project_id`, официальные Google OAuth endpoints и возможность loopback listener `127.0.0.1:<random-port>`. Используется PKCE, access/refresh token lifecycle и `x-goog-user-project`. Это Developer API OAuth, а не квота consumer-подписки Gemini AI Pro.

## Observability 2.0
`/bridge/statistics` теперь выдаёт period-specific provider/model breakdowns, AVG latency, P95 и Success Rate. Добавлена 7-дневная история connection health и категоризированные redacted logs.

### Release Engineering
Portable режим сохранён. В пакет добавлен `OmniBridgeSetup.exe` для install/repair/backup/restore/update/uninstall. Update проверяет SHA256 и автоматически откатывает программные файлы, если новая версия не проходит `/health`. Подробности: `docs/MAINTENANCE_RU.md`.

## Первый запуск — Portable
1. Распакуйте весь ZIP в отдельную папку.
2. Запустите `OmniBridge.exe`.
3. Добавьте API или войдите через поддерживаемый Account Connector.
4. Обновите Model Registry.
5. Запустите Model Doctor.
6. Экспортируйте рабочие модели через «Интеграция».

## Установка
Запустите `install.cmd` или `OmniBridgeSetup.exe --install`. Installed mode: `%LOCALAPPDATA%\OmniBridge`.

## Важно о secrets
API/OAuth/bridge credentials защищаются DPAPI CurrentUser. Переносимый backup сознательно исключает `secrets.json`; перенос на другой Windows user требует повторного ввода credentials/login.

## UI
Основной Desktop UI 3.0 сохранён от подтверждённой alpha31/alpha27 линии без редизайна. Tray при этом заменён на отдельный кастомный двухсекционный Dashboard по утверждённому макету. Левый/правый click открывает Tray Dashboard, double click — главное окно. Подробнее: `docs/TRAY_DASHBOARD_RU.md`.

Начиная с `0.4.6-alpha6` основная рабочая область составляет `1260×740`; дополнительное место используется для новых пользовательских сервисов при сохранении sidebar, карточек, типографики, цветов и общей композиции UI 3.0.

## Сборка
Исходники сохраняют совместимость языка с Go 1.23, но релизные сборки и CI должны выполняться поддерживаемой версией Go (сейчас Go 1.27.x). `build-release.cmd` выполняет tests/vet/UI validation и создаёт Windows x64 release package.


## Tray Dashboard rc6
`GET /bridge/tray` отдаёт только агрегированное состояние без secrets. Кнопка «Остановить сервер» безопасно приостанавливает data plane `/v1/*`, оставляя Tray/admin control доступными для повторного запуска. Refresh Sessions выполняется асинхронно. GigaChat card показывается только при наличии GigaChat API и различает OAuth token lifetime и API usage tokens.
