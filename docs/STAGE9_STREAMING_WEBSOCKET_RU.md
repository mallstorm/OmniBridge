# Этап 9 — Streaming + WebSocket / OmniBridge 0.4.8-alpha8

## Цель

Убрать обязательное полное буферизование cross-protocol streaming и сделать единый transport-neutral stream поверх `CanonicalStreamEvent`.

## Live cross-protocol SSE

Цепочка теперь выглядит так:

`upstream SSE -> StreamDecoder -> CanonicalStreamEvent -> StreamEncoder -> downstream SSE`

Поддержаны три dialect:

- OpenAI Chat Completions;
- Anthropic Messages;
- OpenAI Responses.

Переносятся без ожидания полного ответа:

- start metadata;
- text delta;
- reasoning delta, если upstream сам отдаёт его через API;
- tool start;
- tool argument delta;
- finish reason;
- usage;
- done/error.

Synchronous read/decode/write loop даёт естественный backpressure. При закрытии downstream HTTP соединения request context отменяет upstream request.

## Compatibility fallback

Если клиент запросил `stream=true`, но upstream не вернул `text/event-stream` и вместо этого прислал один JSON response, OmniBridge сохраняет старый one-shot synthetic SSE fallback. Это не основной путь и не используется для нормального SSE upstream.

## WebSocket Stream Transport v1

Endpoint:

`GET /v1/ws`

Авторизация та же, что у data plane (`Authorization: Bearer`, `x-api-key`, `api-key` или `x-bridge-key`).

Опциональный subprotocol:

`omnibridge.stream.v1`

Клиент отправляет один masked text frame:

```json
{
  "type": "request",
  "api_type": "chat-completions",
  "request": {
    "model": "auto/coding",
    "messages": [{"role":"user","content":"hello"}]
  }
}
```

`api_type` может быть `chat-completions`, `responses` или `messages`.

Ответ — последовательность JSON text frames в формате `CanonicalStreamEvent`, например:

```json
{"type":"start","id":"...","model":"..."}
{"type":"text_delta","text_delta":"hello"}
{"type":"usage","usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}
{"type":"finish","finish_reason":"stop"}
{"type":"done","done":true}
```

Одна WebSocket connection обслуживает один generation request и затем закрывается normal close code 1000.

## Ограничение границы этапа

OmniBridge не пытается универсально имитировать provider-native WebSocket APIs. У разных поставщиков WS contracts существенно различаются. Stage 9 реализует стабильный downstream OmniBridge WebSocket transport и SSE->WS canonical bridge. Native upstream WebSocket executor следует добавлять только для конкретного официального provider contract.

## Длинные streams

Для live generation requests снимается общий `http.Client.Timeout=120s`, чтобы длинная Agent-сессия не обрывалась через две минуты. Non-stream requests сохраняют 120s safety timeout. Cancellation по request context остаётся активной.
