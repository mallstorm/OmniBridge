# OmniBridge 1.0.0-rc8 — Adaptive Intelligence

## Что реализовано

- Adaptive Routing: асинхронные bounded outcome events, EWMA success/latency/TTFT, confidence и мягкий компонент `adaptive_quality` в Router Score.
- Route Explain: безопасный список кандидатов, score components, dispatched/skipped/failed/not_reached, физическая модель, fallback и количество попыток.
- Health Autopilot: dry-run, risk/evidence, `preconditions_hash` и защита от выполнения устаревшего действия.
- GigaChat OAuth singleflight: один refresh для одинаковых provider/credentials/scope/OAuth URL, ограниченный контекстом ожидания и без кэширования ошибок.
- Quota Intelligence: reset windows, burn rate, прогноз исчерпания, reserve/priority и стратегия `quota_share`.
- Exact Response Cache: opt-in, temperature=0, non-stream, no-tools, клиентская изоляция, физическая identity в подписи, TTL/LRU и bypass-заголовок.
- Generation deduplication: bounded in-flight join для одинаковых детерминированных запросов с гарантированным завершением ожидателей.
- Context Optimizer: режимы `observe`, `safe`, `balanced`, `experimental`, `off`; фактическая трансформация остаётся fail-open и защищает код, diff, структуры данных и tool contracts.
- models.dev reference sync: HTTPS, timeout/cancellation, last-known-good и явный статус `declared`; verified-флаги не изменяются.
- Provider packs: Ed25519-подписанные data-only manifests, schema/URL/auth validation, без исполняемого кода.
- VS Code self-test: фактический вызов только `ChatVerified` модели с возвратом provider/native/upstream identity.

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

Все `/bridge/*` endpoints требуют Admin API key. Prompt/response bodies, Authorization, cookies и credential не включаются в route traces.

## Гарантии идентичности

Справочный каталог не изменяет `ChatVerified`, `IdentityVerified`, `ToolCalling` или `AgentReady`. Auto routes продолжают требовать Doctor evidence, а VS Code export включает только `ChatVerified` модели. Exact cache и request dedup подписываются физическим provider/model и версией identity, поэтому ответ одной модели не может быть возвращён под именем другой.

## Миграция

Config schema обновлена с 5 до 6, SQLite schema — с 3 до 4. Миграция создаёт `routing_quality`, `route_decisions`, `exact_response_cache` и `catalog_metadata`, не удаляя настройки RC7. До изменения существующей SQLite базы создаётся резервная копия.

## Ограничения RC8

- Семантическая оценка учитывается только при передаче результата отдельного evaluator; HTTP 200 не считается доказательством качества.
- models.dev остаётся справочным источником. Автоматическая публикация каталожных capabilities как проверенных запрещена.
- `balanced` и `experimental` не переписывают tool schemas. Экспериментальное сжатие обычного текста допускается только после добавления обратимого preview/diff pipeline.
- Provider packs валидируются, но не исполняют код и не переопределяют встроенные Yandex AI Studio и GigaChat адаптеры.
