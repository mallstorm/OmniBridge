# OmniBridge Portable Build Environment — русское руководство

## Назначение

Переносная Windows x64 среда для разработки, проверки, компиляции и упаковки OmniBridge без обязательного GitHub Actions.

Основной запуск:

```cmd
START.cmd
```

Откроется графический интерфейс на русском языке. Долгие операции выполняются отдельными backend-скриптами, поэтому GUI не меняет семантику сборки и не зависает на длительных тестах.

## Рекомендуемый первый запуск

1. **Скачать и развернуть инструменты**.
2. **Получить исходники OmniBridge**.
3. **Проверить среду (Doctor)**.
4. Выполнить **FAST-проверку**.
5. Выполнить **FULL + govulncheck**.
6. Выполнить **LOCAL RELEASE**.

## Что делает интерфейс

### 1. Развернуть инструменты

Bootstrap скачивает закреплённые версии Go, portable PowerShell, LLVM-MinGW, rsrc и govulncheck. Архивы принимаются только после SHA256 verification. Системный PATH Windows не изменяется.

### 2. Получить исходники

Рекомендуемый режим — private GitHub snapshot через REST API. Git не обязателен.

По умолчанию:

```text
Repository: mallstorm/OmniBridge
Ref: codex/rc8-team-routing
```

PAT должен иметь минимальный read-доступ к репозиторию. GUI не сохраняет его в конфиг, source metadata или отчёты.

Дополнительно доступны:

- импорт локальной папки;
- импорт ZIP;
- восстановление аварийного RC7 snapshot `436eec3`.

### 3. Doctor

Проверяет:

- Windows x64;
- Go;
- portable PowerShell;
- rsrc;
- govulncheck;
- Windows PowerShell 5.1;
- WPF/PresentationFramework;
- LLVM-MinGW для race detector;
- наличие `source/OmniBridge/go.mod`.

### 4. FAST

Быстрый цикл разработки:

```text
gofmt fail-closed
go test ./...
go vet ./...
```

Запускайте после небольших изменений.

### 5. FULL

Локальный аналог CI:

```text
gofmt
go test ./...
go vet ./...
go test -race ./...
govulncheck ./...
Windows resource regeneration
PowerShell 7 + XAML validation
Windows PowerShell 5.1
DPAPI round-trip
Windows x64 compile smoke
```

FULL офлайн пропускает только свежий сетевой vulnerability database scan.

### 6. LOCAL RELEASE

Полный release pipeline создаёт:

- `OmniBridge.exe`;
- `OmniBridgeSetup.exe`;
- Windows x64 release ZIP;
- SHA256;
- `SBOM.spdx.json`;
- `TOOLCHAIN_PROVENANCE.txt`;
- snapshot исходников.

## DEV Shell

Открывает отдельный процесс с переносными:

```text
GOROOT
GOPATH
GOBIN
GOCACHE
GOMODCACHE
GOTOOLCHAIN=local
GOTELEMETRY=off
CC
CXX
PATH
```

После закрытия shell настройки исчезают вместе с процессом и не загрязняют Windows.

## OFFLINE ZIP

После bootstrap и получения исходников можно упаковать среду вместе с toolchain и source snapshot. Такой архив переносится на другой Windows ПК и позволяет работать без GitHub Actions.

Свежий `govulncheck` обычно остаётся единственным gate, которому нужен интернет для актуальной базы уязвимостей.

## Структура каталогов

```text
tools/       переносной toolchain
downloads/   проверенные скачанные архивы
cache/       Go build/module cache
source/      рабочий snapshot OmniBridge
dist/        EXE и release ZIP
reports/     validation reports
logs/        журналы
snapshots/   контрольные снимки source
fallback/    аварийный RC7 snapshot
docs/        русская документация
```

## Безопасность

Не храните в переносной среде:

- GitHub PAT;
- API keys AI-провайдеров;
- `.env`;
- браузерные cookies/session exports;
- `.pfx/.p12/.key`;
- `credentials.json`.

Для GitHub source sync используйте отдельный fine-grained PAT: только нужный репозиторий и `Contents: Read`.

## Типичные проблемы

### GUI не запускается

Запустите резервное консольное меню или напрямую backend-скрипты. Проверьте Windows PowerShell 5.1 и WPF:

```powershell
Add-Type -AssemblyName PresentationFramework
```

### Bootstrap сообщает SHA256 mismatch

Не обходите проверку. Удалите повреждённый архив из `downloads` и повторите bootstrap.

### GitHub отвечает 401/403

Проверьте PAT, срок действия и доступ к private repository.

### Race detector не запускается

Doctor должен видеть LLVM-MinGW C compiler. Повторите полный bootstrap.

### `govulncheck` не работает без сети

Используйте FULL/RELEASE офлайн, а после восстановления сети повторите online security gate.

### DPAPI acceptance падает

DPAPI проверяется только на настоящей Windows-среде текущего пользователя. Wine/урезанный контейнер для этого gate не подходит.

## Что считать готовой средой

Для разработки:

- Doctor PASS;
- FAST PASS.

Перед завершением крупного этапа:

- FULL PASS;
- race PASS;
- PS5.1 PASS;
- DPAPI PASS;
- Windows compile PASS.

Перед релизом:

- FULL + свежий govulncheck PASS;
- LOCAL RELEASE PASS;
- release ZIP и SHA256 созданы;
- ручной Windows smoke WPF/Tray выполнен.

## Рабочий цикл Team Routing

```text
изменение кода
    ↓
FAST
    ↓
следующий подэтап
    ↓
FULL офлайн
    ↓
FULL online при наличии сети
    ↓
LOCAL RELEASE
    ↓
контрольный snapshot
```

GitHub Actions после этого становится дополнительной внешней проверкой, а не блокирующим условием разработки.
