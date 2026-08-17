# Этап 13 — MCP Control Plane

Добавлен persistent MCP registry для `http`, `streamable-http`, `sse` и `stdio` definitions. Control Plane поддерживает CRUD, reachability/command probes и безопасный data-plane список enabled servers.

STDIO process execution намеренно fail-closed: сам факт наличия команды не даёт OmniBridge права автоматически её запускать. `auto_start` требует явного `allow_process=true`, а текущий Control Plane не превращает сохранённую definition в произвольный background process supervisor. Credential material хранится отдельно и может ссылаться через `credential_ref`; raw secrets в SQLite MCP definition не требуются.

Admin API: `/bridge/mcp`, `/bridge/mcp/{id}/probe`, `/bridge/mcp/probe-all`; data-plane metadata: `/v1/mcp/servers`.
