# OmniBridge 1.0.0-rc6 — Yandex AI Studio: имена моделей, VS Code и маршрутизация

Дата: 23 августа 2026.

## Что исправлено относительно RC5

RC6 базируется на RC5 и не убирает ни одну его функцию (тесты, vet и UI-валидация проходят полностью). Изменения касаются только Yandex AI Studio и устойчивости маршрутизации по native-URI.

1. **Корректные имена моделей при добавлении.**
   Discovery `/v1/models` отдаёт «сырые» id вида `gpt://<folder>/yandexgpt-5.1` или `yandexgpt/latest`. В RC6 каждый найденный Yandex-слаг приводится к читаемому имени:

   | Slug | Имя в OmniBridge и VS Code |
   |---|---|
   | `yandexgpt/latest`, `yandexgpt` | YandexGPT |
   | `yandexgpt-5.1` | YandexGPT 5.1 |
   | `yandexgpt-pro` / `yandexgpt-lite` | YandexGPT Pro / Lite |
   | `deepseek-v4-flash` | DeepSeek V4 Flash |
   | `qwen3-235b-a22b-fp8` | Qwen3 235B A22B FP8 |
   | `gpt-oss-120b` | GPT-OSS 120B |
   | неизвестные семейства | «DeepSeek V3»-подобный производный формат |

   Для известных семейств заполняются context window и max output tokens, а также флаги
   Tool Calling/Reasoning, поэтому строка модели в UI и пикер VS Code сразу выглядят
   осмысленно, до прогона Model Doctor.

2. **Канонический model URI.**
   Если `/v1/models` вернул короткий id без `gpt://`, он приводится к
   `gpt://<FolderId>/<slug>` ещё на этапе discovery. В результате и Model Registry,
   и VS Code export, и исходящий запрос всегда используют один и тот же корректный URI.

3. **Разрешение запросов по «сырому» URI.**
   Раньше запрос с `model: "gpt://<folder>/yandexgpt/latest"` (старая BYOK-запись VS Code,
   ручные запросы) возвращал «model not found»: id содержит «/» и сравнивался только с
   полным ключом реестра. Теперь маршрутизация умеет находить модель по:
   - полному ключу реестра (`yandex/gpt://<folder>/…`);
   - точному native id (`gpt://<folder>/…`);
   - короткому слагу (`yandexgpt/latest`).

4. **`GET /v1/models` шлюза** теперь возвращает `name` и `native_id` в дополнение к `id`,
   что помогает пикеру VS Code показывать читаемые модели.

5. **UX добавления провайдера:** Folder ID обязателен (валидация и в UI, и в Core);
   тост после добавления показывает количество найденных моделей.

## RC6.1 — фиксы интерфейса и дополнительная адаптация Yandex

- **UI: исправлен обрыв отрисовки списков моделей.** В `Model-Icon` использовалась
  переменная `$pid` — имя зарезервировано PowerShell и доступно только для чтения.
  Присваивание бросало исключение, из-за которого страницы «Модели», Dashboard
  («Недавно проверенные модели») и Model Doctor не заполнялись: каталог показывал
  «Моделей: 0» при реально подключённом Yandex. Переменная переименована
  (в validated UI-сборке ошибка «Не удается перезаписать переменную PID» больше не появляется).
- **Добавлены актуальные слаги YandexGPT 5-й линейки** (`yandexgpt-5`,
  `yandexgpt-5-pro`, `yandexgpt-5-lite`, с `latest` и без) в карту метаданных —
  discovery больше не показывает сырые id как имена моделей.
- **Читаемые имена для неизвестных Yandex-моделей.** Если `/v1/models` вернул id
  в виде слага (нижний регистр, дефисы, `/latest`) без имени, Core подставляет
  производное читаемое имя вместо «yandexgpt-5-pro» в реестре и VS Code export.
- **VS Code export не получает не-чатовые Yandex-модели.** Голосовые/реалтайм/
  TTS/STT/embedding-эндпоинты AI Studio (например `speech-realtime-deepseek-v4-flash`)
  остаются в Model Registry для discovery и Model Doctor, но исключаются из
  `chatLanguageModels.json`, чтобы Copilot Chat и Agent-пикер не засорялись
  моделями, не умеющими вести диалог.

## Что осталось прежним

- Yandex-ключ хранится только в Secret Store (`CredentialRef`), в конфиг не пишется.
- Авторизация `Authorization: Api-Key <key>` + `OpenAI-Project: <FolderId>` формируются в Core.
- Responses API остаётся preferred protocol, Chat Completions — совместимый fallback.
- Ограничения параллелизма, recovery от паник, cached-token telemetry, coalesced статистика — без изменений.

## Порядок проверки для релиза

1. `go build ./...`
2. `go test ./...`
3. `go vet ./...`
4. `./scripts/validate-ui.ps1 -Root .`
5. `./scripts/build-release.ps1 -Root .`