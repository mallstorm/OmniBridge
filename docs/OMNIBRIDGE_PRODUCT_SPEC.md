# Техническое задание

## OmniBridge — Universal AI Gateway

### 1. Назначение продукта

**OmniBridge** — локальный универсальный AI-шлюз для Windows, который позволяет подключать различные AI-сервисы через единое приложение и предоставлять их модели локальным клиентам через совместимый API.

Главная идея:

> Пользователь один раз подключает API-провайдера или поддерживаемый пользовательский аккаунт, а OmniBridge самостоятельно обнаруживает endpoint, модели, возможности моделей, проверяет их и предоставляет через единый локальный endpoint.

Внешнему приложению не должно быть важно, где физически находится модель:

-  OpenAI; 
-  GigaChat; 
-  Gemini; 
-  Anthropic; 
-  DeepSeek; 
-  Qwen; 
-  OpenRouter; 
-  Groq; 
-  NanoGPT/NanoKeys-подобный сервис; 
-  локальный Ollama; 
-  LM Studio; 
-  vLLM; 
-  LocalAI; 
-  другой OpenAI-compatible API; 
-  поддерживаемый Account Connector. 

Все они должны представляться клиенту через **OmniBridge Gateway**.

---

# 2. Основной пользовательский сценарий

Пользователь распаковывает или устанавливает и запускает единый:

**OmniBridge.exe**

Затем:

```
```

```
Установка
   ↓
Запуск OmniBridge
   ↓
Добавление API Key
   ↓
Определение провайдера / endpoint
   ↓
Получение списка моделей
   ↓
Проверка каждой модели
   ↓
Определение возможностей
   ↓
Формирование Model Registry
   ↓
Выбор рабочих моделей
   ↓
Экспорт в VS Code
   ↓
Использование через localhost OmniBridge
```

Для поддерживаемых Account Connector:

```
```

```
Аккаунты
   ↓
ChatGPT / Gemini
   ↓
Официальная авторизация
   ↓
Возврат в OmniBridge
   ↓
Получение доступных моделей
   ↓
Проверка
   ↓
Model Registry
   ↓
Экспорт в Gateway / VS Code
```

API Keys и Account Login должны оставаться **двумя независимыми способами подключения**.

---

# 3. Архитектура

OmniBridge должен состоять из отдельных логических компонентов.

## 3.1 OmniBridge Core

Основной процесс:

**OmniBridge.exe**

Функции:

-  локальный HTTP API; 
-  Provider Registry; 
-  Account Registry; 
-  Model Registry; 
-  Router; 
-  Protocol Adapter; 
-  statistics engine; 
-  secure credential access; 
-  health/status; 
-  VS Code API; 
-  diagnostics. 

Core остаётся логически отделён от GUI, но поставляется и управляется единым `OmniBridge.exe`.

Сворачивание окна сохраняет Core и Tray активными. Закрытие по крестику полностью завершает окно, Tray и Core.

---

# 4. Основные компоненты

Проект должен включать:

### `OmniBridge.exe`

Единое приложение: локальный AI Gateway, основное окно управления, первоначальная настройка, Account Connector и Tray.

Логические внутренние компоненты:

### OmniBridge Core

Серверная основа.

### OmniBridge Provider Registry

Каталог поддерживаемых AI-провайдеров.

### OmniBridge Model Registry

Единый каталог найденных моделей.

### OmniBridge Model Doctor

Практическая диагностика моделей.

### OmniBridge Router

Выбор провайдера/модели и маршрутизация запросов.

### OmniBridge Protocol Adapter

Конвертация форматов между API.

### OmniBridge VS Code Connector

Интеграция с VS Code.

### OmniBridge Account Connector

Поддерживаемая официальная пользовательская авторизация.

---

# 5. Локальный Gateway

По умолчанию:

```
```

```
127.0.0.1:4142
```

Сервер должен слушать только localhost, если пользователь явно не включил другое поведение.

Предоставить единый API примерно следующего класса:

```
```

```
/v1/models
/v1/chat/completions
/v1/responses
/v1/embeddings
```

где соответствующий upstream это поддерживает.

Дополнительно internal/admin API:

```
```

```
/health
/bridge/status
/bridge/providers
/bridge/accounts
/bridge/models
/bridge/probe
/bridge/statistics
/bridge/router
/bridge/vscode
```

Admin API защищается локальным ключом.

---

# 6. Provider Registry

Необходимо создать расширяемый реестр AI-провайдеров.

Каждый provider definition должен описывать:

```
```

```
Provider ID
Display Name
API family
Default Base URL
Authentication type
Models endpoint
Chat endpoint
Responses endpoint
Embeddings endpoint
Streaming support
Tool calling convention
Vision convention
Error mapping
Rate-limit mapping
Usage mapping
Special headers
Special TLS behaviour
```

Провайдер не должен быть захардкожен непосредственно в GUI.

GUI получает каталог через Core.

---

# 7. Поддерживаемые семейства API

Архитектура должна поддерживать как минимум:

### OpenAI-compatible

```
```

```
/v1/models
/v1/chat/completions
/v1/responses
```

### Anthropic

Messages API и соответствующее преобразование tool calls.

### Google Gemini

Gemini API и streaming.

### GigaChat

OAuth/API authorization и особенности GigaChat.

### Custom API

Ручной Base URL + API key.

### Local AI

Ollama, LM Studio, vLLM, LocalAI и совместимые endpoints.

---

# 8. API Key Connection

При добавлении подключения пользователь вводит минимум:

```
```

```
Provider
Profile Name
Base URL — если требуется
API Key / Authorization Key
```

Для GigaChat дополнительно:

```
```

```
Scope
```

После нажатия:

**Проверить и добавить**

OmniBridge выполняет автоматическую процедуру.

---

# 9. Automatic Endpoint Discovery

Для неизвестного API нужно реализовать **Auto Provider / Endpoint Doctor**.

Проверять безопасно наиболее распространённые варианты:

```
```

```
/v1/models
/models

/v1/chat/completions
/chat/completions

/v1/responses

/api/tags
/api/chat
```

Также анализировать:

-  HTTP status; 
-  headers; 
-  JSON structure; 
-  error format; 
-  provider-specific markers. 

В результате определять вероятное API family.

Например:

```
```

```
OpenAI compatible
Anthropic compatible
Gemini
Ollama
Unknown / Custom
```

---

# 10. Model Discovery

После подключения провайдера OmniBridge должен автоматически получить доступные модели.

Результаты помещаются в единый **Model Registry**.

Каждая модель получает внутреннюю запись:

```
```

```
Internal Model ID
Native Model ID
Display Name
Provider ID
Provider Name
Connection ID
Source
API protocol
Context window
Capabilities
Status
Latency
Last checked
Error state
```

---

# 11. Model Doctor

Model Doctor является критическим компонентом.

Недостаточно доверять `/models`.

Модель нужно **практически проверить**.

Для каждой модели последовательно тестировать:

### Basic Chat

Простейший запрос.

### Streaming

Работает ли потоковая выдача.

### System Prompt

Поддерживается ли system/instructions.

### Tool Calling

Проверка function/tool calling.

### Parallel Tools

Если заявлено.

### JSON / Structured output

Если доступно.

### Vision

Если модель заявлена мультимодальной.

### Reasoning

Определить поддержку соответствующего режима.

### Agent compatibility

Подходит ли модель для агентского использования.

---

# 12. Классификация моделей

После Doctor модель получает статус.

Например:

### Agent Ready

Работает chat + streaming + tools и удовлетворяет требованиям Agent Bridge.

### Chat Ready

Работает нормальный chat.

### Chat Only

Основной chat работает, но tools/agent отсутствуют.

### Partial

Работает не весь необходимый функционал.

### Error

Модель недоступна.

### Rate Limited

Временно ограничена провайдером.

Это особенно важно для нестандартных API, где наличие модели в `/models` ещё не означает её реальную работоспособность.

---

# 13. Agent Compatibility Layer

Отдельный слой должен адаптировать нестандартные модели для клиентов, ожидающих OpenAI/Copilot-подобное поведение.

Задача:

```
```

```
Client request
      ↓
OmniBridge Agent Adapter
      ↓
Provider-specific format
      ↓
AI provider
      ↓
Normalized tool calls
      ↓
Client
```

Обрабатывать:

-  tool definitions; 
-  tool\_choice; 
-  function arguments; 
-  streaming tool calls; 
-  malformed tool JSON; 
-  finish reasons; 
-  parallel calls; 
-  provider-specific message roles. 

Это должно решать проблемы класса, которые возникали при адаптации нестандартных API под Agent Mode.

---

# 14. Protocol Translation Layer

Необходимо обеспечить преобразования между основными API-family.

Например:

```
```

```
OpenAI request
      ↓
Anthropic adapter
      ↓
Claude
```

или:

```
```

```
OpenAI request
      ↓
Gemini adapter
      ↓
Gemini
```

Обратный ответ нормализуется в формат клиента.

Adapter должен поддерживать:

-  messages; 
-  system instructions; 
-  content blocks; 
-  image references; 
-  tools; 
-  tool results; 
-  streaming; 
-  usage; 
-  errors. 

---

# 15. Router

OmniBridge Router отвечает за выбор upstream.

Поддержать несколько режимов.

### Direct

Конкретная модель всегда отправляется конкретному connection.

### Alias

Например:

```
```

```
coding-fast
reasoning
vision
cheap-chat
```

каждый alias связан с моделью.

### Priority

Если несколько одинаковых моделей:

```
```

```
Provider A
 ↓ fail
Provider B
 ↓ fail
Provider C
```

### Fallback

При:

```
```

```
429
provider unavailable
timeout
temporary capacity error
```

допускается автоматический переход на резервную модель, если это разрешено политикой.

---

# 16. Routing Policies

В перспективе предусмотреть:

```
```

```
Fastest
Cheapest
Highest quality
Agent Ready only
Local only
Cloud only
Specific provider
Manual
```

Но первая релизная версия может начинаться с:

-  Direct; 
-  Priority; 
-  Fallback. 

---

# 17. Account Connectors

Account authentication хранится отдельно от API key connections.

Первоначально поддерживаемые официальные Account Connector:

### ChatGPT / Codex

Использовать поддерживаемую официальную схему авторизации/локального клиента, без хранения пароля пользователя.

### Google Gemini

OAuth authorization.

Пользователь должен видеть:

```
```

```
Account
Email
Connection status
Plan — если доступно
Models
Limits — если API предоставляет
Session state
```

Не смешивать эту систему с API Keys.

---

# 18. Credential Security

Секреты не должны храниться открытым текстом.

На Windows использовать:

**DPAPI**

Отдельно хранить:

```
```

```
config.json
```

и:

```
```

```
secrets storage
```

Config содержит структуру, но не открытые API keys.

Логи никогда не должны содержать полный:

-  API Key; 
-  authorization token; 
-  refresh token; 
-  session cookie; 
-  bridge key. 

---

# 19. Model Registry Persistence

Registry должен сохраняться на диск.

Например:

```
```

```
data/model-registry.json
```

При старте можно мгновенно показать последний известный каталог, после чего выполнить background refresh.

Модель должна хранить:

```
```

```
provider
native model id
capabilities
latency
last successful check
last error
status
```

---

# 20. Statistics Engine

OmniBridge должен самостоятельно учитывать запросы.

Минимально:

```
```

```
Requests
Successful
Failed
Input tokens
Output tokens
Total tokens
Latency
Provider
Model
Timestamp
```

Статистика:

```
```

```
Current session
24 hours
7 days
30 days
All time
```

---

# 21. Provider Quota / Subscription Statistics

Если provider API отдаёт информацию о:

-  quota; 
-  credits; 
-  plan; 
-  requests remaining; 
-  token limits; 
-  reset time; 

OmniBridge должен её получать.

Но архитектурно нужно учитывать:

> Не у всех провайдеров существует официальный API для просмотра тарифа или остатка.

Поэтому иметь состояния:

```
```

```
Available
Unavailable
Unsupported
Unknown
```

Никогда не вычислять «остаток тарифа» вымышленно.

---

# 22. Rate Limit Handling

Нужно унифицировать ошибки вида:

```
```

```
429
rate_limit_exceeded
no_capacity
quota_exceeded
temporarily_unavailable
```

Internal normalized error:

```
```

```
upstream_provider_rate_limit
upstream_provider_capacity
upstream_provider_quota
upstream_timeout
upstream_auth
```

В UI пользователь видит нормальное сообщение.

---

# 23. Retry Engine

Предусмотреть controlled retry.

Например:

```
```

```
attempt 1
1000 ms
attempt 2
2000 ms
attempt 3
```

Но:

-  не retry authentication failures; 
-  не бесконечно retry quota; 
-  учитывать Retry-After; 
-  отменять по client cancellation. 

---

# 24. VS Code Integration

OmniBridge должен автоматически добавлять только **проверенные рабочие модели**.

Сценарий:

```
```

```
Model Doctor
     ↓
Model Registry
     ↓
Agent Ready / Chat Ready
     ↓
VS Code Connector
     ↓
chatLanguageModels configuration
```

Не экспортировать модели со статусом `Error`.

Существующую пользовательскую конфигурацию VS Code нельзя уничтожать.

OmniBridge управляет только своей секцией.

---

# 25. Local Model Discovery

Кнопка:

**Найти локальные AI**

должна проверять локальные runtime.

Минимум:

-  Ollama; 
-  LM Studio; 
-  LocalAI; 
-  vLLM; 
-  другие OpenAI-compatible localhost endpoints. 

Обнаруженный runtime не добавлять без проверки.

Сначала:

```
```

```
detect
→ models
→ probe
→ add
```

---

# 26. Multiple Connections

OmniBridge должен поддерживать несколько ключей одного провайдера.

Например:

```
```

```
OpenRouter Personal
OpenRouter Work
OpenRouter Backup
```

или:

```
```

```
GigaChat Account 1
GigaChat Account 2
```

Модель в Registry привязывается не просто к provider type, а к конкретному connection.

---

# 27. Connection Health

Для каждого подключения хранить:

```
```

```
Connected
Authentication error
Rate limited
Quota exhausted
Timeout
Offline
Unknown
```

Фоновая проверка не должна генерировать большой расход токенов.

Для health использовать дешёвый endpoint, где возможно.

---

# 28. Logs

Разделить:

```
```

```
application log
server log
provider log
doctor log
integration log
```

Уровни:

```
```

```
DEBUG
INFO
WARN
ERROR
```

Секреты обязательно редактируются/redact.

---

# 29. Tray Client

Tray является компактным контроллером Core внутри того же lifecycle и не запускается отдельным EXE.

ЛКМ:

-  открыть основной клиент. 

ПКМ:

```
```

```
Открыть OmniBridge
API Ключи
Аккаунты
Модели
Model Doctor
Логи
Интеграция
Перезапустить сервер
Выход
```

Tray не должен содержать вторую полноценную копию основного интерфейса.

---

# 30. Server Lifecycle

GUI и Core логически разделены, но работают под управлением единого `OmniBridge.exe`.

Сворачивание скрывает окно в Tray. Закрытие окна или команда «Выход» из Tray полностью останавливают Core и процесс приложения.

Возможны состояния:

```
```

```
Stopped
Starting
Ready
Degraded
Error
Stopping
```

GUI не должен считать сервер готовым только потому, что процесс существует.

Использовать `/health`.

---

# 31. Configuration

Главная конфигурация должна описывать:

```
```

```
server
providers
accounts
routing
model registry
statistics
integration
logging
security
```

Нужно предусмотреть миграцию формата config при обновлении.

---

# 32. Backup / Restore

Пользователь должен иметь возможность:

-  создать backup; 
-  восстановить backup. 

Резервная копия может включать:

```
```

```
config
registry
routing rules
statistics
```

Работу с секретами выполнять отдельно и безопасно.

---

# 33. Update Architecture

Предусмотреть встроенную систему обновления в будущем.

Обновление не должно уничтожать:

```
```

```
data/
config
accounts
providers
registry
statistics
```

Бинарники и пользовательские данные должны быть физически/логически разделены.

---

# 34. Portable и Installed Mode

Желательно поддержать два варианта.

### Installed

Через единый **OmniBridge.exe** и встроенный раздел первоначальной настройки.

### Portable

ZIP:

```
```

```
OmniBridge.exe
assets/
scripts/
ui/
data/
```

Но перенос данных между разными portable-папками не должен происходить автоматически без контроля пользователя — это ранее приводило к фантомным состояниям.

---

# 35. Setup

Setup должен:

1.  проверить Windows; 
2.  создать структуру; 
3.  установить файлы; 
4.  подготовить data; 
5.  создать local admin key; 
6.  запустить Core; 
7.  проверить `/health`; 
8.  предложить добавить первый provider; 
9.  обнаружить модели; 
10.  предложить VS Code integration; 
11.  настроить tray/autostart. 

---

# 36. Отказоустойчивость

Если один provider сломан, OmniBridge должен продолжать работать с остальными.

Например:

```
```

```
OpenAI OK
Gemini OK
Provider X ERROR
```

Общий Gateway остаётся:

**READY / DEGRADED**

а не падает полностью.

---

# 37. Производительность

Основные требования:

-  Core стартует быстро; 
-  UI открывается без ожидания всех provider probe; 
-  обнаружение моделей выполняется параллельно с ограничением concurrency; 
-  запросы streaming не буферизуются целиком; 
-  Gateway не должен существенно увеличивать latency. 

Целевой overhead локального bridge:

**минимально возможный**, ориентировочно единицы–десятки миллисекунд без учёта protocol translation.

---

# 38. Cancellation

Если VS Code/клиент отменяет запрос:

```
```

```
Client cancellation
      ↓
OmniBridge
      ↓
cancel upstream request
```

Нельзя продолжать генерацию и тратить quota после закрытия запроса клиентом.

---

# 39. Streaming

Streaming является обязательной частью шлюза.

OmniBridge должен корректно обрабатывать:

-  SSE; 
-  chunks; 
-  tool-call deltas; 
-  stop events; 
-  usage; 
-  provider disconnect; 
-  cancellation. 

---

# 40. Security Boundary

По умолчанию:

```
```

```
localhost only
```

Запретить без явного разрешения:

```
```

```
0.0.0.0
public exposure
```

Если позже добавить LAN access, он должен иметь отдельную аутентификацию и предупреждение.

---

# 41. Главный принцип продукта

Пользователь не должен заниматься ручной адаптацией API.

Он должен сделать:

```
```

```
Вставил ключ
      ↓
OmniBridge всё проверил
      ↓
Показал модели
      ↓
Отметил рабочие возможности
      ↓
Подключил к VS Code
```

Именно **автоматическая диагностика и нормализация** отличает OmniBridge от обычного reverse proxy.

---

# 42. Релизные критерии

Полноценный OmniBridge считается реализованным, когда:

1.  Можно добавить несколько разных AI API. 
2.  Поддерживается Custom/OpenAI-compatible endpoint. 
3.  Endpoint определяется и проверяется автоматически. 
4.  Модели обнаруживаются автоматически. 
5.  Model Doctor реально проверяет capabilities. 
6.  Есть единый Model Registry. 
7.  Есть нормализованный `/v1/...` API. 
8.  Streaming работает. 
9.  Tool Calling работает через adapters. 
10.  Agent Ready определяется практической проверкой. 
11.  Можно иметь несколько providers одновременно. 
12.  Router корректно выбирает upstream. 
13.  Ошибка одного provider не рушит Gateway. 
14.  ChatGPT Account и Gemini Account отделены от API Keys. 
15.  Credentials безопасно хранятся. 
16.  VS Code получает только рабочие модели. 
17.  Statistics считает реальные запросы/токены. 
18.  Quota отображается только там, где upstream реально её предоставляет. 
19.  Local AI обнаруживаются. 
20.  Tray управляет Core. 
21.  Core работает независимо от Desktop. 
22.  Logs не содержат секретов. 
23.  Есть backup/migration. 
24.  Обновление не уничтожает пользовательские подключения. 
25.  UI является только управляющим слоем, а не местом, где реализована серверная логика. 

---

# План реализации самого мультимоста

Я бы разделил разработку **самого OmniBridge**, независимо от UI-редизайна, на 10 крупных пакетов.

### Пакет 1 — Core Gateway

HTTP server, config, secrets, health, lifecycle, localhost API.

### Пакет 2 — Provider Framework

Provider Registry, connection abstraction, OpenAI-compatible, custom endpoints.

### Пакет 3 — Model Discovery

`/models`, Model Registry, persistence, refresh.

### Пакет 4 — Model Doctor

Basic/Streaming/Tools/Vision/Reasoning/Agent probes.

### Пакет 5 — Protocol Adapters

OpenAI ↔ Anthropic ↔ Gemini ↔ GigaChat normalization.

### Пакет 6 — Agent Bridge

Tool Calling, streaming tools, agent compatibility, malformed upstream recovery.

### Пакет 7 — Router

Direct routing, aliases, priority, fallback, retries, rate limits.

### Пакет 8 — Account Connectors

ChatGPT/Codex и Gemini, отдельное credential/session storage.

### Пакет 9 — Integration & Observability

VS Code Connector, stats, quotas, logs, local AI discovery.

### Пакет 10 — Release Engineering

Setup, tray, backup, migration, portable/installed builds, updater readiness, regression suite.
