# OmniBridge 0.4.4-alpha4 — Этап 5: Router 3.0

## Цель
Перевести маршрутизацию из простого priority/fallback выбора в отдельный runtime engine, который умеет распределять нагрузку, учитывать telemetry/resilience и обслуживать виртуальные `auto/*` модели.

## Реализованные стратегии
- `direct`
- `priority`
- `fastest`
- `agent_ready_only`
- `local_only`
- `cloud_only`
- `weighted`
- `round_robin`
- `least_used`
- `p2c`
- `quota_optimized`
- `cost_optimized`
- `auto`

`weighted` использует smooth weighted round-robin state на route key, а `round_robin` ведёт независимую позицию для каждого route key. `least_used` использует 24h request telemetry. `p2c` выбирает две альтернативы и сравнивает load score с большим штрафом за degraded resilience state.

## Auto-модели
В `/v1/models` публикуются виртуальные модели:

- `auto/smart`
- `auto/coding`
- `auto/fast`
- `auto/cheap`
- `auto/local`
- `auto/reasoning`
- `auto/vision`
- `auto/agent`

Они не являются upstream-моделями: Router выбирает реальную модель непосредственно перед запросом.

## Auto scoring
Stage 5 score использует следующие runtime сигналы:
- Resilience health rank;
- 24h success rate;
- average/P95 latency;
- 24h request load;
- capability profile bonus;
- quota/cost hints, если они известны.

`auto/coding` повышает Agent Ready + tools + streaming. `auto/agent`, `auto/reasoning`, `auto/vision` используют соответствующие capability gates. `auto/local` исключает cloud candidates.

Полный Doctor-aware capability requirement engine вводится отдельно на этапе 6.

## Quota/Cost boundary
В Stage 5 нет попыток выдумывать реальную квоту или цену. `quota_optimized` и `cost_optimized` уже имеют рабочий ranking path, но до отдельного Quota & Cost Engine получают значения только из явных operator hints в RoutingPolicy:

```json
{
  "mode": "quota_optimized",
  "candidates": ["provider-a/model", "provider-b/model"],
  "quota_hints": {
    "provider-a/model": 82,
    "provider-b/model": 35
  }
}
```

`quota_hints` — процент 0..100. `cost_hints` — относительная/операторская цена за 1M tokens. На будущем Quota & Cost этапе эти inputs заменяются verified runtime adapters.

## Session Affinity
Router 3.0 поддерживает runtime Session Stickiness.

Стабильный session key может поступать через:
- `X-OmniBridge-Session`;
- `X-Session-ID`;
- `X-Conversation-ID`;
- `OpenAI-Conversation-ID`;
- `session_id` / `conversation_id` / metadata fields в JSON.

В памяти хранится только SHA-256-derived короткий routing key, а не исходный session identifier.

После успешного upstream request выбранная модель закрепляется за route/session. Pin снимается при fallback-eligible upstream failure и не переопределяет routing, если connection уже перешёл из CLOSED в degraded state.

Default TTL: 1800 секунд.

## Diagnostics
`GET /bridge/router/health`

показывает runtime Router snapshot, strategy counters, affinity binding count, virtual models, resilience и connection pools.

`GET /bridge/router/diagnostics?model=auto/smart`

возвращает:
- ordered candidates;
- score breakdown для auto route;
- Router runtime state;
- affinity settings.

Ни prompts, ни session IDs, ни credentials diagnostics не возвращает.

## UI
Stage 5 не меняет компоновку UI 3.0. Расширение пользовательского интерфейса вынесено в отдельный Functional UI Expansion этап после Doctor-Aware Routing. Согласованный размер будущего расширенного окна: 1260×740, с сохранением текущей внутренней дизайн-системы.
