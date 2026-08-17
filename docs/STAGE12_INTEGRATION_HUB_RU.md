# Этап 12 — Integration Hub

Integration Hub обнаруживает поддерживаемые локальные клиенты и строит безопасный OpenAI-compatible connection plan к OmniBridge. Каталог: Visual Studio Code, Cursor, Continue, Cline, Roo Code, Codex CLI, Claude Code, OpenCode и OpenClaw.

Автоматическое изменение конфигурации разрешено только существующему VS Code Connector safe-merge. Для остальных клиентов Hub показывает endpoint/environment/snippet plan, но не перезаписывает пользовательские конфиги вслепую. В планах используется только локальный OmniBridge key placeholder; upstream API keys не экспортируются.

Admin API: `/bridge/integrations`, `/bridge/integrations/{id}/plan`, `/bridge/integrations/{id}/configured`.
