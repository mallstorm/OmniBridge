# OmniBridge 1.0.0-rc7: Verified Models и фактическая identity

## Что изменено

- Discovery, Chat Verified, Agent Ready и Identity Verified теперь являются разными состояниями.
- В VS Code экспортируются только модели с успешным базовым генеративным запросом.
- `toolCalling` для VS Code выставляется только после полного tool round-trip и identity probe.
- Yandex curated-кандидаты имеют `source=curated`, `status=unverified` и не получают статические runtime-capabilities.
- GigaChat при `401` обновляет OAuth token и повторяет запрос только один раз.
- После окончательного выбора модели, включая fallback, OmniBridge добавляет identity instruction для Chat Completions, Responses и Messages.
- Ответ содержит фактические route metadata в HTTP headers и JSON/SSE model metadata.
- Автоматические маршруты требуют доказанного Chat probe; ручной physical route остаётся доступен для диагностики.
- Desktop UI поддерживает сортировку моделей, запуск Doctor для всех подключений или только ошибок и ручные model pools.

## Заголовки ответа

- `X-OmniBridge-Selected-Model`
- `X-OmniBridge-Provider`
- `X-OmniBridge-Native-Model`
- `X-OmniBridge-Route`
- `X-OmniBridge-Fallback-Used`

Эти заголовки являются источником истины. Текстовая самоидентификация LLM используется как дополнительная проверка, а не как единственное доказательство маршрута.

## Допуск модели в VS Code

1. Provider discovery создаёт запись `discovered` или `unverified`.
2. Basic probe устанавливает `chat_verified`.
3. Deep Doctor проверяет streaming, system, tools, tool result, structured output, vision, reasoning и identity.
4. Только `chat_verified` попадает в Chat picker.
5. Только `agent_ready` вместе с `identity_verified` попадает в Agent picker.

После изменения API key, endpoint, Yandex Folder ID или GigaChat scope старые результаты проверки подключения сбрасываются.

## Ручные пулы

В настройках Router можно выбрать несколько проверенных моделей, порядок fallback, стратегию `priority`, `weighted`, `round_robin`, `fastest`, `cost_optimized`, `agent_ready_only` или `local_only`, веса и session affinity. Старые одиночные aliases продолжают загружаться.

## Проверка в VS Code

1. Добавить API provider.
2. Запустить Model Doctor.
3. Выполнить синхронизацию VS Code.
4. Полностью перезапустить VS Code.
5. Включить видимость нужной OmniBridge-модели.
6. Выбрать её в Chat или Agent picker.
7. Спросить: `Какая ты модель и через какого провайдера подключена?`
8. Сверить ответ с route metadata в диагностике OmniBridge.
