# OmniBridge 1.0.0-rc8 — Cline Bridge и release cleanup

## Исправления

- **Копировать ключ** копирует фактический Local Bridge Key из существующего DPAPI Secret Store.
- **Проверить Cline API** делает `GET /v1/models` с `Authorization: Bearer <Local Bridge Key>` и показывает число моделей.
- `/v1/models` сохраняет OpenAI-compatible форму `object=list` + `data[].id`, которую Cline использует для Model ID.
- Yandex-модели проходят через Local Gateway; Yandex API Key остаётся внутри OmniBridge.
- Удалена release-зависимость от `OmniBridgeSetup.exe`; maintenance встроен в `OmniBridge.exe`.
- Runtime и Source архивы разделены, source очищен от пользовательского state/secrets/EXE.

## Cline

```text
API Provider: OpenAI Compatible
Base URL: http://127.0.0.1:4142/v1
API Key: <Интеграция → Копировать ключ>
Model ID: выбрать после загрузки /v1/models
```
