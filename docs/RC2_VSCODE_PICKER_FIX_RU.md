# OmniBridge 1.0.0-rc3 — VS Code Picker Fix

## Причина патча

RC1 корректно создавал записи Custom Endpoint в `%APPDATA%\\Code\\User\\chatLanguageModels.json`, поэтому модели могли появляться в `Chat: Manage Language Models`. Однако текущий VS Code хранит пользовательскую видимость моделей в Chat model picker отдельно от файла Custom Endpoint. Если у импортированной модели выключена Visibility, модель присутствует в Manage Models, но отсутствует в picker; если в Agent mode модель не имеет Tool Calling capability, VS Code также исключает её из Agent picker.

OmniBridge не изменяет внутреннюю базу состояния VS Code (`state.vscdb`) и не пытается принудительно включать недокументированное состояние Visibility. Первый показ модели остаётся управляемым самим VS Code: пользователь при необходимости включает значок глаза в `Chat: Manage Language Models`.

## Что исправлено на стороне OmniBridge

1. Добавлен единый Core endpoint `GET /bridge/vscode/export` (contract 2.0).
2. Экспорт больше не собирается из сырых `/bridge/models` внутри PowerShell.
3. Поля Custom Endpoint формируются централизованно: `id`, `name`, `url`, `apiType`, `toolCalling`, `vision`, `streaming`, `thinking`, `contextWindow`, `maxInputTokens`, `maxOutputTokens`.
4. Provider получает `vendor=customendpoint` и compatibility default `apiType=chat-completions`.
5. Подтверждённая `AgentReady` модель экспортируется с `toolCalling=true` даже при старом compatibility snapshot, где отдельный флаг ToolCalling мог отсутствовать.
6. Streaming описывает capability локального OmniBridge endpoint: Protocol Engine умеет отдавать streaming transport и безопасный one-shot SSE fallback.
7. В экспорт добавлены виртуальные Router models: `auto/smart`, `auto/coding`, `auto/fast`, `auto/cheap`, `auto/local`, `auto/reasoning`, `auto/vision`, `auto/agent` — только если для профиля существуют реальные кандидаты.
8. `scripts/sync-vscode.ps1` выполняет safe merge и сохраняет сторонние Custom Endpoint providers.
9. После синхронизации явно выводятся число экспортированных моделей, число моделей для Agent picker и число `auto/*`.
10. Desktop/Integration показывают подсказку: если модели видны в Manage Models, но отсутствуют в picker, нужно включить Visibility (значок глаза) и полностью перезапустить VS Code.

## Проверка после установки RC2

1. Запустить OmniBridge и убедиться, что нужные модели находятся в Model Registry.
2. Для Agent mode выполнить Model Doctor и убедиться, что хотя бы одна модель имеет Tool Calling / Agent Ready.
3. Открыть `Интеграция` и выполнить `Синхронизировать модели`.
4. Полностью закрыть все окна VS Code и запустить VS Code снова.
5. Выполнить `Chat: Manage Language Models`.
6. Найти OmniBridge и включить значок глаза у нужных моделей. Для первого теста рекомендуется включить `auto/agent`, `auto/coding`, `auto/smart` и одну физическую Agent Ready модель.
7. Открыть Chat/Agent model picker. Модели OmniBridge должны стать доступны. В Agent mode показываются только модели, которые VS Code считает пригодными для tools.

## Почему Visibility не включается автоматически

`chatLanguageModels.json` описывает Custom Endpoint и capabilities, но picker Visibility является пользовательским состоянием VS Code. OmniBridge не редактирует внутреннее хранилище VS Code, поскольку это недокументированный и хрупкий контракт, способный повредить пользовательские настройки при обновлении VS Code.
