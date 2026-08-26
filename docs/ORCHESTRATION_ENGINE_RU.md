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

Reviewer verdict работает fail-closed. Plain-text verdict должен начинаться с отдельного точного токена `PASS` или `FAIL`; строки вроде `PASSING` и `FAILURE` не принимаются. Также parser понимает структурированный JSON verdict с полем `status`, но workflow не зависит от наличия JSON mode у provider.

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

## Review-limit semantics

Если Reviewer продолжает возвращать `FAIL` после достижения `max_iterations`, бесконечного цикла нет. Execution сохраняет:

- лучший последний исправленный candidate как `final_result`;
- `review.status=FAIL`;
- `error=workflow_review_limit`.

Data plane может вернуть этот последний candidate, а admin history явно показывает, что quality gate не был пройден.

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

Persistence является diagnostics/observability слоем, а не условием успешного data-plane ответа. Если workflow уже успешно получил финальный результат, ошибка записи history не заменяет его на HTTP 5xx; она остаётся в `Execution.PersistenceError`.

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

Основные orchestration error codes/states:

- `workflow_not_found`;
- `workflow_request_invalid`;
- `workflow_no_candidate`;
- `workflow_step_failed`;
- `workflow_tools_not_supported`;
- `workflow_protocol_not_supported`;
- `workflow_review_limit` в execution metadata после исчерпания review iterations.

## Тестирование

Unit/integration tests не вызывают реальные платные API. Покрываются:

- workflow validation и cycle detection;
- PASS flow;
- FAIL -> Fixer -> PASS;
- review-limit exhaustion;
- step failure;
- cancellation до model call;
- fail-closed reviewer;
- exact plain-text verdict parsing;
- structured JSON verdict parsing;
- recorder failure без потери успешного model result;
- diversity preference и single-provider fallback;
- HTTP workflow detection/delegation helpers;
- SQLite schema idempotency;
- execution/steps persistence round-trip;
- full HTTP workflow path через fake OpenAI-compatible provider: `/v1/models` -> `workflow/coding-review` -> четыре upstream calls -> final response -> SQLite execution history.

## CI / release validation

Windows GitHub Actions подтвердил прохождение следующих обязательных code/build gates на orchestration feature-ветке:

- `go test ./...`;
- `go vet ./...`;
- XAML + PowerShell validation;
- Windows x64 release build.

Release ZIP был успешно создан как `dist/OmniBridge_1.0.0-rc5_Windows_x64.zip`.

Наблюдавшийся красный workflow был вызван не кодом, а исчерпанной GitHub Actions artifact storage quota на шаге `upload-artifact`. Validation workflow изменён так, чтобы test/vet/UI/build оставались обязательными gates, а временная невозможность загрузить уже собранный ZIP помечалась warning и не превращала успешную сборку в ложный code failure.

## Известные ограничения v1

- публично полноценно поддерживается только `/v1/chat/completions` для workflow models;
- `/v1/responses` и `/v1/messages` для `workflow/*` пока возвращают явный `workflow_protocol_not_supported`;
- внутренние role calls не исполняют workspace tools;
- per-candidate Context Guard trimming для role prompts ещё не интегрирован; Router уже проверяет известные context-window limits, но отдельный guard pass должен быть добавлен в следующий runtime-hardening этап;
- dry-run показывает ordered candidates, но пока не фиксирует отдельным полем окончательный selected candidate/diversity rationale;
- `family` diversity пока эквивалентна provider-level preference.

## Рекомендуемый следующий этап: Orchestration Runtime Hardening v1.1

До GigaCopilot parity migration рекомендуется отдельный небольшой проход v1.1 без Project Memory, workspace editing и visual workflow editor.

Цели v1.1:

1. применить Context Guard к каждому внутреннему role request с учётом реально выбранного candidate `ContextWindow`;
2. повторно проверять quota/budget между workflow steps и прекращать дальнейшие upstream calls при достижении hard budget;
3. расширить dry-run полями selected candidate, provider и diversity decision;
4. добавить общий canonical output path для `/v1/responses`, не создавая второй orchestration engine;
5. связать workflow execution с observability trace ID и агрегировать total latency/cost/upstream-call count;
6. добавить explicit-selector tests и no-candidate/budget/candidate-context regression tests;
7. после зелёного CI зафиксировать v1 contract и только затем переходить к GigaChat/VS Code Agent parity.

Project Memory, Multi-Agent Workspace Editing и визуальный Workflow editor остаются за пределами v1.1.
