# OmniBridge 1.0.0-rc3 — VS Code auth, GigaChat TLS и Model UI

## 1. VS Code: invalid local bridge key

RC3 проверяет текущий Data API Key против работающего Core до изменения VS Code. `GET /bridge/vscode/export` публикует fingerprint ключа, а sync сравнивает его с ключом, расшифрованным из DPAPI SecretStore.

В `chatLanguageModels.json` provider `OmniBridge` получает текущий локальный `apiKey`, а каждая модель — `requestHeaders.x-bridge-key = ${apiKey}`. VS Code подставляет provider API key при выполнении запроса. Старые OmniBridge/Universal Bridge Custom Endpoint записи на localhost:4142 удаляются перед safe merge. После записи файл читается обратно и проверяется.

Core принимает Bearer, x-api-key, api-key и x-bridge-key. Bearer scheme сравнивается без учёта регистра.

## 2. GigaChat TLS

Russian Trusted Root CA встроен непосредственно в Core через Go embed. GigaChat HTTP client строит trust pool как Windows/system roots + встроенный Russian Trusted Root CA + необязательный legacy extra bundle.

TLS validation не отключается. `InsecureSkipVerify` не используется. Windows certificate store не изменяется. `scripts/install-gigachat-cert.ps1` оставлен только как compatibility helper и копирует уже поставляемый с OmniBridge сертификат, не скачивая его из сети.

Secret-safe состояние доступно в `GET /bridge/provider-engine` -> `gigachat_tls`.

## 3. Иконки моделей

Model list больше не использует один generic cube. `Model-Icon` сначала определяет модельное семейство (GPT/OpenAI, Claude, Gemini, DeepSeek, Qwen, Llama, Mistral, Gemma, Kimi, GLM, MiniMax, GigaChat, Grok, Cohere, Phi и др.), затем provider icon, затем generic fallback.

Пакет содержит 91 provider icon и 20 family icons. Для известных брендов используются существующие брендовые assets, для остальных — локальные branded-monogram assets. Иконки применяются и в Models, и в Model Doctor.

## 4. Model Doctor scores

`doctor_score`, `agent_score`, `coding_score`, `reliability_score` всегда присутствуют в Model Registry JSON. После deep probe Models page восстанавливает выбранную модель и немедленно обновляет правую панель. Verified zero отображается как 0; непроверенная модель получает явный badge `Не проверено` вместо ложного Agent Ready.

Полный Doctor по подключению обновляет Models, Doctor report и Dashboard после завершения.

## 5. Читаемость UI

Все явные FontSize в XAML увеличены на +1 относительно RC2. Для TextBlock добавлен глобальный размер 13. Основной и вторичный текст стали светлее. Размер окна остаётся 1260×740, Tray — 1000×672; компоновка UI 3.0 не перерабатывалась.
