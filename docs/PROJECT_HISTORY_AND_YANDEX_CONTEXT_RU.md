# OmniBridge — история разработки и контекст ветки «Yandex API и нейросети»

Дата сверки: 15 августа 2026 года.

## Вывод по версии

`OmniBridge_1.0.0-rc4_SOURCE` — самая свежая полная исходная сборка, найденная в доступных ветках. Номера `0.3.2-rc6`, `0.4.1-alpha1` … `0.4.13-alpha13` относятся к последовательным этапам разработки, уже сведённым в поколение `1.0.0-rc1` и исправления `rc2`–`rc4`. Это подтверждается `RELEASE.json`, `IMPLEMENTATION_MATRIX.md`, документами этапов 1–15 и минимальной точкой обновления `0.3.2-rc6`.

Исходный архив RC4 сохранён без изменений. Его SHA-256:

`da6f35c3adba1b12e59cace307f6991836e4c4e345ce79e49eb48b460c830623`.

Текущая ветка исходников получила номер `1.0.0-rc5`, потому что в неё добавлены новый native provider Yandex AI Studio и отдельный пакет стабилизации.

## Раннее целевое ТЗ

OmniBridge задумывался как локальный Windows AI gateway с единым приложением `OmniBridge.exe`, независимым Core и следующими подсистемами:

1. Provider Registry для API-провайдеров и локальных runtime.
2. Account Registry для подключений по пользовательской сессии.
3. Model Registry с discovery, capability и состоянием моделей.
4. Model Doctor для реальной проверки протокола, streaming, tools, reasoning и контекстных ограничений.
5. Router для direct/priority/fastest/agent/local/cloud и автоматических стратегий.
6. Protocol Adapter для OpenAI Chat/Responses, Anthropic Messages, Gemini, GigaChat и совместимых API.
7. VS Code Connector, который экспортирует локальный endpoint OmniBridge, а не секреты внешних провайдеров.
8. Защищённое хранение credentials, статистика, health/status, logs, backup/restore/update/rollback.
9. Desktop UI: Dashboard, API Keys, Accounts, Models, Model Doctor, Integration/VS Code, Logs, Settings и About.

Основной сценарий ТЗ: пользователь добавляет API key или аккаунт → OmniBridge обнаруживает и проверяет модели → Model Registry публикует единый каталог → VS Code получает localhost endpoint → Router и Protocol Engine выбирают рабочее подключение. Core должен оставаться работоспособным без UI. Согласованный визуальный контракт сохраняется: UI 3.0, главное окно 1260×740 и Tray Dashboard 1000×672 без редизайна.

## Этапы разработки

| Этап | Версия | Результат |
|---|---|---|
| 1 | `0.3.2-rc6` | RC stabilization: account login/preflight, Tray и rollback hardening |
| 2 | `0.4.1-alpha1` | Persistence 2.0: SQLite WAL, migrations, import JSON, backup/restore |
| 3 | `0.4.2-alpha2` | Provider Engine 3.0: data-driven catalog, executors, request sanitizer |
| 4 | `0.4.3-alpha3` | Resilience 3.0: persistent circuit breaker, failure taxonomy, pools, single-flight |
| 5 | `0.4.4-alpha4` | Router 3.0: weighted/RR/least-used/P2C/quota/cost/auto, session affinity |
| 6 | `0.4.5-alpha5` | Doctor-Aware Routing и четыре технических score |
| 7 | `0.4.6-alpha6` | Functional UI Expansion без смены UI 3.0 |
| 8 | `0.4.7-alpha7` | Protocol Engine 3.0 и canonical request/response/stream contract |
| 9 | `0.4.8-alpha8` | Live cross-protocol SSE и downstream WebSocket transport |
| 10–14 | `0.4.13-alpha13` | Quota & Cost, Context Guard, Integration Hub, MCP, Observability/Remote |
| 15 | `1.0.0-rc1`–`rc4` | Release engineering, VS Code picker/auth, GigaChat TLS, Model UI fixes |
| 16 | `1.0.0-rc5` | Yandex AI Studio и стабилизация тяжёлых операций |

## Полный контекст ветки Yandex

До интеграции в OmniBridge предпринималась прямая настройка VS Code BYOK с провайдером `Yandex AI Studio` (`vendor=customendpoint`). Использовались скрипты:

- `Fix-YandexAI-Copilot-v4.ps1`;
- `Install-YandexAI-CopilotModels.ps1`;
- `Repair-YandexAI-CopilotModels.ps1`;
- `Recover-VSCode-YandexBYOK.ps1`.

Целевой endpoint был `https://ai.api.cloud.yandex.net/v1/chat/completions`; сначала пытались применять Responses, затем Chat Completions как fallback. Требуемая авторизация: `Authorization: Api-Key <Yandex API Key>`. В ветке фигурировали модели:

| Модель | URI | Контекст | Максимальный output |
|---|---|---:|---:|
| YandexGPT 5.1 | `gpt://$FolderId/yandexgpt-5.1` | 32 768 | 8 192 |
| DeepSeek V4 Flash | `gpt://$FolderId/deepseek-v4-flash` | 1 048 576 | 16 384 |
| Qwen3 235B A22B FP8 | `gpt://$FolderId/qwen3-235b-a22b-fp8/latest` | 262 144 | 16 384 |
| GPT OSS 120B | `gpt://$FolderId/gpt-oss-120b/latest` | 131 072 | 16 384 |

Проблема прямого подхода: `chatLanguageModels.json` мог содержать ссылку на Secret Storage, которой не соответствовал реально сохранённый секрет. Скрипты намеренно не записывали ключ открытым текстом, поэтому VS Code отправлял запрос без корректного заголовка и Yandex отвечал `Unauthenticated: IAM token or API key has to be passed`. После массовой регистрации/проверки моделей VS Code создавал высокую нагрузку и не показывал модели; DeepSeek V4 Flash работал, тогда как YandexGPT 5.1 возвращал ошибку. Recovery-скрипт удалял только Yandex BYOK entries и предварительно делал backup пользовательского файла.

Отдельно исследовался prompt cache. Yandex применяет его автоматически к повторяющимся стабильным префиксам (system prompt, tool definitions и ранняя часть диалога). Учитывались оба поля telemetry:

- Chat Completions: `usage.prompt_tokens_details.cached_tokens`;
- Responses: `usage.input_tokens_details.cached_tokens`.

Для Responses клиент может передать `prompt_cache_retention=24h`. Цены не зашиты в Core: Quota & Cost использует только проверенный pricing registry или операторские данные.

## Решение в RC5

Прямая зависимость от VS Code Secret Storage устранена. В Core добавлен native executor `yandex`:

- `GET https://ai.api.cloud.yandex.net/v1/models`;
- Responses API как preferred protocol, Chat Completions как совместимый fallback;
- `Authorization: Api-Key` формируется только в Core;
- Folder ID валидируется отдельно и передаётся как `OpenAI-Project`;
- API key хранится через `CredentialRef`/Secret Store;
- model URI сохраняется без подмены;
- UI получил отдельное поле Folder ID;
- cached input tokens попадают в статистику и Quota & Cost;
- Deep Model Doctor предупреждает о платных запросах и проверяет preferred protocol.

## Стабилизация

RC5 ограничивает тяжёлые операции: 16 generation/embeddings/WebSocket запросов, 2 Model Doctor probes и 4 discovery операции одновременно. При насыщении Core возвращает HTTP 429 `gateway_busy` с `Retry-After`, а не зависает. Добавлены request/background/tray panic recovery, пакетная запись полных snapshots статистики, немедленная запись usage events и `Stats.Flush()` при штатном завершении.

Статическая проверка: 92 уникальных provider definitions, 14/14 XAML документов разбираются, все 65 именованных Button подключены к PowerShell `Add_Click`, 12/12 PowerShell scripts сохраняют UTF-8 BOM, secret-pattern scan пройден. Полные `go test`, `go vet`, UI validation и Windows x64 build вынесены в GitHub Actions, потому что в текущем Linux workspace нет Go и PowerShell runtime.

## Официальные материалы Yandex

- https://yandex.cloud/en/docs/ai-studio/concepts/api
- https://yandex.cloud/en/docs/ai-studio/operations/get-api-key
- https://yandex.cloud/en/docs/ai-studio/models/listModels
- https://yandex.cloud/en/docs/ai-studio/responses/createResponse
