# OmniBridge — обслуживание и обновление

Начиная с `1.0.0-rc8`, `OmniBridge.exe` является и основным приложением, и maintenance utility. Отдельный `OmniBridgeSetup.exe` больше не нужен.

Поддерживаемые режимы:

```text
OmniBridge.exe --install
OmniBridge.exe --repair
OmniBridge.exe --backup
OmniBridge.exe --restore
OmniBridge.exe --update <archive.zip>
OmniBridge.exe --uninstall
OmniBridge.exe --verify
```

Командные `.cmd`-обёртки вызывают эти режимы напрямую. Update/rollback по-прежнему выполняет проверку manifest/SHA256, snapshot программных файлов, backup пользовательских данных и health/readiness gate.

## Артефакты релиза

`scripts/build-release.ps1` создаёт раздельно:

- `OmniBridge_<version>_Windows_x64.zip` — runtime без пользовательского состояния;
- `OmniBridge_<version>_Source.zip` — исходники без EXE, `data/`, logs, SQLite/DPAPI secrets.

Это исключает случайную публикацию рабочего `data/secrets.json` или `omnibridge.db` вместе с исходниками.
