# Этап 10 — Quota & Cost Engine

OmniBridge хранит quota snapshots и pricing registry в SQLite. Данные имеют источник и confidence (`verified`, `operator`, `estimated`, `unknown`). Неизвестная квота не считается безлимитной, а неизвестная цена — нулевой.

Engine принимает явные rate-limit headers upstream и официальные account-usage payloads, если в них действительно присутствуют remaining/limit значения. События известной стоимости сохраняются отдельно; доступны session/day/month budget status и optional reject-on-budget. Router `quota_optimized` и `cost_optimized` получают эти данные до operator hints.

Admin API: `/bridge/quota-cost`, `/bridge/quota-cost/refresh`, `/bridge/quota`, `/bridge/pricing`.
