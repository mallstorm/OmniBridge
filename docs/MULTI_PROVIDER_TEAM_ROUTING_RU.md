# Multi-Provider AI Team Routing v1

Pass 3 добавляет в OmniBridge отдельный provider-neutral слой формирования AI-команды поверх существующего Doctor-Aware Router. Router по-прежнему отвечает за hard compatibility, protocol/capability filtering, health и базовый порядок кандидатов. Team Routing не заменяет его: он получает уже совместимый candidate pool и оптимизирует его для конкретной роли workflow.

## Runtime pipeline

```text
workflow step
  -> Doctor-Aware Router
  -> compatible candidate pool
  -> Team Routing profile
  -> diversity / quota / cost / judge policy
  -> ordered team candidates
  -> Context Guard per concrete model
  -> Resilience / fallback
  -> Provider Engine
```

## Role profiles

Встроенный `workflow/coding-review` использует четыре явных профиля:

- `developer`: главный вес у CodingScore, затем Reliability/Doctor; quota и cost учитываются как secondary signal;
- `critic`: повышенный вес ReliabilityScore, DoctorScore и подтверждённого Reasoning;
- `fixer`: CodingScore + ReliabilityScore с quota/cost awareness;
- `judge`: максимальный приоритет Reliability/Doctor и reasoning, Doctor proof обязателен; дополнительно применяется independence penalty за уже использованный provider/family.

Профиль задаётся через `Step.Selector.Profile`, то есть роль не привязана к конкретной модели или бренду.

## Diversity

Поддерживаются три режима:

- `provider`: следующая роль предпочитает другой connection/provider;
- `family`: учитывается модельное семейство (`gpt`, `o-series`, `claude`, `gemini`, `gigachat`, `qwen`, `llama`, `mistral`, `deepseek` и т.д.);
- `model`: не повторять тот же model ID, пока есть альтернатива.

Diversity по умолчанию является предпочтением, а не availability gate. Если независимый кандидат недоступен, workflow может продолжить работу на уже использованном provider — это помечается как fallback в execution diagnostics.

## Fallback policy

`Workflow.Policy.Fallback` содержит:

- `max_candidates`: сколько кандидатов Team Planner передаёт runtime fallback loop;
- `allow_diversity_fallback`: разрешено ли деградировать к уже использованному provider/family/model;
- `quota_floor_percent`: soft floor; кандидат ниже него не объявляется автоматически невалидным, но получает сильный penalty.

Hard budget из Quota & Cost Engine по-прежнему проверяется отдельно перед каждым role call.

## Judge policy

Reviewer является независимым quality gate, а не ещё одним генератором. Built-in policy включает:

- `prefer_different_provider=true`;
- `prefer_different_family=true`.

Если доступна модель другого provider/family, она получает приоритет даже перед немного более высоким score уже использованного кандидата. Если альтернативы нет, graceful fallback остаётся разрешён.

## Quota & Cost awareness

Team Planner использует существующие `QuotaCost.CandidateHints`, которые уже попадают в Router candidate projection. Никакие цены не угадываются по названию модели.

- verified/operator quota влияет на team score;
- известная стоимость сравнивается относительно остальных кандидатов текущего role pool;
- unknown quota/cost остаются нейтральными и не превращаются в ложный ноль;
- hard budget и provider rate limits продолжают обрабатываться существующими Quota/Cost и Resilience слоями.

## Execution diagnostics

Каждый `StepResult` теперь хранит `RoutingDecision`:

- role profile;
- model family;
- candidate rank / count;
- fallback marker;
- quota evidence;
- cost evidence;
- human-readable rationale.

Эти данные сохраняются вместе с execution history в существующую SQLite persistence и доступны через orchestration execution endpoints.

`POST /bridge/orchestration/evaluate` расширен до team dry-run: для каждого шага он показывает ordered candidates, выбранные model/provider/family, quota/cost и rationale, не вызывая upstream model APIs.

## Acceptance

Pass 3 содержит три уровня тестов:

1. pure planner tests — role scoring, provider/family diversity, quota/cost, hard diversity mode;
2. orchestration engine tests — перенос provider/family/model history и persistence routing decision;
3. local multi-provider E2E — четыре логических OpenAI-compatible providers без платных API должны сформировать Developer/Critic/Fixer/Reviewer team через настоящий Router -> Team Planner -> Resilience -> Provider path.

## Не входит в Pass 3

- Project Memory;
- одновременное редактирование workspace несколькими агентами;
- visual Workflow Editor;
- параллельный ensemble/consensus DAG.

Эти функции должны строиться поверх нового Team Routing contract, а не обходить его.
