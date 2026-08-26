# OmniBridge Orchestration Engine v1

## Цель

Orchestration Engine добавляет поверх существующих Protocol Engine, Router и Provider Engine управляемые multi-model workflows. Обычные модели продолжают использовать прежний pipeline без изменений.

Первый встроенный workflow: `workflow/coding-review`.

```text
Developer -> Critic -> Fixer -> Reviewer
                         ^          |
                         |-- FAIL --|
```

Reviewer является fail-closed quality gate. Число циклов Fixer/Reviewer ограничено `max_iterations=2`.

## Границы первого этапа

В этот этап намеренно не входят:

- Project Memory;
- Multi-Agent Workspace Editing;
- визуальный редактор Workflows.

Orchestration v1 координирует model calls; он не исполняет workspace tools вместо IDE/Agent Harness. Запрос `workflow/*` с tools в первой версии отклоняется явной ошибкой `workflow_tools_not_supported`, чтобы не создавать ложную или небезопасную автономность.

## Архитектура

```text
Client
  -> OmniBridge data plane
  -> workflow/coding-review
  -> Orchestration Engine
  -> existing Router
  -> existing Resilience
  -> existing Protocol Engine
  -> existing Provider / Account Engine
```

`internal/orchestration` не содержит OpenAI/Anthropic/GigaChat-клиентов и не имеет собственного routing engine.

`internal/server/orchestration_adapter.go` реализует provider-neutral `orchestration.Executor` поверх существующих server contracts: `routingCandidatesForRequest`, resilience state machine, `upstream`, Protocol Engine translation и общую usage/cost telemetry.

## Backward compatibility

В production runtime используется `Server.StartOrchestrated()`. Он оборачивает существующий `Server.Handler()` тонким orchestration-aware router.

Только следующие запросы обрабатываются новым слоем:

- `GET /v1/models` — для публикации workflow virtual models;
- явные `model=workflow/*` на generation endpoints;
- `/bridge/orchestration*` diagnostics/admin API.

Все обычные модели делегируются исходному `Server.Handler()` без изменения прежнего proxy/routing/provider pipeline.

## Domain

`internal/orchestration` содержит workflow/step/role/policy, registry, execution state, strict review verdict, Recorder contract и независимый от провайдеров Executor contract.

Роли:

- `developer` — создаёт исходный candidate;
- `critic` — ищет конкретные дефекты и пропуски;
- `fixer` — исправляет candidate;
- `reviewer` — выдаёт строгий `PASS` или `FAIL`.

Reviewer verdict работает fail-closed: ответ, который не начинается с `PASS` или `FAIL`, считается ошибкой quality gate.

## Routing и diversity

Каждая роль использует существующие виртуальные Router profiles:

- Developer/Fixer -> `auto/coding`;
- Critic -> `auto/reasoning`;
- Reviewer -> `auto/smart` с Doctor proof preference.

Diversity policy допускает `none`, `provider`, `family`, `model`; для `coding-review` используется preference `provider`. Это preference, а не hard gate: если подходит только один provider, workflow продолжает работу.

`family` в v1 оставлен как контракт расширения; пока при отсутствии отдельного model-family registry он деградирует до provider-level preference.

## Public model contract

`GET /v1/models` публикует:

```text
workflow/coding-review
```

Для OpenAI-compatible клиента это обычный `model`:

```json
{
  "model": "workflow/coding-review",
  "messages": [
    {"role": "user", "content": "..."}
  ]
}
```

Внутри выполняется несколько upstream model calls, а клиент получает один финальный Chat Completions response.

### Streaming

Внутренние model calls v1 выполняются non-streaming. Если downstream передал `stream=true`, после завершения workflow финальный ответ преобразуется существующим Protocol Engine в корректный synthetic SSE. Внутренние ответы Developer/Critic/Reviewer наружу не стримятся.

## Persistence

История хранится в существующей `data/omnibridge.db`.

Orchestration использует отдельную идемпотентную subsystem schema migration v1, не меняя стабильный глобальный Persistence 2.0 schema version на этапе feature-ветки.

Таблицы:

- `orchestration_schema`;
- `orchestration_workflows`;
- `orchestration_executions`;
- `orchestration_steps`.

Сохраняются:

- workflow/version/status/iteration;
- started/finished timestamps;
- selected model/provider каждого шага;
- input/output/total tokens;
- публичный result/critique/reviewer output;
- final result и error state.

Secrets, Authorization/OAuth tokens и скрытые chain-of-thought traces не сохраняются.

## Admin API

```text
GET  /bridge/orchestration
GET  /bridge/orchestration/workflows
GET  /bridge/orchestration/executions?limit=100
GET  /bridge/orchestration/executions/{id}
POST /bridge/orchestration/evaluate
```

`POST /bridge/orchestration/evaluate` — dry-run: показывает workflow steps, role selectors и доступных Router candidates без LLM generation.

## Ошибки

Основные orchestration error codes:

- `workflow_not_found`;
- `workflow_request_invalid`;
- `workflow_no_candidate`;
- `workflow_step_failed`;
- `workflow_tools_not_supported`;
- `workflow_protocol_not_supported`;
- `workflow_persistence_failed`;
- `workflow_review_limit` в execution metadata после исчерпания review iterations.

## Тестирование

Unit tests не вызывают реальные платные API. Покрываются:

- workflow validation и cycle detection;
- PASS flow;
- FAIL -> Fixer -> PASS;
- fail-closed reviewer;
- diversity preference и single-provider fallback;
- HTTP workflow detection/delegation helpers;
- SQLite schema idempotency;
- execution/steps persistence round-trip.

Полная готовность PR требует прохождения существующих `go test ./...`, `go vet ./...`, UI validation и Windows x64 release build.

## Следующий этап после v1

После стабилизации этого vertical slice можно отдельно реализовать GigaCopilot parity migration для GigaChat/VS Code Agent Harness и затем безопасное Agent orchestration. Project Memory, workspace editing и visual workflow editor остаются отдельными будущими этапами и не должны незаметно попадать в этот PR.
