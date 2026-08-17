# OmniBridge 0.4.2-alpha2 — Этап 3: Provider Engine 3.0

## Цель

Перевести API-provider слой от набора ветвлений по `Kind` к расширяемой архитектуре:

`ProviderDefinition + Protocol Target + Provider Executor + Request Sanitizer`.

Основной Desktop UI 3.0 не изменяется.

## Data-driven Provider Registry

Каталог перенесён в `internal/providerdefs/catalog.json` и в текущей версии содержит **91 definition**.

Уровни поддержки намеренно различаются:

- `native` — отдельный executor OmniBridge: OpenAI, Anthropic, Gemini, GigaChat;
- `preset` — заранее настроенный OpenAI/Anthropic-compatible API preset;
- `local` — заранее настроенный локальный runtime;
- `template` — OpenAI-compatible provider/cloud template, где Base URL зависит от аккаунта/региона/deployment и поэтому вводится пользователем;
- `local-template` — локальный OpenAI-compatible runtime с пользовательским Base URL;
- `reserved` — провайдер присутствует в каталоге, но native protocol driver ещё не разрешён;
- `custom` — Same-Origin Auto Detect.

OmniBridge не выдаёт `template` за заранее проверенный фиксированный endpoint. Перед сохранением template выполняется same-origin `/models` + protocol probe; без успешной проверки сохранение возможно только при явном `allow_unverified`.

## Executor Registry

В `internal/providers/executor.go` введён интерфейс `Executor` и registry:

- `openai-compatible` — общий executor для большинства API;
- `openai` — OpenAI lane на общей совместимой реализации;
- `anthropic` — native Messages/model listing;
- `gemini` — native model discovery + OpenAI-compatible generation lane;
- `gigachat` — OAuth + GigaChat-specific translation;
- `unsupported-native` — безопасный отказ для зарезервированных native profiles.

`Manager.ListModels` и `Manager.Proxy` больше не выбирают provider через большой `switch p.Kind`; executor определяется definition registry.

## Protocol Target

`ResolveTarget()` формирует target plan:

- executor;
- support level;
- protocol;
- effective Base URL;
- effective auth type;
- headers.

Protocol-specific Base URL/Auth overrides остаются совместимыми с предыдущей конфигурацией.

## Target Request Sanitizer

Перед отправкой в executor выполняется declarative safety pass:

- удаляются внутренние поля `_omnibridge_*`;
- definition может задать `request_policies.remove`;
- definition может задать безопасное top-level `rename`;
- GigaChat policy дополнительно удаляет `parallel_tool_calls`, дальнейшая GigaChat-specific трансляция остаётся в специализированном executor.

Protocol translation по-прежнему живёт в `internal/protocol`; sanitizer не заменяет Universal Protocol Engine.

## Generic model-list parser

OpenAI-compatible discovery теперь понимает обе распространённые формы:

- `{ "data": [...] }`;
- `{ "models": [...] }`.

Элементы могут быть объектами либо строковыми model IDs. Поддерживаются также `displayName`, `inputTokenLimit` и `outputTokenLimit`, если upstream их возвращает.

## Keyless local auto-detect

Для keyless local endpoint Auto Detect возвращает `auth_type=none`, а не фиктивный `bearer`.

## Admin diagnostics

Добавлен:

`GET /bridge/provider-engine`

Он показывает количество definitions, распределение по executors/support levels и executor, выбранный для каждого настроенного API connection. Secrets не возвращаются.

## Безопасность

- Custom/template probing остаётся same-origin;
- credential не пересылается на другой host;
- web-cookie/session scraping отсутствует;
- MITM/TLS impersonation отсутствуют;
- account/deployment-specific URL не выдумываются в catalog templates;
- `reserved` native profiles нельзя случайно использовать как рабочий chat backend.
