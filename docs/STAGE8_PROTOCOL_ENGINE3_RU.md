# Этап 8 — Protocol Engine 3.0 / OmniBridge 0.4.7-alpha7

## Цель

Сделать protocol translation отдельным доменным слоем, а не набором условных веток в server/router. Router выбирает модель/connection; Protocol Engine отвечает за форму запроса, agent-history invariants и response normalization.

## Каноническая модель

Добавлены публичные protocol-neutral contracts:

- `CanonicalRequest`
- `CanonicalMessage`
- `CanonicalPart`
- `CanonicalTool`
- `CanonicalToolCall`
- `CanonicalResponse`
- `CanonicalUsage`
- `CanonicalStreamEvent`

Старые имена `Request/Message/Tool/ToolCall/Response` оставлены alias-ами, чтобы не ломать существующий код.

## Translator Registry

`internal/protocol/registry.go` регистрирует adapters для:

- OpenAI Chat Completions (`chat-completions`)
- Anthropic Messages (`messages`)
- OpenAI Responses (`responses`)

Public parse/encode операции теперь проходят через registry. Это позволяет Stage 9 добавить отдельные live stream decoder/encoder без переноса provider logic обратно в server.

## Same-protocol normalization

Ранее `OpenAI -> OpenAI` запрос фактически пропускался как есть. Теперь same-protocol lane выполняет lossless normalization, сохраняя неизвестные vendor fields (`logprobs`, custom flags и т. п.).

Безопасно исправляются:

- отсутствующие tool-call IDs;
- trailing comma / code-fence defects в `function.arguments`;
- отсутствующий `type: object`/`properties` в tool JSON Schema;
- нарушенная последовательность tool call -> tool result;
- orphan tool results (контент сохраняется, invalid role нейтрализуется);
- system/developer messages, находящиеся после обычной истории;
- часть эквивалентных reasoning aliases.

Provider-specific sanitizer остаётся последним target-aware слоем и теперь также вызывает Protocol Engine для прямых probe/provider вызовов.

## Agent history

Cross-protocol canonical normalization гарантирует:

- stable tool IDs;
- связность tool call/result;
- deterministic empty result repair только для явно незавершённой malformed history;
- сохранение текста orphan result без отправки upstream протокольно невалидной роли;
- schema coercion без semantic prompt rewrite.

## Structured output

Нормализовано различие JSON Schema shapes:

- Chat Completions: `response_format.json_schema`
- Responses: `text.format` (`name/schema/strict`)

Schema не переписывается семантически; добавляются только обязательные structural defaults там, где они отсутствуют.

## Reasoning compatibility

Canonical message/response теперь умеют переносить provider-returned reasoning replay fields между Chat и Messages, а Responses output reasoning summary разбирается в canonical response. Это compatibility layer, а не механизм раскрытия скрытых chain-of-thought: OmniBridge переносит только то reasoning-содержимое, которое upstream уже явно вернул в протокольном payload.

## Response sanitizer

Same-protocol response также проходит lossless normalization: неизвестные поля сохраняются, tool IDs/arguments нормализуются. Cross-protocol response сначала переводится в `CanonicalResponse`, затем нормализуется и кодируется в downstream protocol.

## Stream contract

В Stage 8 введён `CanonicalStreamEvent` и опубликован diagnostic flag `canonical_stream_contract=true`. Реальный cross-protocol stream пока остаётся buffered synthetic SSE. Это сознательная граница этапа: live SSE/WebSocket decoder/encoder реализуется Stage 9, не смешивая transport rewrite с Protocol Engine refactor.

## Diagnostics

Новый admin endpoint:

`GET /bridge/protocol-engine`

Он показывает version 3.0, зарегистрированные protocols и feature flags. Secret values endpoint не возвращает.

## Regression policy

- UI 3.0/1260×740 не изменяется.
- Неизвестные поля same-protocol запросов и ответов не теряются.
- Router не содержит protocol-specific agent repair.
- Provider-specific quirks остаются в provider target sanitizer/executor, а не в routing.
