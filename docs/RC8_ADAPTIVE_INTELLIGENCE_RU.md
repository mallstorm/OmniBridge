# OmniBridge 1.0.0-rc8 — Adaptive Intelligence

## Что реализовано

- Adaptive Routing: bounded async outcome events, EWMA success/latency/TTFT, confidence и мягкий компонент `adaptive_quality` в Router Score.
- Route Explain: безопасный список кандидатов, score components, dispatched/skipped/failed/not_reached, физическая модель, fallback и количество попыток.
- Health Autopilot: dry-run, risk/evidence, `preconditions_hash` и защита от выполнения устаревшего действия.
- GigaChat OAuth singleflight: один refresh для одинаковых provider/credentials/scope/OAuth URL, ограниченный контекстом ожидания и без кэширования ошибок.
- Quota Intelligence: reset windows, burn rate, прогноз исчерпания, reserve/priority и стратегия `quota_share`.
- Exact Response Cache: opt-in, только явно заданный `temperature=0`, non-stream, no-tools, клиентская/physical identity изоляция, TTL/LRU и bypass-заголовок.
- Generation deduplication: bounded in-flight join только для явно детерминированных запросов, с короткоживущим completion handoff и очисткой expired results.
- Context Optimizer: режимы `observe`, `safe`, `balanced`, `experimental`, `off`; safe-трансформация остаётся prompt-neutral, защищает code/diff/structured data и полный tool-call/tool-result contract.
- models.dev reference sync: HTTPS, timeout/cancellation, redirect validation, SSRF-защита для локальных/private literal targets, persistent last-known-good и явный статус `declared`; verified-флаги не изменяются.
- Provider packs: Ed25519-подписанные data-only manifests, schema/URL/auth validation, без исполняемого кода.
- VS Code self-test: фактический вызов только `ChatVerified` модели с возвратом provider/native/upstream identity.
- Release engineering: fail-closed formatting/vulnerability gates, PowerShell 5.1 + DPAPI acceptance, SPDX 2.3 SBOM и toolchain provenance.

## API

- `GET|PUT /bridge/router/quality`
- `GET /bridge/router/explain?trace_id=...`
- `GET /bridge/health/autopilot`
- `POST /bridge/health/autopilot/actions`
- `GET|PUT|DELETE /bridge/cache`
- `GET|PUT /bridge/context-optimizer`
- `POST /bridge/context-optimizer/preview`
- `GET|PUT|POST /bridge/catalog-sync`
- `POST /bridge/provider-packs/validate`
- `POST /bridge/vscode/self-test`

Все `/bridge/*` endpoints требуют Admin API key. Prompt/response bodies, Authorization, cookies и credentials не включаются в route traces.

## Гарантии идентичности

Справочный каталог не изменяет `ChatVerified`, `IdentityVerified`, `ToolCalling` или `AgentReady`. Auto routes продолжают требовать Doctor evidence, а VS Code export включает только `ChatVerified` модели. Exact cache и request dedup подписываются namespace и физическим provider/model/identity, поэтому ответ одной модели не должен возвращаться под именем другой.

## Exact Cache contract

Exact Cache выключен по умолчанию и допускается только когда клиент явно передал `temperature=0`. Отсутствующий `temperature`, `null`, ненулевое значение, streaming и tools считаются некэшируемыми. Cache использует TTL и LRU; completion-handoff для dedup имеет отдельный короткий TTL.

## Context Optimizer contract

`safe` режим не вставляет собственные system/developer messages. Он не удаляет system/developer instructions, tool schemas, `tool_calls`, `tool_call_id`, `functions_state_id`, typed `tool_use`/`tool_result`/function blocks, code/diff/structured markers. Фактическое сокращение в RC8 ограничено доказанными дубликатами plain-text history вне recent window.

`balanced` и `experimental` не получают права переписывать tool schemas или tool history. Более агрессивная семантическая компрессия остаётся за пределами RC8 до появления обратимого preview/diff pipeline.

## models.dev / last-known-good

Catalog sync принимает только абсолютный публичный HTTPS URL, повторно проверяет redirect targets и ограничивает число redirect. Literal localhost/private/link-local/metadata-style targets и URL userinfo отклоняются. Успешный reference snapshot сохраняется в SQLite и восстанавливается после рестарта как last-known-good; SourceURL сохранённого snapshot отражает фактический источник этих данных.

## Миграция

Config schema обновлена с 5 до 6, SQLite schema — с 3 до 4. Миграция создаёт `routing_quality`, `route_decisions`, `exact_response_cache` и `catalog_metadata`, не удаляя настройки RC7. До изменения существующей SQLite базы создаётся резервная копия.

## Windows acceptance

GitHub Actions run #160 подтвердил Windows Server 2025 / Go 1.27.0: `go test ./...`, `go test -race ./...`, `go vet ./...`, `govulncheck v1.7.0`, PowerShell 7 UI validation, Windows PowerShell 5.1 UI validation, DPAPI round-trip/invalid blob tests и Windows x64 release build. Release ZIP создаётся успешно; upload artifact остаётся заблокирован внешней GitHub Actions storage quota.

## Ограничения RC8

- Семантическая оценка учитывается только при передаче результата отдельного evaluator; HTTP 200 не считается доказательством качества.
- models.dev остаётся справочным источником. Автоматическая публикация каталожных capabilities как проверенных запрещена.
- Provider packs валидируются, но не исполняют код и не переопределяют встроенные Yandex AI Studio и GigaChat adapters.
- Interactive Desktop/Tray smoke и live-provider acceptance требуют реальной пользовательской Windows-сессии/credentials.
- Signed updater trust chain, Authenticode и anti-downgrade gate остаются P0 стабильного `1.0.0`.
