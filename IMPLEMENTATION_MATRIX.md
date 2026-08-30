# OmniBridge 1.0.0-rc8 — дорожная карта поколения 1.0

| Этап | Статус | Результат |
|---|---|---|
| 1. RC Stabilization | **Выполнен в 0.3.2-rc6** | Account preflight/login hardening, Tray regression gates, maintenance rollback hardening |
| 2. Persistence 2.0 | **Выполнен в 0.4.1-alpha1** | SQLite WAL, migrations, JSON import, domain schema, persistent Config/Models/Stats/Health, backup/restore |
| 3. Provider Engine 3.0 | **Выполнен в 0.4.2-alpha2** | Data-driven provider catalog, Executor Registry, TargetPlan, templates + same-origin verification, request sanitizer |
| 4. Resilience 3.0 | **Выполнен в 0.4.3-alpha3** | Persistent breaker, failure taxonomy, DEGRADED/HALF_OPEN, adaptive cooldown, connection pools, single-flight |
| 5. Router 3.0 | **Выполнен в 0.4.4-alpha4** | Weighted/RR/Least Used/P2C/Quota/Cost-ready/Auto, virtual models, session affinity |
| 6. Doctor-Aware Routing | **Выполнен в 0.4.5-alpha5** | Request capability inference, deep Doctor proof, Doctor/Agent/Coding/Reliability scores, context preflight |
| 7. Functional UI Expansion | **Выполнен в 0.4.6-alpha6** | 1260×740 без редизайна, Router service, Doctor scores, capability/context detail, Connection Pool/Resilience, dry-run |
| 8. Protocol Engine 3.0 | **Выполнен в 0.4.7-alpha7** | Canonical model, Translator Registry, lossless same-protocol sanitizer, tool/schema/reasoning normalization |
| 9. Streaming + WebSocket | **Выполнен в 0.4.8-alpha8** | Live cross-protocol SSE, canonical stream adapters, `/v1/ws` |
| 10. Quota & Cost | **Выполнен в 0.4.13-alpha13** | Verified/operator quota, pricing registry, cost events, budgets, Router quota/cost hints |
| 11. Context Guard | **Выполнен в 0.4.13-alpha13** | Context-fit, conservative token estimate, observe/compact, persistent savings telemetry |
| 12. Integration Hub | **Выполнен в 0.4.13-alpha13** | VS Code safe-merge + detection/connection plans for Cursor/Continue/Cline/Roo/Codex/Claude/OpenCode/OpenClaw |
| 13. MCP Control Plane | **Выполнен в 0.4.13-alpha13** | Persistent MCP registry/probes, HTTP/SSE/streamable-http/stdio definitions, fail-closed process policy |
| 14. Observability & Remote | **Выполнен в 0.4.13-alpha13** | Safe request tracing, P50/P95 telemetry, Prometheus metrics, TLS + CIDR fail-closed remote mode |
| 15. 1.0 Release Engineering | **Выполнен в 1.0.0-rc1; RC2 integration patch** | release metadata, package verifier, readiness gate, version-aware install/update rollback, corruption fail-closed, VS Code Custom Endpoint picker/export hardening |
| 16. Yandex + Stability hardening | **Выполнен в 1.0.0-rc5** | Native Yandex AI Studio, Api-Key + Folder ID, Responses/Chat, cached-token telemetry, overload limits, panic isolation, batched statistics |
| 17. Yandex/VS Code + Release hardening | **Выполнен в 1.0.0-rc6** | Канонические Yandex URI/имена, фильтр chat-моделей для VS Code, UI `$PID` fix, version consistency, Windows SQLite REAL fix |
| 18. Verified Models & Identity | **Выполнен в 1.0.0-rc7** | Chat/Identity verification, physical upstream ID, verified VS Code export, identity envelope и response metadata |
| 19. Adaptive Routing & Explainability | **Выполнен в 1.0.0-rc8** | EWMA quality/confidence, bounded outcomes, route decision trace, explain API, Health Autopilot |
| 20. Quota & Efficiency Intelligence | **Выполнен в 1.0.0-rc8** | GigaChat token singleflight, quota-share/burn forecast, generation dedup, Exact Response Cache |
| 21. Safe Extensibility | **Выполнен в 1.0.0-rc8** | Safe Context Optimizer, models.dev reference sync, signed data-only provider packs, VS Code self-test |

## Этапы 19–21 — Adaptive Intelligence

`1.0.0-rc8` добавляет эксплуатационную обратную связь, не ослабляя RC7 Doctor/identity gates. Adaptive score является только мягким коэффициентом; auth, quota, breaker, capability, context и Doctor остаются жёсткими ограничениями. Подробности: `docs/RC8_ADAPTIVE_INTELLIGENCE_RU.md`.

## Этап 17 — Yandex/VS Code + Release hardening

`1.0.0-rc6` устраняет расхождения native model URI между Yandex discovery, Model Registry, VS Code export и входящими запросами. Не-чатовые модели больше не попадают в Chat picker. Версия синхронизирована между Core, `VERSION.txt`, `RELEASE.json`, XAML и Windows manifest; отдельный тест блокирует повторный version drift. Windows SQLite сохраняет одинаковый тип `float64` для `REAL`, как и cgo-реализация.

## Этап 16 — Yandex AI Studio + Stability hardening

`1.0.0-rc5` закрывает провайдерную интеграцию Yandex без прямого редактирования VS Code BYOK-файлов: Yandex API Key остаётся в Secret Store, Folder ID преобразуется в `OpenAI-Project`, модели обнаруживаются через официальный OpenAI-compatible Models API. Введены bounded concurrency для generation/probe/discovery, panic isolation и coalesced persistence статистики. UI 3.0 и Tray не переделывались.



## Этап 15 — 1.0 Release Engineering

`1.0.0-rc1` завершает дорожную карту: пакет получает `RELEASE.json`, SHA-256 и secret-hygiene gates; installer проверяет точную запущенную версию и `/readyz`; runtime публикует безопасный readiness и admin release report; повреждённая SQLite БД отклоняется без перезаписи исходного файла; rollback snapshot включает весь maintenance payload. Подробности: `docs/STAGE15_RELEASE_ENGINEERING_RU.md`.

## Этапы 10–14 — единый крупный проход

`0.4.13-alpha13` закрывает пять связанных слоёв одним совместимым контрактом: Router получает реальные quota/cost hints; Context Guard работает перед Doctor-Aware routing; Integration Hub и MCP используют тот же защищённый Core; Observability связывает запросы trace-id и сохраняет только безопасную метаинформацию. В UI 3.0 добавлена страница «Сервисы», а Integration Hub выведен на существующую страницу «Интеграция». Подробности: `docs/BATCH_STAGES10_14_RU.md`.

## Этап 9 — Streaming + WebSocket

`0.4.8-alpha8` переводит cross-protocol streaming на live `CanonicalStreamEvent`: upstream SSE декодируется и сразу кодируется в downstream Chat/Responses/Messages SSE без ожидания полного ответа. Добавлен `GET /v1/ws` — WebSocket Stream Transport v1 с одним request frame и protocol-neutral canonical event frames. Реализованы text/reasoning/tool/usage/finish/done events, cancellation/backpressure через synchronous streaming loop и снят whole-request timeout для live provider/account streams. Native provider-specific upstream WebSocket transports не подменяются универсальной реализацией и будут добавляться только там, где у официального API есть определённый WS contract. Подробнее: `docs/STAGE9_STREAMING_WEBSOCKET_RU.md`.

## Этап 8 — Protocol Engine 3.0

`0.4.7-alpha7` вводит отдельный canonical protocol layer (`CanonicalRequest/Response/StreamEvent`), Translator Registry, lossless same-protocol normalization, centralized tool-history/schema/structured-output/reasoning compatibility и admin diagnostics `/bridge/protocol-engine`. Live cross-protocol streaming намеренно остаётся задачей Stage 9. Подробнее: `docs/STAGE8_PROTOCOL_ENGINE3_RU.md`.

## Этап 7 — Functional UI Expansion

`0.4.6-alpha6` впервые выводит наружу Router 3.1 и Doctor-Aware данные без изменения визуального лица OmniBridge. Shell увеличен до `1260×740`; sidebar, нижняя action-панель, карточки, цвета и типографика UI 3.0 сохранены. Добавлена отдельная страница «Маршрутизация», расширены Models и Model Doctor. Подробнее: `docs/STAGE7_FUNCTIONAL_UI_EXPANSION_RU.md`.

## Этап 6 — Doctor-Aware Routing

`0.4.5-alpha5` связывает Router с deep Model Doctor evidence. `auto/*` больше не выбирает непроверенную модель для запроса, который требует Tools/Vision/Reasoning/Agent/Structured capability. Добавлены request requirements, context/output preflight, четыре технических score и dry-run `/bridge/router/evaluate`. Deep Doctor state сохраняется при Model Registry refresh. Подробнее: `docs/STAGE6_DOCTOR_AWARE_ROUTING_RU.md`.

## Этап 5 — Router 3.0

Новый `internal/router` является отдельным runtime engine и не зависит напрямую от provider/stats/resilience packages. Server передаёт в него безопасную проекцию кандидата с capability/health/telemetry/hints.

В `/v1/models` добавлены `auto/smart`, `auto/coding`, `auto/fast`, `auto/cheap`, `auto/local`, `auto/reasoning`, `auto/vision`, `auto/agent`. Session affinity хранится только в памяти и использует hashed session key.

`quota_optimized`/`cost_optimized` уже готовы архитектурно, но Stage 5 не считает неизвестные данные точными: до этапа 10 они используют только явные operator hints. Подробнее: `docs/STAGE5_ROUTER3_RU.md`.

## UI policy

Согласован Functional UI Expansion без редизайна: при появлении новых сервисов основное окно может быть увеличено до `1260×740`, при этом сохраняются нынешние sidebar, карточки, типографика, цвета, отступы и общая внутренняя разметка UI 3.0.
