# OmniBridge — единый проход этапов 10–14

Версия: `0.4.13-alpha13`.

В одном проходе завершены Quota & Cost Engine, Context Guard, Integration Hub, MCP Control Plane и Observability & Remote. Они используют Persistence 2.0 и общий security boundary OmniBridge.

## Пользовательский слой

Основное окно остаётся `1260×740` и сохраняет дизайн UI 3.0. Добавлена только функциональная страница **«Сервисы»** с четырьмя блоками: Quota & Cost, Context Guard, MCP и Observability/Remote. Существующая страница «Интеграция» получила компактную строку Integration Hub. `Theme.xaml` не менялся.

## Границы достоверности и безопасности

- unknown quota != unlimited;
- unknown pricing != free;
- Context Guard не делает скрытый LLM summary;
- сторонние client configs не перезаписываются без безопасного адаптера;
- MCP stdio auto-process policy fail-closed;
- traces не содержат prompts/API keys;
- remote non-loopback требует TLS + CIDR allow-list.

Следующий этап: 15 — Release Engineering / 1.0 RC.
