param(
    [string]$Root,
    [ValidateSet('install','repair','restore','update','uninstall','backup','verify')][string]$Mode='install',
    [string]$Package
)
$ErrorActionPreference='Stop'
Add-Type -AssemblyName PresentationFramework
Add-Type -AssemblyName System.Windows.Forms

if(-not $Root){$Root=Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)}
$Root=[IO.Path]::GetFullPath($Root)
$InstallRoot=Join-Path $env:LOCALAPPDATA 'OmniBridge'
$BackupRoot=Join-Path $env:LOCALAPPDATA 'OmniBridgeBackups'
New-Item -ItemType Directory -Force -Path $BackupRoot | Out-Null

function Show-Info([string]$text){[System.Windows.MessageBox]::Show($text,'OmniBridge',[System.Windows.MessageBoxButton]::OK,[System.Windows.MessageBoxImage]::Information)|Out-Null}
function Show-ErrorBox([string]$text){[System.Windows.MessageBox]::Show($text,'OmniBridge',[System.Windows.MessageBoxButton]::OK,[System.Windows.MessageBoxImage]::Error)|Out-Null}
function Stop-InstalledOmniBridge([string]$base){
    Get-Process -Name OmniBridge -ErrorAction SilentlyContinue | ForEach-Object {
        try{
            $path=$_.Path
            if($path -and ([IO.Path]::GetFullPath($path)).StartsWith(([IO.Path]::GetFullPath($base)),[StringComparison]::OrdinalIgnoreCase)){
                $_.CloseMainWindow()|Out-Null
                if(-not $_.WaitForExit(2500)){Stop-Process -Id $_.Id -Force -ErrorAction SilentlyContinue}
            }
        }catch{}
    }
}
function Copy-AppPayload([string]$from,[string]$to){
    New-Item -ItemType Directory -Force -Path $to|Out-Null
    foreach($name in @('OmniBridge.exe','OmniBridge.exe','README_RU.md','VERSION.txt','RELEASE.json','RELEASE_REPORT.md','IMPLEMENTATION_MATRIX.md','VALIDATION_REPORT.txt','SHA256SUMS.txt')){
        $src=Join-Path $from $name
        if(Test-Path $src){Copy-Item -LiteralPath $src -Destination (Join-Path $to $name) -Force}
    }
    foreach($dir in @('assets','scripts','ui','docs')){
        $src=Join-Path $from $dir
        if(Test-Path $src){
            $dst=Join-Path $to $dir
            if(Test-Path $dst){Remove-Item -LiteralPath $dst -Recurse -Force}
            Copy-Item -LiteralPath $src -Destination $dst -Recurse -Force
        }
    }
    foreach($file in @('install.cmd','repair.cmd','backup.cmd','restore.cmd','update.cmd','uninstall.cmd')){
        $src=Join-Path $from $file
        if(Test-Path $src){Copy-Item -LiteralPath $src -Destination (Join-Path $to $file) -Force}
    }
    foreach($dir in @('data','logs')){New-Item -ItemType Directory -Force -Path (Join-Path $to $dir)|Out-Null}
}
function Write-Sha256Manifest([string]$dir){
    $manifest=Join-Path $dir 'SHA256SUMS.txt'
    if(Test-Path $manifest){Remove-Item -LiteralPath $manifest -Force}
    Get-ChildItem -LiteralPath $dir -File -Recurse | ForEach-Object {
        $hash=(Get-FileHash -Algorithm SHA256 -LiteralPath $_.FullName).Hash.ToLower()
        $rel=$_.FullName.Substring($dir.Length+1).Replace('\','/')
        "$hash  $rel"
    } | Set-Content -Encoding UTF8 -LiteralPath $manifest
}

function New-AppSnapshot([string]$base,[string]$prefix){
    if(-not (Test-Path $base)){return $null}
    $stamp=Get-Date -Format 'yyyyMMdd-HHmmss'
    $zip=Join-Path $BackupRoot ("$prefix-$stamp.zip")
    $stage=Join-Path $env:TEMP ("OmniBridge-snapshot-"+[guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Force -Path $stage|Out-Null
    try{
        foreach($name in @('OmniBridge.exe','OmniBridge.exe','README_RU.md','VERSION.txt','RELEASE.json','RELEASE_REPORT.md','IMPLEMENTATION_MATRIX.md','VALIDATION_REPORT.txt','SHA256SUMS.txt','install.cmd','repair.cmd','backup.cmd','restore.cmd','update.cmd','uninstall.cmd')){
            $src=Join-Path $base $name;if(Test-Path $src){Copy-Item -LiteralPath $src -Destination (Join-Path $stage $name) -Force}
        }
        foreach($dir in @('assets','scripts','ui','docs')){
            $src=Join-Path $base $dir;if(Test-Path $src){Copy-Item -LiteralPath $src -Destination (Join-Path $stage $dir) -Recurse -Force}
        }
        if(-not (Test-Path (Join-Path $stage 'OmniBridge.exe'))){return $null}
        Write-Sha256Manifest $stage
        Compress-Archive -Path (Join-Path $stage '*') -DestinationPath $zip -CompressionLevel Optimal
        return $zip
    }finally{Remove-Item $stage -Recurse -Force -ErrorAction SilentlyContinue}
}
function New-DataBackup([string]$base){
    $data=Join-Path $base 'data'
    if(-not (Test-Path $data)){throw 'Папка data не найдена.'}
    $stamp=Get-Date -Format 'yyyyMMdd-HHmmss'
    $zip=Join-Path $BackupRoot ("OmniBridge-data-$stamp.zip")
    $stage=Join-Path $env:TEMP ("OmniBridge-data-"+[guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Force -Path $stage|Out-Null
    try{
        $copied=0
        foreach($name in @('omnibridge.db','omnibridge.db-wal','omnibridge.db-shm','config.json','model-registry.json','usage-stats.json','connection-health.json')){
            $src=Join-Path $data $name
            if(Test-Path $src){Copy-Item -LiteralPath $src -Destination (Join-Path $stage $name) -Force;$copied++}
        }
        # secrets.json intentionally excluded: DPAPI data must not silently travel between Windows accounts.
        if($copied -eq 0){'Persistent OmniBridge data was empty when this backup was created.'|Set-Content -Encoding UTF8 (Join-Path $stage 'EMPTY_BACKUP.txt')}
        Write-Sha256Manifest $stage
        Compress-Archive -Path (Join-Path $stage '*') -DestinationPath $zip -CompressionLevel Optimal
        return $zip
    }finally{Remove-Item $stage -Recurse -Force -ErrorAction SilentlyContinue}
}
function Try-NewDataBackup([string]$base){
    if(-not (Test-Path (Join-Path $base 'data') -PathType Container)){return $null}
    return New-DataBackup $base
}
function Restore-DataRollback([string]$zip,[string]$base){
    if(-not $zip){return}
    $src=Resolve-DataBackup $zip
    Copy-DataBackupPayload $src $base
}

function Assert-SafeRelativePath([string]$rel){
    if([string]::IsNullOrWhiteSpace($rel)){throw 'Пустой путь в SHA256SUMS.'}
    $rel=$rel.Replace('/','\')
    if([IO.Path]::IsPathRooted($rel)){throw "Абсолютный путь запрещён в SHA256SUMS: $rel"}
    foreach($part in $rel.Split('\')){if($part -eq '..'){throw "Выход из каталога запрещён в SHA256SUMS: $rel"}}
    return $rel
}
function Verify-Sha256Manifest([string]$dir,[bool]$required=$false){
    $sum=Join-Path $dir 'SHA256SUMS.txt'
    if(-not (Test-Path $sum)){
        if($required){throw 'Пакет не содержит обязательный SHA256SUMS.txt'}
        return $false
    }
    $checked=0
    foreach($line in Get-Content -LiteralPath $sum){
        $line=$line.Trim();if(-not $line -or $line.StartsWith('#')){continue}
        if($line -notmatch '^([0-9a-fA-F]{64})\s+(.+)$'){throw "Некорректная строка SHA256SUMS: $line"}
        $expected=$Matches[1].ToLower();$rel=Assert-SafeRelativePath $Matches[2].Trim()
        if($rel -ieq 'SHA256SUMS.txt'){continue}
        $file=Join-Path $dir $rel
        $full=[IO.Path]::GetFullPath($file);$rootFull=([IO.Path]::GetFullPath($dir)).TrimEnd('\')+'\'
        if(-not $full.StartsWith($rootFull,[StringComparison]::OrdinalIgnoreCase)){throw "Путь выходит из пакета: $rel"}
        if(-not (Test-Path $full -PathType Leaf)){throw "Файл из SHA256SUMS отсутствует: $rel"}
        $actual=(Get-FileHash -Algorithm SHA256 -LiteralPath $full).Hash.ToLower()
        if($actual -ne $expected){throw "SHA256 не совпадает: $rel"}
        $checked++
    }
    if($checked -eq 0){throw 'SHA256SUMS.txt не содержит проверяемых файлов.'}
    return $true
}
function Assert-SafeZip([string]$zip){
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $archive=[IO.Compression.ZipFile]::OpenRead($zip)
    try{
        foreach($entry in $archive.Entries){
            $name=[string]$entry.FullName
            if([string]::IsNullOrWhiteSpace($name)){continue}
            $normalized=$name.Replace('/','\')
            if([IO.Path]::IsPathRooted($normalized)){throw "ZIP содержит абсолютный путь: $name"}
            foreach($part in $normalized.Split('\')){if($part -eq '..'){throw "ZIP содержит небезопасный путь: $name"}}
        }
    }finally{$archive.Dispose()}
}
function Verify-Package([string]$dir,[bool]$requireRC=$true){
    $exe=Join-Path $dir 'OmniBridge.exe'
    if(-not (Test-Path $exe -PathType Leaf)){throw 'Пакет не содержит OmniBridge.exe'}
    $verFile=Join-Path $dir 'VERSION.txt'
    $releaseFile=Join-Path $dir 'RELEASE.json'
    if(-not (Test-Path $verFile -PathType Leaf)){throw 'Пакет не содержит VERSION.txt'}
    $version=(Get-Content -Raw -LiteralPath $verFile).Trim()
    if(Test-Path $releaseFile -PathType Leaf){
        $meta=Get-Content -Raw -LiteralPath $releaseFile|ConvertFrom-Json
        if([string]$meta.version -ne $version){throw "RELEASE.json не совпадает с VERSION.txt: $($meta.version) != $version"}
        if($requireRC -and [string]$meta.channel -ne 'rc'){throw "Ожидался RC-пакет, channel=$($meta.channel)"}
    }elseif($requireRC){throw 'RC-пакет не содержит RELEASE.json'}
    $null=Verify-Sha256Manifest $dir $true
    if($requireRC){
        foreach($forbidden in @('secrets.json','.env','credentials.json')){
            $hit=Get-ChildItem -LiteralPath $dir -Recurse -File -ErrorAction SilentlyContinue|Where-Object{$_.Name -ieq $forbidden}|Select-Object -First 1
            if($hit){throw "Релизный пакет содержит запрещённый секретный файл: $($hit.FullName)"}
        }
    }
}
function Resolve-PackageDirectory([string]$zip,[bool]$requireRC=$true){
    if(-not $zip){
        $dlg=New-Object System.Windows.Forms.OpenFileDialog
        $dlg.Filter='OmniBridge package (*.zip)|*.zip|All files (*.*)|*.*'
        if($dlg.ShowDialog()-ne[System.Windows.Forms.DialogResult]::OK){throw 'Операция отменена.'}
        $zip=$dlg.FileName
    }
    if(Test-Path $zip -PathType Container){Verify-Package $zip $requireRC;return $zip}
    if(-not (Test-Path $zip)){throw "Пакет не найден: $zip"}
    $stage=Join-Path $env:TEMP ("OmniBridge-package-"+[guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Force -Path $stage|Out-Null
    Assert-SafeZip $zip
    Expand-Archive -LiteralPath $zip -DestinationPath $stage -Force
    $candidate=$stage
    $children=Get-ChildItem $stage -Directory
    if(-not (Test-Path (Join-Path $candidate 'OmniBridge.exe')) -and $children.Count -eq 1){$candidate=$children[0].FullName}
    Verify-Package $candidate $requireRC
    return $candidate
}
function Resolve-DataBackup([string]$zip){
    if(-not $zip){
        $dlg=New-Object System.Windows.Forms.OpenFileDialog
        $dlg.Filter='OmniBridge backup (*.zip)|*.zip|All files (*.*)|*.*'
        if($dlg.ShowDialog()-ne[System.Windows.Forms.DialogResult]::OK){throw 'Операция отменена.'}
        $zip=$dlg.FileName
    }
    if(Test-Path $zip -PathType Container){return $zip}
    if(-not (Test-Path $zip)){throw "Backup не найден: $zip"}
    $stage=Join-Path $env:TEMP ("OmniBridge-restore-"+[guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Force -Path $stage|Out-Null
    Assert-SafeZip $zip
    Expand-Archive -LiteralPath $zip -DestinationPath $stage -Force
    $candidate=$stage
    $children=Get-ChildItem $stage -Directory
    if(-not (Test-Path (Join-Path $candidate 'config.json')) -and $children.Count -eq 1){$candidate=$children[0].FullName}
    $known=@('omnibridge.db','config.json','model-registry.json','usage-stats.json','connection-health.json')|Where-Object{Test-Path (Join-Path $candidate $_)}
    if($known.Count -eq 0){throw 'Backup не содержит данных OmniBridge.'}
    # New backups are signed with SHA256SUMS. Legacy backups remain readable for compatibility.
    $null=Verify-Sha256Manifest $candidate $false
    if(Test-Path (Join-Path $candidate 'config.json')){Get-Content -Raw (Join-Path $candidate 'config.json')|ConvertFrom-Json|Out-Null}
    return $candidate
}
function Copy-DataBackupPayload([string]$from,[string]$base){
    $data=Join-Path $base 'data';New-Item -ItemType Directory -Force -Path $data|Out-Null
    $hasDb=Test-Path (Join-Path $from 'omnibridge.db')
    # A legacy JSON-only backup must force a clean JSON -> SQLite import on next start.
    if(-not $hasDb){
        foreach($name in @('omnibridge.db','omnibridge.db-wal','omnibridge.db-shm')){Remove-Item -LiteralPath (Join-Path $data $name) -Force -ErrorAction SilentlyContinue}
    }else{
        # Never combine a restored main database with stale WAL/SHM from the previous install.
        foreach($name in @('omnibridge.db','omnibridge.db-wal','omnibridge.db-shm')){Remove-Item -LiteralPath (Join-Path $data $name) -Force -ErrorAction SilentlyContinue}
    }
    foreach($name in @('omnibridge.db','omnibridge.db-wal','omnibridge.db-shm','config.json','model-registry.json','usage-stats.json','connection-health.json')){
        $f=Join-Path $from $name;if(Test-Path $f){Copy-Item -LiteralPath $f -Destination (Join-Path $data $name) -Force}
    }
}
function New-Shortcut([string]$target){
    $menu=Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs'
    $link=Join-Path $menu 'OmniBridge.lnk'
    $wsh=New-Object -ComObject WScript.Shell
    $sc=$wsh.CreateShortcut($link);$sc.TargetPath=$target;$sc.WorkingDirectory=Split-Path $target -Parent;$sc.IconLocation=$target;$sc.Save()
}
function Test-NewCore([string]$base){
    $exe=Join-Path $base 'OmniBridge.exe'
    $verFile=Join-Path $base 'VERSION.txt'
    $expected=if(Test-Path $verFile){(Get-Content -Raw -LiteralPath $verFile).Trim()}else{''}
    $p=Start-Process -FilePath $exe -WorkingDirectory $base -PassThru
    $ok=$false
    try{
        for($i=0;$i -lt 60;$i++){
            Start-Sleep -Milliseconds 250
            if($p.HasExited){break}
            try{
                $h=Invoke-RestMethod -Uri 'http://127.0.0.1:4142/health' -TimeoutSec 1
                if($h.ok -and (-not $expected -or [string]$h.version -eq $expected)){
                    # /readyz is stricter than /health: DB quick_check + package/version/runtime integrity.
                    $ready=Invoke-RestMethod -Uri 'http://127.0.0.1:4142/readyz' -TimeoutSec 8
                    if($ready.ok -and (-not $expected -or [string]$ready.version -eq $expected) -and -not $p.HasExited){$ok=$true;break}
                }
            }catch{}
        }
    }finally{
        if(-not $ok -and -not $p.HasExited){Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue}
    }
    return $ok
}

try{
    switch($Mode){
        'verify' {
            Verify-Package $Root
            Show-Info "Пакет OmniBridge проверен: VERSION/RELEASE metadata/SHA-256/secret hygiene — OK.`n$Root"
        }
        'backup' {
            $base=if(Test-Path $InstallRoot){$InstallRoot}else{$Root}
            $exe=Join-Path $base 'OmniBridge.exe'
            $wasRunning=$false
            Get-Process -Name OmniBridge -ErrorAction SilentlyContinue | ForEach-Object {
                try{if($_.Path -and ([IO.Path]::GetFullPath($_.Path)).StartsWith(([IO.Path]::GetFullPath($base)),[StringComparison]::OrdinalIgnoreCase)){$wasRunning=$true}}catch{}
            }
            # SQLite backup is made with Core stopped so the WAL is fully checkpointed/closed.
            if($wasRunning){Stop-InstalledOmniBridge $base}
            try{$zip=New-DataBackup $base}
            finally{if($wasRunning -and (Test-Path $exe)){Start-Process -FilePath $exe -WorkingDirectory $base|Out-Null}}
            Show-Info "Резервная копия создана:`n$zip"
        }
        'install' {
            Verify-Package $Root
            Stop-InstalledOmniBridge $InstallRoot
            $rollback=New-AppSnapshot $InstallRoot 'preinstall'
            $dataRollback=Try-NewDataBackup $InstallRoot
            try{
                Copy-AppPayload $Root $InstallRoot
                New-Shortcut (Join-Path $InstallRoot 'OmniBridge.exe')
                @{installed_at=(Get-Date).ToString('o');source=$Root;version=(Get-Content -Raw (Join-Path $Root 'VERSION.txt')).Trim()}|ConvertTo-Json|Set-Content -Encoding UTF8 (Join-Path $InstallRoot 'installed.json')
                if(-not (Test-NewCore $InstallRoot)){throw 'Установленная версия не прошла health-check.'}
            }catch{
                Stop-InstalledOmniBridge $InstallRoot
                if($rollback){$stage=Resolve-PackageDirectory $rollback $false;Copy-AppPayload $stage $InstallRoot}
                if($dataRollback){Restore-DataRollback $dataRollback $InstallRoot}
                if($rollback){$null=Test-NewCore $InstallRoot}
                throw
            }
            Show-Info "OmniBridge установлен и прошёл release readiness:`n$InstallRoot`n`nRollback app: $rollback`nRollback data: $dataRollback"
        }
        'repair' {
            Verify-Package $Root
            Stop-InstalledOmniBridge $InstallRoot
            $rollback=New-AppSnapshot $InstallRoot 'prerepair'
            $dataRollback=Try-NewDataBackup $InstallRoot
            try{
                Copy-AppPayload $Root $InstallRoot
                New-Shortcut (Join-Path $InstallRoot 'OmniBridge.exe')
                if(-not (Test-NewCore $InstallRoot)){throw 'Восстановленная версия не прошла release readiness.'}
            }catch{
                Stop-InstalledOmniBridge $InstallRoot
                if($rollback){$stage=Resolve-PackageDirectory $rollback $false;Copy-AppPayload $stage $InstallRoot}
                if($dataRollback){Restore-DataRollback $dataRollback $InstallRoot}
                if($rollback){$null=Test-NewCore $InstallRoot}
                throw
            }
            Show-Info "Установка OmniBridge восстановлена и прошла release readiness.`nRollback app: $rollback`nRollback data: $dataRollback"
        }
        'restore' {
            $base=if(Test-Path $InstallRoot){$InstallRoot}else{$Root}
            Stop-InstalledOmniBridge $base
            $before=New-DataBackup $base
            try{
                $src=Resolve-DataBackup $Package
                Copy-DataBackupPayload $src $base
                if(Test-Path (Join-Path $base 'OmniBridge.exe')){
                    if(-not (Test-NewCore $base)){throw 'Восстановленные данные не прошли health-check.'}
                }
            }catch{
                Stop-InstalledOmniBridge $base
                $rollbackData=Resolve-DataBackup $before
                Copy-DataBackupPayload $rollbackData $base
                if(Test-Path (Join-Path $base 'OmniBridge.exe')){$null=Test-NewCore $base}
                throw
            }
            Show-Info "Данные восстановлены и прошли health-check. Автоматический backup до восстановления:`n$before`n`nsecrets.json не восстанавливался."
        }
        'update' {
            $base=if(Test-Path $InstallRoot){$InstallRoot}else{$Root}
            $src=Resolve-PackageDirectory $Package
            Stop-InstalledOmniBridge $base
            $rollback=New-AppSnapshot $base 'preupdate'
            $dataRollback=Try-NewDataBackup $base
            try{
                Copy-AppPayload $src $base
                if(-not (Test-NewCore $base)){throw 'Новая версия не прошла release readiness после обновления.'}
            }catch{
                Stop-InstalledOmniBridge $base
                if($rollback){$stage=Resolve-PackageDirectory $rollback $false;Copy-AppPayload $stage $base}
                if($dataRollback){Restore-DataRollback $dataRollback $base}
                if($rollback){$null=Test-NewCore $base}
                throw
            }
            Show-Info "Обновление установлено и прошло release readiness.`nRollback app snapshot:`n$rollback`n`nRollback data snapshot:`n$dataRollback"
        }
        'uninstall' {
            if(-not (Test-Path $InstallRoot)){Show-Info 'Установленная копия OmniBridge не найдена.';break}
            Stop-InstalledOmniBridge $InstallRoot
            $dataBackup=New-DataBackup $InstallRoot
            Remove-Item (Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs\OmniBridge.lnk') -Force -ErrorAction SilentlyContinue
            Remove-Item $InstallRoot -Recurse -Force
            Show-Info "OmniBridge удалён. Данные сохранены в:`n$dataBackup"
        }
    }
}catch{
    Show-ErrorBox $_.Exception.Message
    throw
}
