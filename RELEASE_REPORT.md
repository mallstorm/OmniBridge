# OmniBridge 1.0.0-rc8 — Release Report

## Назначение

RC8 развивает проверенную базу RC7: сохраняет физическую identity и Doctor gates, добавляет адаптивное качество маршрута, объяснение выбора, Health Autopilot, quota intelligence, безопасную экономию контекста и контролируемое расширение каталога.

## RC8

- bounded async outcomes и EWMA success/latency/TTFT с confidence;
- `Route Decision Trace` и `/bridge/router/explain` без prompt/secret payload;
- Health Autopilot с dry-run и `preconditions_hash`;
- GigaChat OAuth refresh singleflight и сохранённый bounded 401 retry;
- `quota_share`, burn rate и predicted exhaustion;
- Exact Response Cache и generation dedup только для deterministic non-stream no-tools запросов;
- Context Optimizer `observe/safe/balanced/experimental`, fail-open и protected code/schema blocks;
- models.dev только как `declared` reference metadata;
- Ed25519 data-only provider packs без выполнения внешнего кода;
- фактический VS Code self-test для ChatVerified модели;
- Config schema 6, SQLite schema 4, Windows manifest `1.0.0.8`.

## RC6

- Yandex URI приводятся к `gpt://<FolderId>/<slug>` и разрешаются как по registry ID, так и по native URI/slug;
- известные Yandex-модели получают читаемые имена и capability metadata;
- голосовые, realtime, TTS/STT и embedding endpoints исключены из VS Code Chat export;
- PowerShell UI больше не присваивает зарезервированную переменную `$PID`;
- версия `1.0.0-rc6` синхронизирована между Core, `VERSION.txt`, `RELEASE.json`, XAML и Windows manifest `1.0.0.6`;
- Windows SQLite возвращает `REAL` как `float64`, а failed-open корректно закрывает handle;
- сравнение локальных API-ключей переведено на constant-time SHA-256 comparison;
- CI использует поддерживаемый Go 1.27.x и проверяет `gofmt`.

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

## Проверка 29 августа 2026

- ZIP полностью распакован: 386 записей, ошибок CRC, опасных путей и symlink нет;
- provider catalog JSON: 92 уникальных definition;
- XAML XML parse: 14/14;
- все 65 именованных UI Button имеют Click handler;
- YAML workflow parse: PASS;
- secret-pattern scan: PASS;
- `go test ./...`: PASS с SQLite;
- `go test -race ./...`: PASS;
- `go vet ./...`: PASS для Linux и Windows target;
- Windows x64 cross-build `OmniBridge.exe` и `OmniBridgeSetup.exe`: PASS;
- нативный запуск WPF/PowerShell и Windows-only SQLite regression-test остаются задачей GitHub Actions/Windows acceptance.

## Windows acceptance

1. Подключить Yandex AI Studio через «API Ключи», указав API Key и Folder ID.
2. Убедиться, что `/bridge/models` содержит Yandex models без `Unauthenticated`.
3. В «Интеграция» выполнить «Синхронизировать модели» и полностью перезапустить VS Code.
4. Проверить YandexGPT 5.1 и DeepSeek V4 Flash в Chat; Deep Model Doctor запускать осознанно из-за платных probes.
5. После повторных запросов проверить Dashboard «Входящие / кэш».
