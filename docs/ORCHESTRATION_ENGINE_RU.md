# OmniBridge Orchestration Engine v1

## Цель

Orchestration Engine добавляет поверх существующих Protocol Engine, Router и Provider Engine управляемые multi-model workflows. Обычные модели продолжают использовать прежний pipeline без изменений.

Первый встроенный workflow: `workflow/coding-review`.

```text
Developer -> Critic -> Fixer -> Reviewer
                         ^          |
                         |-- FAIL --|
```

Reviewer является fail-closed quality gate. Число циклов Fixer/Reviewer ограничено `max_iterations`.

## Границы первого этапа

В этот этап намеренно не входят Project Memory, Multi-Agent Workspace Editing и визуальный редактор workflows. Orchestration v1 координирует model calls; он не пытается самостоятельно исполнять workspace tools вместо IDE/Agent Harness.

## Domain

`internal/orchestration` содержит workflow/step/role/policy, registry, execution state, strict review verdict и независимый от провайдеров Executor contract.

Роли: `developer`, `critic`, `fixer`, `reviewer`.

Diversity policy допускает `none`, `provider`, `family`, `model`; для coding-review используется preference `provider`. Diversity должна деградировать мягко, если доступен только один подходящий provider.

Permissions заложены как `none`, `read_only`, `write`, но enforcement workspace tools не входит в v1.

## Следующая интеграция

Следующий атомарный шаг — Server adapter, который реализует orchestration.Executor через существующие Router + upstream/resilience/protocol механизмы OmniBridge. После этого `workflow/coding-review` добавляется в `/v1/models` и маршрутизируется из `/v1/chat/completions` в Orchestration Engine.

Нельзя создавать отдельный provider client или второй routing engine внутри orchestration package.

## Безопасность

Не сохранять secrets, Authorization/OAuth tokens или скрытые chain-of-thought traces. Диагностика может хранить публичные step outputs, critique/review verdict, model/provider selection, usage, latency и status согласно общей security/redaction policy OmniBridge.
