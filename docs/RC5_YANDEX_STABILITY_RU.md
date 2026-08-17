# OmniBridge 1.0.0-rc5 — Yandex AI Studio и стабилизация

## Контекст

Прямая регистрация Yandex AI Studio в `chatLanguageModels.json` VS Code оказалась хрупкой: ссылка Secret Storage могла не соответствовать реальному секрету, `Authorization: Api-Key` не попадал в запрос, а массовая проверка нескольких протоколов и tool calling создавала высокую нагрузку. В результате YandexGPT 5.1 возвращал `Unauthenticated`, хотя DeepSeek V4 Flash через тот же публичный API работал.

## Native provider

Добавлен provider `yandex` со своим executor и контрактом:

- Base URL: `https://ai.api.cloud.yandex.net/v1`;
- Models: `GET /v1/models`;
- генерация: Responses API и Chat Completions;
- авторизация: `Authorization: Api-Key <key>`;
- каталог: `OpenAI-Project: <Folder ID>`;
- API Key хранится только через `CredentialRef`/Secret Store;
- Folder ID валидируется отдельно и не смешивается с ключом;
- upstream model ID/URI сохраняется без подмены.

Пользователь добавляет Yandex на странице «API Ключи»: выбирает `Yandex AI Studio`, вводит имя профиля, API Key и `YANDEX CLOUD FOLDER ID`, затем нажимает «Проверить и добавить».

## Prompt cache и стоимость

AI Studio применяет prompt cache автоматически. Responses API также принимает `prompt_cache_retention=24h`, если клиенту нужен расширенный срок. OmniBridge не кэширует готовые ответы и не повторяет скрыто запросы: это сохраняет корректность генерации и не маскирует фактическое потребление.

RC5 читает оба варианта usage:

- `usage.prompt_tokens_details.cached_tokens` для Chat Completions;
- `usage.input_tokens_details.cached_tokens` для Responses.

Кэшированные входящие токены сохраняются в статистике и передаются Quota & Cost Engine, поэтому при заданной проверенной цене расчёт использует отдельный cached-input tariff. На Dashboard показатель «Входящие / кэш» показывает обе величины.

## Стабилизация

- не более 16 одновременных generation/embedding/WebSocket запросов;
- не более 2 глубоких Model Doctor probes;
- не более 4 одновременных provider/account discovery операций;
- при перегрузке возвращается нормализованный HTTP 429 `gateway_busy` с `Retry-After`;
- panic в HTTP handler изолируется, получает trace-safe 500 и не завершает Core;
- panic фонового refresh/tray refresh логируется и не завершает приложение;
- полные JSON/SQLite snapshots статистики coalesced, отдельные usage events сохраняются сразу;
- при штатном закрытии выполняется `Stats.Flush()`;
- Yandex deep Doctor проверяет preferred protocol и предупреждает о платных вызовах.

## Совместимость

Главное окно остаётся 1260×740, Tray — 1000×672. Дизайн UI 3.0, GigaChat TLS/CA, ChatGPT/Gemini Account Connectors, Router 3.1, Protocol Engine 3.1 и VS Code export contract 2.2 сохранены.

## Официальные материалы

- https://yandex.cloud/en/docs/ai-studio/concepts/api
- https://yandex.cloud/en/docs/ai-studio/models/
- https://yandex.cloud/en/docs/ai-studio/operations/get-api-key
- https://yandex.cloud/en/docs/ai-studio/concepts/agents/conversations-api
- https://yandex.cloud/en/docs/ai-studio/responses/createResponse
