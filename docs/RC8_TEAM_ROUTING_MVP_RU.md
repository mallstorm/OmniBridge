# OmniBridge RC8 — Team Routing / Orchestration MVP

## Назначение

Team Routing — детерминированный control-plane поверх существующего Router 3.1. Он не заменяет Provider Engine, fallback, resilience, Model Doctor или adaptive scoring и не создаёт автономный «рой» агентов.

Слой отвечает на вопрос: **какая проверенная модель должна быть lead, какая может быть assistant и нужен ли независимый reviewer для этой задачи**.

RC8 Team Routing состоит из двух независимо включаемых уровней:

1. **Planning / dry-run** — строит и объясняет план, не делает upstream calls.
2. **Bounded execution preview** — выполняет только явно разрешённый `team/*` request в жёстких лимитах calls/time/bytes/cost и повторно проверяет hard gates непосредственно перед каждым физическим dispatch.

Оба уровня выключены по умолчанию. Execution нельзя включить, если planning выключен.

## Реализовано

- детерминированная классификация типа/сложности/риска задачи;
- operator model-role profiles;
- inferred profiles только из фактически подтверждённых возможностей Model Registry;
- protected quota reserve;
- sticky lead ownership по `task_id`;
- ограниченное число assistants;
- независимый reviewer для high/critical задач;
- provider privacy boundary: reviewer/assistants по умолчанию остаются у provider выбранного lead;
- cross-provider review/assistants только через явные execution-policy opt-in флаги;
- объяснимый план назначения и причины rejection;
- persistent config через SQLite document `team-routing.json`;
- admin API для статуса, настройки и dry-run evaluation;
- bounded non-streaming execution для `team/*`;
- execution-time revalidation модели перед каждым вызовом;
- global budget gate + optional per-request estimated cost ceiling;
- shared wall-clock deadline;
- exact physical dispatch без скрытого Team fallback;
- usage/cost/resilience/adaptive accounting через существующие RC8 primitives.

Не реализовано:

- автономные model-to-model delegation loops;
- бесконечные multi-agent циклы;
- streaming Team execution;
- автоматическое повторное выполнение lead после reviewer concerns;
- автоматическое изменение model profiles;
- обучение ролей по названиям моделей или текстовой self-identification;
- прозрачное `team/*` распознавание внутри основного `proxy` без explicit execution header — это остаётся следующим интеграционным шагом после стабилизации preview.

## Hard gates

Orchestration не имеет права ослабить нижележащие ограничения OmniBridge.

Для кандидата применяются как минимум:

1. health / circuit breaker;
2. `ChatVerified`;
3. для high/critical — `IdentityVerified`;
4. для high/critical — фактический Doctor proof;
5. для tool/agent задач — `AgentReady` + `ToolCalling` + verified identity;
6. фактические reasoning/vision requirements;
7. verified context window;
8. quota reserve policy;
9. operator `risk_limit` и разрешённые assignment roles;
10. provider privacy boundary;
11. **повторная dispatch-time revalidation** непосредственно перед assistant/lead/reviewer call.

Security role намеренно **не выводится автоматически**. Для назначения модели на `security_review` оператор должен явно добавить соответствующую роль в profile.

## Классификация

Classifier работает локально и детерминированно. Текст запроса используется только в памяти для определения класса и **не входит в возвращаемый `Task` и не сохраняется в Team Routing storage**.

Tool/function schemas исключаются из текстовой классификации, чтобы имя функции вроде `security_audit` не превращало обычный пользовательский запрос в security task.

Основные классы:

- `general`
- `security_review`
- `architecture`
- `debugging`
- `implementation`
- `testing`
- `review`
- `research`
- `summarization`
- `documentation`

Risk levels:

- `low`
- `medium`
- `high`
- `critical`

Явный operator override риска является **монотонным**: он может повысить риск, но не может понизить уровень, уже установленный classifier/task policy.

## Sticky ownership

Для high/critical, tool и agent задач lead закрепляется за `task_id` на ограниченный TTL. Sticky owner повторно используется только пока остаётся eligible.

Если hard gate перестал выполняться перед фактическим dispatch:

- stale lead не вызывается;
- sticky binding освобождается;
- текущий request завершается fail-closed;
- следующий request выполняет новый план.

Таким образом Team execution не подменяет план незаметным fallback.

## Protected quota reserve

Profile может задавать `minimum_quota_reserve` в процентах.

- routine task не может использовать модель, если известный остаток квоты находится на уровне reserve или ниже;
- critical task может использовать reserve только при явном `allow_critical_reserve_use=true`;
- факт такого назначения отмечается `uses_protected_reserve=true`;
- inferred reserve активен только когда OmniBridge действительно имеет quota telemetry;
- explicit profile остаётся способом задать строгую операторскую reserve policy.

Поскольку eligibility проверяется повторно перед каждым dispatch, assistant calls не могут незаметно истратить квоту и оставить stale lead/reviewer eligible по старому snapshot.

## Provider privacy boundary

Наличие нескольких облачных providers не означает разрешение автоматически передавать один и тот же пользовательский контекст каждому из них.

По умолчанию:

- `allow_cross_provider_review=false`;
- `allow_cross_provider_assistants=false`;
- reviewer должен быть другой физической моделью, но у того же provider, что и lead;
- assistant с другим provider не запускается;
- если high/critical task требует reviewer, а у provider lead нет второго подходящего reviewer, plan становится invalid/fail-closed вместо скрытой отправки задачи другому vendor.

Только явный operator opt-in разрешает cross-provider sharing:

- `allow_cross_provider_review=true` разрешает и предпочитает подходящего reviewer у другого provider;
- `allow_cross_provider_assistants=true` разрешает advisory assistants у других providers.

Эти флаги влияют и на dry-run, поэтому оператор видит границы передачи данных **до** реального execution.

## Reviewer

`reviewer_risk_threshold` определяет, начиная с какого уровня нужен независимый reviewer. По умолчанию — `high`.

Reviewer:

- не может совпадать с lead;
- обязан пройти identity + Doctor gates;
- должен иметь подходящую review/analysis/security роль;
- по умолчанию остаётся у provider lead, но является другой физической моделью;
- cross-provider reviewer разрешается только при `allow_cross_provider_review=true`;
- вызывается без tools отдельным bounded call;
- не имеет права инициировать новый delegation loop;
- возвращает структурированный verdict `approve` или `concerns`;
- при отсутствии/ошибке mandatory reviewer request завершается fail-closed.

`execution.block_on_review_concerns_at` задаёт уровень риска, начиная с которого verdict, отличный от `approve`, блокирует lead response. Default — `critical`.

## Assistants

Assistants назначаются только при `delegation_allowed=true` и только при совпадении роли с задачей. Низкая стоимость сама по себе не делает модель подходящим assistant.

Execution assistants дополнительно ограничены:

- `execution.run_assistants=true`;
- только non-tool / non-agent request;
- в preview — только Chat Completions;
- каждый assistant выполняет read-only advisory call без tools;
- по умолчанию assistant должен принадлежать provider lead;
- другой provider разрешается только при `allow_cross_provider_assistants=true`;
- notes передаются lead как **untrusted user-level evidence**, а не как system/developer instruction;
- число assistants ограничено одновременно `policy.max_assistants` и `execution.max_calls`.

## Persistent config

Пример:

```json
{
  "enabled": true,
  "policy": {
    "sticky_ttl_seconds": 7200,
    "reviewer_risk_threshold": "high",
    "max_assistants": 1,
    "allow_critical_reserve_use": false
  },
  "execution": {
    "enabled": true,
    "max_calls": 3,
    "max_wall_time_seconds": 90,
    "max_input_bytes": 8388608,
    "max_response_bytes": 8388608,
    "max_review_bytes": 524288,
    "max_estimated_cost_usd": 0.25,
    "require_known_cost": false,
    "run_assistants": false,
    "allow_cross_provider_review": false,
    "allow_cross_provider_assistants": false,
    "block_on_review_concerns_at": "critical"
  },
  "profiles": {
    "provider/model-a": {
      "roles": ["implementation", "debugging", "testing"],
      "risk_limit": "high",
      "minimum_quota_reserve": 20,
      "lead": true,
      "assist": true,
      "review": false
    },
    "provider/model-b": {
      "roles": ["analysis", "review", "architecture"],
      "risk_limit": "high",
      "minimum_quota_reserve": 15,
      "lead": true,
      "assist": true,
      "review": true
    }
  }
}
```

Profile lookup: exact registry model ID -> native model ID -> provider ID -> `*`.

Execution limits:

- `max_calls`: 1..6;
- `max_wall_time_seconds`: 5..300;
- `max_input_bytes`: до 32 MiB;
- `max_response_bytes`: до 32 MiB;
- `max_review_bytes`: до 2 MiB;
- `max_estimated_cost_usd`: `0` отключает локальный estimate ceiling;
- `require_known_cost=true` запрещает execution, если для любой обязательной модели нет cost hint;
- `allow_cross_provider_review=false` по умолчанию не разрешает отправлять task/lead response другому provider для review;
- `allow_cross_provider_assistants=false` по умолчанию не разрешает отправлять task другому provider для advisory call.

## Admin API

### GET `/bridge/orchestration`

Возвращает persistent config, runtime sticky-binding summary, список `team/*` virtual models и execution contract.

### PUT `/bridge/orchestration`

Сохраняет planning/execution policy и profiles.

### POST `/bridge/orchestration/evaluate`

Dry-run endpoint. Никогда не делает upstream вызов.

Принимает исходный JSON request или wrapper:

```json
{
  "api_type": "chat-completions",
  "task_id": "repo-fix-42",
  "task_type": "debugging",
  "risk": "high",
  "request": {
    "model": "auto/smart",
    "messages": [
      {"role": "user", "content": "..."}
    ]
  }
}
```

Ответ содержит requirements, task, lead/assistants/reviewer, scores/reasons, rejection reasons, profile sources, provider-sharing policy и hard gates.

## Bounded data-plane preview

Поддерживаемые virtual models:

- `team/smart`
- `team/coding`
- `team/debug`
- `team/review`
- `team/research`
- `team/architecture`
- `team/security`

Они используются на обычных generation endpoints:

- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/messages`

Но preview требует **все три** opt-in условия:

1. `enabled=true`;
2. `execution.enabled=true`;
3. request header `X-OmniBridge-Team-Execute: true`.

Пример:

```http
POST /v1/chat/completions
Authorization: Bearer <local Data API Key>
X-OmniBridge-Team-Execute: true
X-OmniBridge-Task: repo-fix-42
Content-Type: application/json
```

```json
{
  "model": "team/debug",
  "stream": false,
  "messages": [
    {"role": "user", "content": "Find the regression and propose a fix."}
  ]
}
```

Без `X-OmniBridge-Team-Execute: true` Team extension **не читает body и не перехватывает обычный RC8 data-plane**. В этом случае `team/*` ещё не является прозрачной моделью основного Router и обычный proxy вправе вернуть стандартную ошибку неизвестного route/model. Это намеренный preview-contract, а не скрытый fallback.

## Execution pipeline

1. Data API auth и Remote CIDR policy выполняются до Team execution.
2. Проверяется global OmniBridge budget.
3. Analyzer получает фактические capability requirements.
4. Classifier строит task/risk.
5. Planner выбирает lead/assistants/reviewer.
6. Provider privacy policy ограничивает reviewer/assistants provider-ом lead, если cross-provider opt-in не включён.
7. Проверяется `max_calls` и estimated cost ceiling.
8. Создаётся общий wall-clock deadline.
9. Опциональные assistants выполняются как advisory calls в разрешённых provider boundaries.
10. Перед lead делается свежая eligibility/quota/health revalidation.
11. Lead вызывается как **exact physical model** через существующий `upstream` contract; Team layer не делает скрытый fallback.
12. Existing Context Optimizer применяется к lead request.
13. Existing resilience/adaptive/quota headers/usage/cost accounting сохраняются.
14. Mandatory reviewer revalidates и выполняет отдельный exact physical call.
15. При critical reviewer concerns/unknown request блокируется согласно policy.
16. Успешный lead response остаётся protocol-compatible и получает `omnibridge_team` metadata + Team response headers.

## Streaming

Streaming намеренно отклоняется в bounded preview.

Причина: reviewer должен видеть завершённый lead response. Буферизация SSE разрушила бы streaming semantics, а review незавершённого ответа не является корректной независимой проверкой.

Streaming Team execution может появиться только с отдельным архитектурным контрактом (например, lead stream + post-stream review event или двухфазный protocol), а не как скрытая буферизация обычного SSE.

## Cache

Composite Team execution не использует Exact Response Cache RC8. Отдельные assistant/reviewer calls также не проходят generation cache.

Это консервативно: multi-model result зависит от plan, physical identities, reviewer и runtime quota/health. Composite cache потребует отдельной signature, включающей Team plan version и physical assignment IDs.

## Response metadata

Успешный JSON response получает дополнительный top-level `omnibridge_team` с:

- route;
- task type/risk;
- physical lead;
- assistants;
- reviewer;
- reviewer verdict/summary;
- calls/max calls;
- estimated/actual cost telemetry;
- Context Optimizer mode;
- фактически разрешённые cross-provider review/assistant flags.

Headers:

- `X-OmniBridge-Team-Route`
- `X-OmniBridge-Team-Lead`
- `X-OmniBridge-Team-Reviewer`
- `X-OmniBridge-Team-Review`
- `X-OmniBridge-Team-Calls`

Стандартные RC7/RC8 physical model headers остаются источником истины.

## Следующий интеграционный этап

После стабилизации bounded preview и зелёного Windows CI:

1. перенести распознавание `team/*` внутрь основного `proxy` после единственного чтения body;
2. убрать необходимость per-request execution header для клиентов, которые выбирают `team/*` как virtual model;
3. экспортировать `team/*` через `/v1/models` / VS Code только когда planning и execution policy действительно готовы;
4. добавить persistent Team execution trace, связывающий plan -> physical calls -> actual cost -> reviewer verdict;
5. после этого отдельно проектировать streaming, а не имитировать его буферизацией.
