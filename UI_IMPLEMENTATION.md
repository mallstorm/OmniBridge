# OmniBridge UI 3.0 — implementation notes

Версия: 1.0.0-rc8

## Архитектурное изменение

`scripts/desktop.ps1` больше не содержит XAML. На старте он загружает общую ResourceDictionary, новый Shell и десять самостоятельных view. Backend-контракты вызываются через локальный admin API.

## Dashboard

Реализована reference-композиция: hero/lifecycle, KPI, server card, Quick Access, account cards, recent models, system information, 24-hour statistics и нижняя action-панель.

## Остальные страницы

- API Keys: provider catalog, безопасное добавление, GigaChat Scope, Local AI discovery, connection list;
- Accounts: отдельные ChatGPT/Codex и Gemini official login flows;
- Models: фильтры, Doctor/Agent/Coding/Reliability Score, расширенные capabilities, context/output limits и запуск Doctor;
- Model Doctor: connection-based deep probe, классификация и четыре технических score;
- Маршрутизация: Auto Router profiles, ranking diagnostics, Connection Pool/Resilience и safe dry-run;
- Integration: безопасный merge в VS Code и счётчики экспортируемых моделей;
- Logs: структурированный viewer и фильтры;
- Settings: lifecycle, backup и Router aliases;
- About: продуктовая архитектура и security boundary.

## Runtime validation

`scripts/validate-ui.ps1` использует официальный PowerShell parser и WPF XamlReader. Go regression test дополнительно проверяет well-formed XAML, StaticResource и отсутствие legacy embedded XAML.

## Рабочая область 1.0

С Stage 7 фиксированный Shell увеличен до `1260×740`. Это функциональное расширение, а не редизайн: sidebar остаётся 190 px, сохранены существующие карточки, PageHost, нижняя hint/action-панель и Theme.xaml.


## RC3 readability / model identity
- Main shell remains 1260×740; layout topology is unchanged.
- Explicit FontSize values are +1 versus RC2 and the implicit TextBlock size is 13.
- TextBrush/Text2Brush/Text3Brush and HeadingBrush were brightened.
- Models and Doctor rows use family/provider icon resolution (91 providers + 20 families).
- Unverified Doctor state is displayed explicitly instead of being confused with zero or Agent Ready.
