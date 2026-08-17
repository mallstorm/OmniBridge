# Этап 14 — Observability & Remote

Каждый HTTP request получает безопасный trace-id. Trace хранит путь, выбранную модель/provider, статус, длительность, attempts, streaming/cross-protocol/context action и известную стоимость — но не prompt и не секретные request headers.

Runtime telemetry включает requests/errors/active, average latency, P50/P95 и per-path counters; доступен Prometheus text endpoint `/metrics`. Persistent request traces сохраняются в SQLite.

Remote mode выключен по умолчанию. Non-loopback bind разрешается только при `remote.enabled=true`, наличии TLS certificate/key и непустом CIDR allow-list. Политика использует реальный socket RemoteAddr и не доверяет `X-Forwarded-For`. Local loopback остаётся обычным HTTP.
