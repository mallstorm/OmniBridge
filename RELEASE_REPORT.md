# OmniBridge 1.0.0-rc8 — Release Report

## Назначение

RC8 развивает проверенную базу RC7: сохраняет физическую identity и Doctor gates, добавляет эксплуатационную обратную связь для Router, объяснение выбора маршрута, Health Autopilot, quota intelligence, безопасную экономию повторных запросов и контролируемое расширение каталога.

## Adaptive Intelligence

- bounded async outcome events;
- EWMA success/latency/TTFT и confidence;
- мягкий `adaptive_quality` component в Router Score без ослабления hard gates;
- `Route Decision Trace` и `/bridge/router/explain` без prompt/secret payload;
- Health Autopilot с dry-run, evidence и `preconditions_hash`;
- GigaChat OAuth refresh singleflight и bounded retry после 401;
- quota reset windows, burn rate, predicted exhaustion и `quota_share`;
- VS Code self-test только для `ChatVerified` модели с provider/native/upstream identity.

## Exact Response Cache и generation dedup

RC8 использует консервативный контракт:

- Exact Cache выключен по умолчанию;
- запрос кэшируется/дедуплицируется только при явно переданном `temperature=0`;
- отсутствие `temperature`, `null` и любое ненулевое значение не считаются детерминированными;
- streaming и запросы с tools не кэшируются;
- signature включает namespace и физические provider/model/native/identity данные;
- реализованы TTL и настоящий LRU touch/eviction;
- временные dedup-results имеют короткий срок жизни и очищаются;
- cache clear удаляет retained cache и завершённые dedup-results.

Это исключает возврат ответа одной физической модели под identity другой и не превращает обычный default-temperature запрос в «детерминированный» без явного указания клиента.

## Context Optimizer

Режимы: `off`, `observe`, `safe`, `balanced`, `experimental`.

RC8 release contract для фактической трансформации остаётся консервативным:

- system/developer instructions не удаляются;
- tool schemas не переписываются;
- OpenAI `tool_calls` / `tool_call_id` и typed tool-use/tool-result blocks защищены как единый contract;
- код, diff и структурные маркеры защищены;
- safe compaction удаляет только доказанные дубликаты plain-text истории вне recent window;
- safe mode не добавляет собственный synthetic system/developer prompt;
- observe mode никогда не изменяет request.

Более агрессивная семантическая компрессия остаётся за пределами RC8 до появления обратимого preview/diff pipeline.

## models.dev reference sync

`models.dev` остаётся только справочным источником:

- данные маркируются `declared`;
- reference sync не может установить `ChatVerified`, `IdentityVerified`, `ToolCalling` или `AgentReady`;
- допускается только абсолютный публичный HTTPS URL;
- localhost/private/link-local/metadata-style literal targets и URL userinfo отклоняются;
- redirect target повторно валидируется, число redirect ограничено;
- last-known-good snapshot сохраняется в SQLite и восстанавливается после перезапуска процесса.

## Provider Packs

- Ed25519 verification;
- data-only manifests;
- schema/URL/auth validation;
- внешний исполняемый код запрещён;
- встроенные Yandex AI Studio и GigaChat adapters не переопределяются provider pack-ом.

## Release Engineering RC8

- версия `1.0.0-rc8`, Config schema 6, SQLite schema 4, Windows manifest `1.0.0.8`;
- `RELEASE.json` в release package содержит build timestamp, source commit и точный Go toolchain;
- генерируется `TOOLCHAIN_PROVENANCE.txt`;
- генерируется `SBOM.spdx.json` формата SPDX 2.3;
- `go.mod` не содержит внешних module dependencies;
- secret-like file scan остаётся fail-closed;
- `SHA256SUMS.txt` формируется после упаковки файлов;
- CI использует Go 1.27.x;
- gofmt является fail-closed gate;
- `govulncheck v1.7.0` является fail-closed gate;
- feature branches проверяются через Pull Request, `main` — при push;
- устаревшие validation runs на одной ветке настроены на cancel-in-progress.

## Windows acceptance

GitHub Actions run #160 на кодовом RC8 head `cd8dd3dc6d24c4259e291c963cf3a92d56462ae3` подтвердил:

- Windows Server 2025;
- Go `1.27.0 windows/amd64`;
- fail-closed formatting gate — PASS;
- `go test ./...` — PASS;
- `go test -race ./...` — PASS;
- `go vet ./...` — PASS;
- `govulncheck v1.7.0` — `No vulnerabilities found`;
- PowerShell 7 UI validation — PASS, 12 XAML views;
- Windows PowerShell `5.1.26100.33296` UI validation — PASS, 12 XAML views;
- DPAPI protect/unprotect round-trip — PASS;
- invalid DPAPI ciphertext rejection — PASS;
- Windows x64 release build — PASS;
- `dist/OmniBridge_1.0.0-rc8_Windows_x64.zip` создан успешно.

Upload artifact остаётся best-effort: GitHub вернул `Artifact storage quota has been hit`, поэтому у run #160 нет retained downloadable artifact, хотя сам release ZIP был успешно создан на runner.

## Сохранённый RC7 contract

- Discovery / Chat Verified / Agent Ready / Identity Verified остаются независимыми состояниями;
- auto routes требуют доказанного Chat probe;
- Agent export требует agent/identity evidence;
- physical identity после fallback отражается в response metadata;
- VS Code получает только проверенные Chat-модели;
- старые single aliases и model pools сохраняют совместимость;
- Router 3.1 и Protocol Engine 3.1 остаются базовыми runtime engines.

## Что ещё не является закрытым stable-1.0 acceptance

1. Интерактивный Desktop/Tray smoke в пользовательской Windows-сессии.
2. Live-provider acceptance с реальными credentials для критичных providers: models/chat/responses/stream/tools/identity/cache/fallback.
3. Signed updater trust chain и желательно Authenticode.
4. Anti-downgrade / monotonic SemVer gate с явным rollback override.
5. Полная provider conformance suite для native/preset support levels.
6. Восстановление GitHub Actions artifact quota и подтверждение retained RC8 artifact.

RC8 по автоматизированным Windows gates готов как release candidate. Оставшиеся пункты относятся к live acceptance и security gates стабильного `1.0.0`, а не к известной ошибке сборки RC8.
