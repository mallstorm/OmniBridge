# Этап 6 — Doctor-Aware Routing / OmniBridge 0.4.5-alpha5

Этап связывает Router 3.0 с фактически подтверждёнными результатами Model Doctor.

## Что изменилось

- входящий Chat/Responses/Messages запрос анализируется до выбора upstream;
- выделяются требования: streaming, system/developer prompt, tools, parallel tools, structured output, vision, reasoning, agent loop, примерный input/output/context budget;
- для `auto/*` capability-sensitive запросы работают fail-closed: непроверенная Model Doctor модель не выдаётся за совместимую;
- явно выбранная пользователем непроверенная модель не блокируется только из-за отсутствия deep Doctor proof — это считается unknown и остаётся совместимым с прежним Direct поведением;
- известный `context_window` и `max_output_tokens` участвуют в preflight;
- Model Doctor рассчитывает `DoctorScore`, `AgentScore`, `CodingScore`, `ReliabilityScore` 0..100;
- Auto Router учитывает эти scores вместе с runtime health/latency/success/quota/cost signals;
- добавлен dry-run `POST /bridge/router/evaluate?api_type=...`, который не вызывает upstream;
- глубокие Doctor results теперь переживают обычный Model Registry refresh и не теряются при повторном discovery;
- добавлено `doctor_checked_at` и признак `doctor_verified`.

## Правило доверия capability

Provider/model metadata остаётся полезным discovery-сигналом, но для `auto/*` запросов с Tools/Vision/Reasoning/Agent/Structured requirements OmniBridge требует deep Doctor evidence. Это предотвращает выбор модели только потому, что каталог ошибочно объявил функцию поддерживаемой.

Для Direct route политика мягче: если deep Doctor ещё не запускался, неизвестная capability не считается доказанным запретом. Если Doctor уже проверил модель и функция не прошла, несовместимый запрос отклоняется до upstream.

## Dry-run диагностика

`POST /bridge/router/evaluate?api_type=chat-completions`

Body — обычный API request, например:

```json
{
  "model": "auto/smart",
  "messages": [{"role":"user","content":"Use the tool"}],
  "tools": [{"type":"function","function":{"name":"read_file","parameters":{"type":"object"}}}],
  "tool_choice": "required"
}
```

Ответ содержит только безопасные производные данные: inferred requirements, candidate order и score breakdown. Prompt целиком в diagnostics не сохраняется.

## Scores

Scores не пытаются оценивать «интеллект» модели. Они отражают проверенную техническую пригодность:

- `DoctorScore` — общий coverage deep probes;
- `AgentScore` — tool-loop/system/streaming/parallel пригодность;
- `CodingScore` — coding-agent protocol compatibility;
- `ReliabilityScore` — стабильность фактических Doctor protocol probes.

Runtime Success Rate остаётся отдельным сигналом Router и не смешивается с Doctor reliability при хранении.

## Следующий этап

Этап 7 — Functional UI Expansion: окно 1260×740, прежняя дизайн-система UI 3.0, вывод Router/Doctor/Health/Connection Pool данных без редизайна приложения.
