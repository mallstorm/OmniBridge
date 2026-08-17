# OmniBridge 1.0.0-rc5 — Release Report

## Назначение

RC5 развивает подтверждённую базу `1.0.0-rc4`: добавляет профессиональное подключение Yandex AI Studio и стабилизирует Core/UI при длительных генерациях, массовом discovery и Deep Model Doctor. Визуальное лицо WPF/PowerShell UI 3.0 и Tray не менялось.

## Yandex AI Studio

- native provider `yandex` и отдельный executor;
- официальный OpenAI-compatible Base URL `https://ai.api.cloud.yandex.net/v1`;
- Models, Responses и Chat Completions;
- раздельные поля Yandex API Key и Folder ID;
- `Authorization: Api-Key` и `OpenAI-Project` формируются Core, а не VS Code;
- API Key хранится через Secret Store и не попадает в config/logs;
- Model Doctor использует preferred protocol и предупреждает о платных глубоких проверках;
- cached-token telemetry поддерживает Chat `prompt_tokens_details` и Responses `input_tokens_details`.

## Stabilization

- generation/WebSocket/embeddings concurrency: 16;
- deep probe concurrency: 2;
- provider/account discovery concurrency: 4;
- перегрузка нормализуется в HTTP 429 `gateway_busy` с `Retry-After`;
- HTTP panic recovery с redacted stack logging;
- фоновые Core/Tray refresh защищены от process-wide panic;
- полная статистика записывается пакетно, а usage events — немедленно;
- штатное завершение принудительно выполняет `Stats.Flush()`.

## Сохранённый RC4 contract

- VS Code export contract 2.2 и literal local gateway auth;
- встроенный Russian Trusted Root CA для GigaChat при обычной TLS validation;
- Provider/Model/Doctor UI, 1260×740 main window, 1000×672 Tray;
- Router 3.1, Protocol Engine 3.1, Context Guard, Quota & Cost, MCP, Observability;
- DPAPI/Secret Store и localhost-only default.

## Проверка в текущей среде

- структура ZIP и SHA-256 исходного RC4 проверены;
- provider catalog JSON: 92 уникальных definition;
- XAML XML parse: 14/14;
- все 65 именованных UI Button имеют Click handler;
- YAML workflow parse: PASS;
- secret-pattern scan: PASS;
- Go и PowerShell runtime в текущем Linux workspace отсутствуют, поэтому новый RC5 не объявляется binary-validated локально;
- GitHub Actions workflow выполняет `go test ./...`, `go vet ./...`, UI validation и Windows x64 release build после публикации.

## Windows acceptance

1. Подключить Yandex AI Studio через «API Ключи», указав API Key и Folder ID.
2. Убедиться, что `/bridge/models` содержит Yandex models без `Unauthenticated`.
3. В «Интеграция» выполнить «Синхронизировать модели» и полностью перезапустить VS Code.
4. Проверить YandexGPT 5.1 и DeepSeek V4 Flash в Chat; Deep Model Doctor запускать осознанно из-за платных probes.
5. После повторных запросов проверить Dashboard «Входящие / кэш».
