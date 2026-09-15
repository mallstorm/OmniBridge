# OmniBridge 1.0.0-rc8 — Release Report

## Исправлено

1. Local Bridge Key раньше отображался только маской и не мог быть передан Cline — добавлено безопасное копирование.
2. Добавлена реальная Cline-проверка `GET /v1/models` с Bearer Local Bridge Key и отображение количества моделей.
3. Подтверждён OpenAI-compatible model catalog (`object=list`, `data[].id`) для автоматического Model ID.
4. Устранены stale ссылки release pipeline на удалённый `cmd/setup` / `OmniBridgeSetup.exe`.
5. Runtime и source разнесены; пользовательские `data/`, SQLite/DPAPI secrets, logs и compiled EXE не публикуются в source.
6. Windows manifest синхронизирован с `1.0.0.8`.

## Проверки

- gofmt: PASS
- go test ./...: PASS
- go vet ./...: PASS
- Windows amd64 cross-build integrated `OmniBridge.exe`: PASS
- XAML parse/UI contract: PASS
- OpenAI-compatible `/v1/models` Bearer regression: PASS

Native Windows WPF/DPAPI smoke test в Linux-среде не заявляется.
