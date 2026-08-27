# OmniBridge Orchestration Engine v1.1

## Назначение

Orchestration Engine добавляет поверх существующих Protocol Engine, Router, Resilience, Context Guard, Quota/Cost и Provider/Account Engine управляемые multi-model workflows. Обычные модели продолжают использовать прежний data-plane pipeline без изменения поведения.

Первый встроенный workflow:

```text
workflow/coding-review

Developer -> Critic -> Fixer -> Reviewer
                         ^          |
                         |-- FAIL --|
```

Reviewer является fail-closed quality gate. Цикл Fixer/Reviewer ограничен `max_iterations=2`.

## Границы v1.1

Намеренно НЕ входят:

- Project Memory;
- Multi-Agent Workspace Editing;
- визуальный Workflow Editor.

Workflow координирует model calls, но не исполняет workspace tools вместо IDE/Agent Harness. Запросы `workflow/*` с tools отклоняются `workflow_tools_not_supported`.

## Архитектура

```text
Client
  -> Chat Completions / Responses / Messages
  -> workflow/coding-review
  -> Orchestration Engine
  -> existing Router
  -> per-candidate Context Guard
  -> existing Resilience
  -> existing Protocol Engine
  -> existing Provider / Account Engine
```

`internal/orchestration` остаётся provider-neutral: там нет OpenAI/Anthropic/GigaChat clients и нет отдельного routing engine.

`internal/server/orchestration_adapter.go` реализует `orchestration.Executor` поверх существующих `routingCandidatesForRequest`, Context Guard, resilience, `upstream`, Protocol Engine и usage/cost telemetry.

## Backward compatibility

Production runtime использует `Server.StartOrchestrated()`, который оборачивает существующий `Server.Handler()` тонким router-слоем.

Перехватываются только:

- `GET /v1/models` для публикации workflow virtual models;
- generation requests с явным `model=workflow/*`;
- `/bridge/orchestration*`;
- `/bridge/agent-compatibility*` diagnostics.

Любая обычная модель делегируется исходному `Server.Handler()`.

## Public protocol parity

`workflow/coding-review` доступен через три внешних dialect:

```text
POST /v1/chat/completions
POST /v1/responses
POST /v1/messages
```

Все три endpoint используют один и тот же Orchestration Engine и один provider-neutral execution state. Responses и Messages не имеют отдельных workflow engines.

`stream=true` поддерживается как synthetic final SSE через существующий Protocol Engine: внутренние Developer/Critic/Fixer/Reviewer ответы наружу не раскрываются и не стримятся.

## Roles и routing

Встроенные роли:

- `developer` — создаёт candidate;
- `critic` — выявляет конкретные дефекты;
- `fixer` — исправляет candidate;
- `reviewer` — возвращает строгий `PASS` или `FAIL`.

Role selectors по умолчанию:

- Developer/Fixer -> `auto/coding`;
- Critic -> `auto/reasoning`;
- Reviewer -> `auto/smart`.

Конкретная модель всегда выбирается существующим Router с Doctor/capability/context/health/quota/cost сигналами.

## Reviewer contract

Reviewer работает fail-closed. Допустимы только точные verdict tokens `PASS`/`FAIL` либо структурированный JSON verdict. `PASSING`, `PASSIVE`, `FAILURE` и неопределённые ответы не принимаются как PASS.

При исчерпании `max_iterations` бесконечного цикла нет: execution получает failure `workflow_review_limit`, сохраняет последний исправленный candidate и reviewer metadata.

## Runtime hardening v1.1

### Per-step budget gate

Hard budget повторно проверяется перед каждым role call. Если предыдущий шаг исчерпал разрешённый бюджет, следующий upstream request не отправляется и execution завершается `workflow_budget_exceeded`.

### Per-candidate Context Guard

Context Guard применяется после выбора конкретного Router candidate, когда известен его реальный `ContextWindow`. Каждый fallback candidate проходит проверку отдельно; отказ одной модели по context fit не запрещает попробовать следующую совместимую модель.

### Resilience/fallback

Каждый role call использует существующую resilience state machine и failure classification. Orchestrator не реализует второй circuit breaker или retry engine.

### Diversity

Поддерживаются политики:

```text
none
provider
family
model
```

Для `coding-review` diversity — preference. Если независимый provider недоступен, workflow деградирует к доступной модели вместо необоснованного отказа.

## Dry-run diagnostics

```text
POST /bridge/orchestration/evaluate
```

показывает без LLM generation:

- workflow/version/policy;
- steps и roles;
- selector;
- ordered Router candidates;
- выбранную model/provider;
- context window;
- capability/Doctor requirements;
- diversity policy;
- routing error, если совместимого candidate нет.

## Persistence

Execution history хранится в существующей `data/omnibridge.db`; отдельная БД не создаётся.

Хранятся workflow/version/status, iterations, step role/model/provider, timestamps, token usage, публичные step results, final result, reviewer status и error state. Secrets, Authorization/OAuth credentials и скрытые chain-of-thought traces не сохраняются.

Persistence является diagnostics layer. Ошибка записи history не превращает уже успешно полученный model result в HTTP 5xx; она фиксируется как `PersistenceError`.

Admin API:

```text
GET  /bridge/orchestration
GET  /bridge/orchestration/workflows
GET  /bridge/orchestration/executions?limit=100
GET  /bridge/orchestration/executions/{id}
POST /bridge/orchestration/evaluate
```

## Observability

Chat Completions, Responses и Messages связывают execution с общим request trace. Агрегируются:

- workflow execution ID;
- workflow/status/review status;
- iterations;
- upstream call count;
- models/providers used;
- input/output/total tokens.

Скрытые reasoning traces не логируются.

## Agent Compatibility foundation

В ветке также присутствует provider-neutral `internal/agentcompat` и admin diagnostics:

```text
GET  /bridge/agent-compatibility
POST /bridge/agent-compatibility/evaluate
```

Это foundation следующего крупного прохода. Он не исполняет workspace tools и не меняет scope Orchestration v1.1.

Текущий GigaChat adapter уже имеет проверенные механизмы tool aliasing, schema sanitization, `tool_choice -> function_call`, `functions_state_id` round-trip, обратное восстановление OpenAI tool calls и SSE translation. Следующий проход должен усиливать и тестировать этот слой, а не дублировать его.

## Ошибки

Основные codes/states:

- `workflow_not_found`;
- `workflow_request_invalid`;
- `workflow_no_candidate`;
- `workflow_step_failed`;
- `workflow_budget_exceeded`;
- `workflow_tools_not_supported`;
- `workflow_review_limit`.

## Тестирование

Без реальных платных API покрыты:

- workflow validation/cycle detection;
- PASS;
- FAIL -> Fixer -> PASS;
- review-limit exhaustion;
- cancellation;
- step failure;
- strict/fail-closed reviewer parsing;
- recorder failure semantics;
- diversity + single-provider fallback;
- SQLite schema/persistence round-trip;
- HTTP workflow discovery;
- full Chat Completions E2E через fake provider;
- full Responses E2E через fake provider;
- full Messages E2E через fake provider;
- четыре внутренних role calls и final result contract.

## Validation gate

Feature-ветка проверяется Windows GitHub Actions:

```text
go test ./...
go vet ./...
XAML + PowerShell validation
Windows x64 release build
```

Actions artifact retention синхронизирован с `main`: хранится rolling set из трёх последних artifacts, caches очищаются отдельным maintenance workflow.

## Статус v1.1

Контракт Orchestration v1.1 считается завершённым после зелёного CI на финальном HEAD и проверки mergeability PR. Новые функции после этого относятся к следующему крупному проходу, а не расширяют v1.1.

## Следующий крупный проход

**Agent Compatibility & GigaChat Parity**:

1. единый provider-neutral Agent Compatibility contract и blockers/evidence;
2. GigaChat multi-round parity hardening: aliases, schema, `tool_choice`, `functions_state_id`, history;
3. streaming tool-call parity и fragmented SSE tests;
4. Agent Doctor v2 с реальным двухраундовым tool round-trip contract на fake providers;
5. VS Code/Copilot-shaped acceptance tests, `auto/agent`, fallback и documentation/CI gate.

Project Memory, Multi-Agent Workspace Editing и visual Workflow Editor остаются вне этого прохода.
