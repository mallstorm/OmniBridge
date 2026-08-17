# Router 2.0

Routing config сохраняет совместимость с `aliases`, `priorities`, `fallbacks` и `max_retries` и дополнительно поддерживает `policies`.

Доступные mode:
- `direct`
- `priority`
- `fastest`
- `agent_ready_only`
- `local_only`
- `cloud_only`

Пример:
```json
{
  "policies": {
    "coding-fast": {
      "mode": "fastest",
      "candidates": ["openai/gpt-x", "groq/model-y", "local/llama"]
    },
    "agent": {
      "mode": "agent_ready_only"
    }
  },
  "circuit_breaker_threshold": 3,
  "circuit_breaker_cooldown_seconds": 30,
  "max_retries": 2
}
```

`fastest` использует фактическую среднюю latency модели за 24 часа, а при отсутствии статистики — последнее значение Model Doctor. Circuit breaker временно исключает модель после последовательных transient failures. Состояние доступно через `GET /bridge/router/health`.
