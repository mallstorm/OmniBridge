# OmniBridge Tray Dashboard — 0.4.4-alpha4

## Назначение
Кастомная двухсекционная Tray-панель заменяет стандартное Windows ContextMenu. Основной UI 3.0 не изменяется.

## Поведение
- Левый или правый клик по значку OmniBridge: показать/скрыть Tray Dashboard.
- Двойной клик: открыть главное окно OmniBridge.
- Клик вне панели: скрыть Tray Dashboard.
- Панель не отображается в taskbar и позиционируется у рабочей области того монитора, где находится курсор/Tray.

## Левая секция
Показывает только реальные данные из `GET /bridge/tray`:
- lifecycle/endpoint/uptime;
- количество включённых и реально работающих API connections;
- подключённые Account Connector;
- доступные модели и Agent Ready;
- Requests/Errors/Input Tokens/Output Tokens/AVG/P95/Success Rate за 24 часа;
- условную карточку GigaChat.

### GigaChat
Карточка появляется только если настроен хотя бы один enabled provider kind=`gigachat`.
Показываются:
- токены запросов за 24 часа и текущую сессию;
- средняя скорость расхода токенов текущей сессии;
- реальный срок жизни **OAuth access token** из внутреннего cache, если токен уже был получен.

OAuth lifetime не выдаётся за месячную quota. Лимит 36M и прочие цифры не рисуются, если upstream их достоверно не предоставил. При нескольких GigaChat connections кнопка в заголовке карточки переключает подключения.

## Правая секция
- Запустить сервер — снимает безопасную паузу Gateway data plane.
- Остановить сервер — переводит Gateway data plane в paused, но оставляет admin/control plane и Tray доступными.
- Обновить сессии — асинхронно обновляет Account Connector, provider health и Model Registry через `POST /bridge/tray/refresh`.
- Проверить обновления — использует `data/update-feed.json`, если источник обновлений настроен. Автоустановки нет.
- Настройки — существующая страница Settings.
- Открыть панель / Статистика — существующий Dashboard.
- Интеграция VS Code — существующая Integration page.
- Справка — папка docs.
- Выход — полностью закрывает OmniBridge.

## API
- `GET /bridge/tray`
- `POST /bridge/tray/start`
- `POST /bridge/tray/stop`
- `POST /bridge/tray/refresh`

`/bridge/tray` не возвращает API keys, bridge keys, access tokens, refresh tokens, cookies или другие credentials.

## Частота обновления
Пока Tray закрыт, отдельного polling нет. Пока панель открыта:
- локальный countdown GigaChat обновляется раз в 1 секунду без сетевого запроса;
- агрегированное локальное состояние Core обновляется раз в 3 секунды.

Долгий provider/account refresh запускается в Go в фоне и не блокирует WPF UI.

## Update feed
Опциональный `data/update-feed.json` может содержать либо:

```json
{"manifest_url":"https://example.invalid/omnibridge/manifest.json"}
```

либо локально заданную версию для теста:

```json
{"version":"0.4.4-alpha4"}
```

Если feed не настроен, Tray честно показывает «Источник обновлений не настроен».
