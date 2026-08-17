# Этап 11 — Context Guard

Context Guard выполняется до маршрутизации, когда известен проверенный context window. Он использует консервативную tokenizer-independent оценку и режимы `observe`, `compact`, `off`.

При compaction сохраняются system/developer instructions и последние сообщения; удаляются только старые removable messages. Скрытая LLM-суммаризация не выполняется, поэтому Guard не создаёт незаметных расходов и не подменяет содержание модели. Результат снова проходит Protocol Engine repair. События и число сэкономленных токенов сохраняются в SQLite.

Admin API: `/bridge/context-guard`, `/bridge/context-guard/preview`.
