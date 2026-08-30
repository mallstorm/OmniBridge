# OmniBridge RC8 — Team Routing / Orchestration MVP

## Назначение

Team Routing — детерминированный control-plane поверх существующего Router 3.1. Он не заменяет Provider Engine, fallback, resilience, Model Doctor или adaptive scoring и не создаёт автономный «рой» агентов.

Слой отвечает на вопрос: **какая проверенная модель должна быть lead, какая может быть assistant и нужен ли независимый reviewer для этой задачи**. Фактический upstream execution на этапе MVP не выполняется: API возвращает только dry-run план.

## Границы MVP

Реализовано:

- детерминированная классификация типа/сложности/риска задачи;
- operator model-role profiles;
- inferred profiles только из фактически подтверждённых возможностей Model Registry;
- protected quota reserve;
- sticky lead ownership по `task_id`;
- ограниченное число assistants;
- независимый reviewer для high/critical задач;
- объяснимый план назначения и причины rejection;
- persistent config через SQLite document `team-routing.json`;
- admin API для статуса, настройки и dry-run evaluation;
- Team Routing выключен по умолчанию.

Не реализовано в этом этапе:

- автоматический multi-model upstream execution;
- модель-модель delegation loops;
- скрытая генерация дополнительных prompt/user messages;
- автоматическое слияние нескольких ответов;
- автономное изменение model profiles;
- обучение ролей по названиям моделей или текстовой self-identification.

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
9. operator `risk_limit` и разрешённые assignment roles.

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

Для high/critical, tool и agent задач lead закрепляется за `task_id` на ограниченный TTL. Sticky owner повторно используется только пока остаётся eligible. Если hard gate перестал выполняться, owner не удерживается принудительно: план предупреждает `sticky_owner_unavailable_reselected` и выбирает нового подходящего lead.

## Protected quota reserve

Profile может задавать `minimum_quota_reserve` в процентах.

- routine task не может использовать модель, если известный остаток квоты находится на уровне reserve или ниже;
- critical task может использовать reserve только при явном `allow_critical_reserve_use=true`;
- факт такого назначения отмечается `uses_protected_reserve=true`;
- inferred reserve активен только когда OmniBridge действительно имеет quota telemetry;
- для строгой защиты провайдера без telemetry оператор должен использовать явный profile и собственную политику подключения/квоты.

## Reviewer

`reviewer_risk_threshold` определяет, начиная с какого уровня нужен независимый reviewer. По умолчанию — `high`.

Reviewer:

- не может совпадать с lead;
- обязан пройти identity + Doctor gates;
- должен иметь подходящую review/analysis/security роль;
- при отсутствии reviewer high-risk plan завершается fail-closed и не считается `valid`.

## Assistants

Assistants назначаются только при `delegation_allowed=true` и только при совпадении роли с задачей. Низкая стоимость сама по себе не делает модель подходящим assistant.

`max_assistants` ограничен диапазоном 0..4.

## Admin API

### GET `/bridge/orchestration`

Возвращает persistent configuration и runtime sticky-binding summary.

### PUT `/bridge/orchestration`

Сохраняет конфигурацию Team Routing.

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

### POST `/bridge/orchestration/evaluate`

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

Ответ содержит:

- requirements, полученные существующим Router analyzer;
- классифицированный task;
- lead / assistants / reviewer;
- score и причины назначения;
- rejected candidates и причины rejection;
- источник profile (`configured` или `inferred`);
- список hard gates;
- `execution: "dry_run_only"`.

Endpoint **не делает upstream вызов**.

## HTTP security

Team Routing endpoints подключены как изолированное admin extension после Remote CIDR policy. Они требуют тот же Admin API Key, сохраняют observability/logging и имеют отдельный panic-recovery wrapper. `/v1/*` data-plane этим подключением не изменяется.

## Следующий этап: bounded execution

После зелёного Windows CI MVP можно подключить специальный virtual route (`team/*`) к Router 3.1. Следующий этап должен оставаться ограниченным:

1. план строится один раз;
2. lead выполняет основной запрос через существующий `proxy/upstream` contract;
3. assistants — только для разрешённых read-only/decomposable подзадач;
4. reviewer — отдельный bounded pass без tools и без возможности инициировать новый цикл delegation;
5. фиксированный max calls / max wall time / budget ceiling;
6. любой hard-gate drift перед dispatch приводит к re-plan или fail-closed;
7. полный trace связывает plan, physical model IDs, calls, cost и reviewer result.

До реализации этих ограничений Team Routing остаётся dry-run control-plane.
