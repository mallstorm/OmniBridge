# OmniBridge 0.4.3-alpha3 — Этап 4: Resilience 3.0

## Цель этапа

Заменить старый runtime-only circuit breaker Router 2.0 на устойчивую систему уровня 1.0, которая различает типы ошибок, изолирует сбои конкретного connection, переживает перезапуск OmniBridge и безопасно возвращает upstream в работу контролируемым probe-запросом.

Основной Desktop UI 3.0 и Tray Dashboard в этом этапе не редизайнились.

## Архитектура

Добавлен пакет `internal/resilience`.

Lifecycle breaker:

`CLOSED -> DEGRADED -> OPEN -> HALF_OPEN -> CLOSED`

- `CLOSED` — normal routing.
- `DEGRADED` — число upstream failures достигло 60% configured threshold, но запросы ещё разрешены.
- `OPEN` — connection/model временно исключён из routing.
- `HALF_OPEN` — cooldown завершён; разрешается ровно один recovery probe.

Если probe снова падает, breaker возвращается в `OPEN`, а cooldown увеличивается. После успешного probe состояние возвращается в `CLOSED` и escalation counter сбрасывается.

## Failure taxonomy

Resilience 3.0 классифицирует:

- `rate_limit`;
- `quota_exhausted`;
- `timeout`;
- `network`;
- `provider_5xx`;
- `authentication`;
- `model_unavailable`;
- `invalid_request`;
- `client_cancelled`;
- `unknown`.

`authentication`, `quota_exhausted` и `model_unavailable` открывают соответствующий breaker немедленно. Обычные timeout/network/5xx/rate-limit используют configured threshold.

`invalid_request` и `client_cancelled` не считаются upstream failure и не отравляют breaker.

Локальные protocol/translation rejects также исключены из provider-failure accounting.

## Per-connection isolation

Основной breaker key:

`connection:<source>:<connection-id>`

Например:

- `connection:api:openrouter-personal`
- `connection:api:openrouter-work`
- `connection:account:chatgpt-main`

Ошибка одного API key/account connection больше не обязана блокировать весь provider-kind.

Для `model_unavailable` используется отдельный model-scoped key:

`model:<registry-model-id>`

Это позволяет исключить конкретную модель, сохранив остальные модели того же connection.

## Connection Pool foundation

Если несколько подключений публикуют одинаковый `native_model_id`, запрос к неквалифицированному model ID автоматически разворачивается в pool кандидатов.

Пример:

`model="gpt-x"`

может разрешиться в:

- `openrouter-personal/gpt-x`
- `openrouter-work/gpt-x`
- `backup/gpt-x`

Router сначала выбирает connection с лучшим resilience state, а при transient failure переключается на следующее подключение. Это фундамент для Router 3.0; Weighted/RR/P2C/Quota/Cost стратегии будут добавлены отдельным этапом.

## Adaptive cooldown

Configured `circuit_breaker_cooldown_seconds` остаётся базовым cooldown.

Повторный цикл `OPEN -> HALF_OPEN -> OPEN` увеличивает cooldown экспоненциально до hard cap 30 минут.

`Retry-After` upstream учитывается, когда он доступен. Authentication/quota получают безопасный минимальный cooldown, чтобы не создавать aggressive retry loop.

## Persistence

SQLite schema поднята с version 1 до version 2.

Добавлена таблица:

`resilience_events`

`circuit_breakers` хранит актуальный snapshot каждого breaker, включая:

- state;
- failure count;
- failure-kind counters;
- last failure/success;
- open-until;
- open cycle count;
- transition history.

`resilience_events` сохраняет отдельные transitions для диагностики.

Существующая `omnibridge.db` schema 1 автоматически получает backup `omnibridge.db.schema1.<timestamp>.bak` перед migration 2.

## Single-flight control operations

Добавлен `resilience.SingleFlight`.

Он схлопывает параллельные одинаковые control-plane операции:

- Model Registry discovery/refresh;
- account refresh одного и того же account connector.

Если одновременно несколько UI/background запросов запускают refresh, фактическая upstream работа выполняется один раз, а остальные callers получают общий результат.

Отмена ожидающего caller не отменяет уже запущенную shared operation.

## Routing behaviour

Transient failures теперь не обрабатываются как один общий `retryableStatus`.

Реализовано:

- failure classification до решения о fallback;
- immediate fallback на другое connection без искусственной задержки;
- backoff только для повторной попытки в том же connection;
- auth/invalid request не отправляются в бесконечный retry loop;
- client disconnect не открывает breaker;
- temporarily OPEN connection исключается ещё на этапе candidate filtering;
- HALF_OPEN разрешает только один probe одновременно.

## Diagnostics

Добавлено:

`GET /bridge/resilience`

Возвращает secret-safe состояние:

- breaker states;
- failure counters;
- transition history;
- connection pools;
- persistence flag;
- configured threshold/cooldown;
- число single-flight operations in flight.

Добавлено:

`POST /bridge/resilience/reset`

С `{"key":"connection:api:..."}` сбрасывает один breaker. Пустой key сбрасывает все breaker states и resilience events.

`GET /bridge/router/health` теперь также показывает persistent breaker snapshots и connection pools.

`GET /bridge/connections/health` дополнен `resilience_state`.

Tray API active count теперь не считает connection активным, если его модели временно заблокированы Resilience 3.0.

## Безопасность

- Secrets в resilience database records не сохраняются.
- Полный request/prompt body в transition history не сохраняется.
- Ошибки клиента не используются для наказания provider.
- Admin reset защищён существующим Admin API key.
- localhost-only policy Gateway не менялась.

## Проверки этапа

Добавлены regression tests для:

- CLOSED -> DEGRADED -> OPEN -> HALF_OPEN -> CLOSED;
- controlled single HALF_OPEN probe;
- adaptive cooldown escalation;
- persistence across DB reopen;
- failure classification;
- invalid-request non-poisoning;
- connection-pool fallback между двумя одинаковыми native model IDs;
- persistent `/bridge/resilience` state;
- single-flight collapse;
- SQLite schema 1 -> 2 migration + pre-migration backup.

## Следующий этап

**Этап 5 — Router 3.0**:

- Weighted;
- Round Robin;
- Least Used;
- P2C;
- Quota Optimized;
- Cost Optimized;
- Auto routing family;
- session affinity/stickiness foundation;
- routing metrics and decision diagnostics.
