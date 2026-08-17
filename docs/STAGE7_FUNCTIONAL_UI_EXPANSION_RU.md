# Этап 7 — Functional UI Expansion / OmniBridge 0.4.6-alpha6

## Цель

Вывести в Desktop уже реализованные Router 3.1 / Doctor-Aware / Resilience возможности, не меняя визуальную идентичность UI 3.0.

## Shell

- фиксированная рабочая область: `1260×740`;
- sidebar сохранена шириной 190 px;
- WindowStyle, Theme.xaml, карточки, типографика и нижняя action-панель сохранены;
- добавлен один новый навигационный сервис: **Маршрутизация**.

## Маршрутизация

Новая страница показывает:

- Router 3.1 runtime version;
- восемь `auto/*` профилей;
- активные Session Affinity bindings;
- Connection Pool health;
- OPEN/DEGRADED breaker summary;
- ранжированный список кандидатов для выбранного `auto/*`;
- score breakdown из Doctor/Agent/Coding/Health/Latency сигналов;
- safe dry-run запроса через `/bridge/router/evaluate` без обращения к upstream.

Dry-run предлагает пользовательские профили Chat / Tool Calling / Parallel Tools / Vision / Reasoning / Structured JSON / Agent Loop и строит только синтетический capability-request. Пользовательские prompt/session данные для этого не требуются.

## Models

Карточка выбранной модели расширена без изменения общей двухколоночной композиции:

- Doctor Score;
- Agent Score;
- Coding Score;
- Reliability Score;
- Chat / Streaming / Tools / Parallel / JSON / Reasoning / Vision;
- Context Window;
- Max Output Tokens;
- время последней Doctor-проверки.

## Model Doctor

В существующий отчёт добавлены четыре score. Deep Doctor workflow и кнопки проверки не изменены.

## Security boundary

Новые UI-сервисы используют только локальные admin endpoints. Router dry-run не вызывает upstream и не сохраняет пользовательский prompt. Session Affinity по-прежнему хранит только хешированный идентификатор.

## Не сделано намеренно

- не изменялась Theme.xaml;
- не переделывалась sidebar;
- не мигрировали UI с WPF/PowerShell;
- не выводились фиктивные quota/cost значения — это отдельный этап 10.
