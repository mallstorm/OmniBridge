# OmniBridge — Agent Compatibility & GigaChat Parity v1

## Назначение

Этот этап усиливает OmniBridge как универсальный Agent Gateway для клиентов VS Code/Copilot-класса. Он не создаёт отдельный агентный runtime и не исполняет workspace tools внутри OmniBridge. Его задача — доказуемо определить, какие модели способны выдержать agent tool loop, и надёжно адаптировать provider-specific semantics к canonical/OpenAI-compatible контракту.

Вне scope:

- Project Memory;
- Multi-Agent Workspace Editing;
- визуальный Workflow Editor.

## Пять задач этапа

1. Явный provider-neutral Agent Compatibility contract и persisted Doctor evidence.
2. GigaChat multi-round parity: aliases, schema sanitization, forced tool choice, functions_state_id и tool history.
3. Stateful streaming tool-call parity для fragmented SSE.
4. Agent Doctor v2 со строгим двухраундовым tool round-trip.
5. VS Code/Copilot-shaped acceptance, auto/agent fail-closed routing, документация и CI.

## Agent Compatibility contract

`internal/agentcompat` формирует безопасный отчёт на основании сохранённых результатов Model Doctor. Проверяются:

- Doctor verification;
- system prompt;
- streaming;
- tool calling;
- tool round-trip;
- persisted AgentReady verdict.

Blockers возвращаются явно. Маркетинговое имя модели и предположения по provider name не считаются доказательством capability.

`providers.Model` теперь сохраняет `tool_round_trip` отдельно. Для старых registry snapshots, где это поле ещё отсутствовало, ранее Doctor-verified `AgentReady` помечается как `legacy_agent_ready_backfill`, а не маскируется под новый evidence.

Admin API:

```text
GET  /bridge/agent-compatibility
POST /bridge/agent-compatibility/evaluate
POST /bridge/agent-compatibility/probe
```

## GigaChat parity

Существующий GigaChat adapter уже сохранял проверенные механизмы из GigaCopilot-класса:

- deterministic GigaChat-safe aliases для tool/function names;
- reverse restoration исходного tool name;
- sanitization JSON Schema;
- `tool_choice` -> `function_call`;
- system/developer merge;
- OpenAI assistant tool history -> GigaChat function history;
- `functions_state_id` -> стабильный OpenAI tool-call ID и обратно;
- tool result -> GigaChat function role;
- `finish_reason=function_call` -> `tool_calls`;
- non-stream и SSE response translation;
- Trusted Root CA handling.

Этот этап не переписывает adapter, а фиксирует и усиливает эти свойства regression tests.

## Structured tool-result normalization

Перед GigaChat translation tool-result content нормализуется в JSON string. Это покрывает не только plain text, но и object/array content, которые могут приходить от editor-agent clients.

## Stateful fragmented SSE

Ранее каждый GigaChat `data:` payload переводился независимо. При fragmented tool call provider мог прислать:

1. `functions_state_id`;
2. затем function name;
3. затем несколько argument deltas.

Stateless translation могла потерять state/name и сгенерировать разные или пустые tool-call IDs.

Новый stateful translator хранит состояние по choice index и гарантирует стабильный OpenAI tool-call ID во всех argument fragments. GigaChat-specific `functions_state_id` наружу не протекает.

## Agent Doctor v2

Строгий probe доступен через:

```text
POST /bridge/agent-compatibility/probe
{"model":"provider/model"}
```

Для API-provider выполняется обычный deep Model Doctor, после чего tool round-trip перепроверяется отдельно.

Строгий контракт:

```text
Round 1
  -> model MUST call test_probe(value=7)

Tool result
  -> {"value":7}

Round 2
  -> HTTP 2xx недостаточно
  -> model MUST NOT request another tool
  -> model MUST reply exactly TOOL_OK
```

Только после этого `ToolRoundTrip` считается подтверждённым Agent Doctor v2. Результат сохраняется через обычный Model Registry и используется последующими routing decisions.

Account Connectors пока fail-closed для нового strict endpoint: пока для них не введён эквивалентный двухраундовый execution primitive, endpoint возвращает `agent_probe_not_supported`, а не выдаёт ложное подтверждение.

## auto/agent

`auto/agent` продолжает использовать существующий Doctor-Aware Router. Agent-shaped request требует tool calling, AgentMode и Doctor proof. Непроверенный candidate не должен пройти compatibility filter; несколько Doctor-proven candidates остаются пригодными для resilience/fallback.

Acceptance test использует VS Code/Copilot-shaped tool name и полноценную assistant/tool history, а не упрощённый chat-only запрос.

## Тесты

Добавлены regression/acceptance tests на:

- explicit persisted `ToolRoundTrip` evidence;
- backward-compatible legacy evidence marker;
- fail-closed blocker при отсутствии round-trip;
- GigaChat forced tool choice;
- GigaChat multi-round functions_state_id restoration;
- JSON-safe tool-result normalization;
- GigaChat streaming name/state restoration;
- fragmented SSE со стабильным tool-call ID;
- строгий Agent Doctor: TOOL_OK success;
- строгий Agent Doctor: отклонение простого второго HTTP 2xx без TOOL_OK;
- VS Code/Copilot-shaped `auto/agent` request;
- rejection модели без Doctor proof;
- наличие нескольких Doctor-proven fallback candidates.

Unit/integration tests не используют платные API.

## Архитектурная граница

```text
IDE / Agent client
       |
       v
Protocol Engine / request analyzer
       |
       v
Agent Compatibility evidence
       |
       v
Doctor-Aware Router (auto/agent)
       |
       v
Provider executor
       |
       +--> GigaChat agent-safe adapter
```

Agent Compatibility не заменяет Router, Protocol Engine, Provider Engine или Orchestration Engine.

## Следующий крупный этап

После стабилизации этого pass следующий пакет — **Multi-Provider AI Team Routing**:

1. role-specific routing policy profiles;
2. provider/model-family diversity с объяснимым rationale;
3. fallback/judge policies;
4. quota/cost-aware team composition;
5. execution diagnostics и multi-provider acceptance tests.
