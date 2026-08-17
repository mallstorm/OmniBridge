# Observability 2.0

`GET /bridge/statistics?period=session|24h|7d|30d|all` возвращает period-specific total, provider и model aggregates, Average Latency, P95 и Success Rate.

`session` начинается при текущем запуске Core; `all` использует сохранённые all-time counters. P95 для all-time основан на сохранённом подробном окне событий (до 30 дней).

История состояния подключений сохраняется в `data/connection-health.json` до 7 дней:
- `GET /bridge/connections/health`
- `GET /bridge/connections/history`
- `GET /bridge/connections/history?id=<connection>&limit=100`

Core log получает категории `[gateway]`, `[router]`, `[accounts]`, `[doctor]`, `[integration]`, `[health]`, `[admin]`, при этом проходит через общий secret-redaction writer.
