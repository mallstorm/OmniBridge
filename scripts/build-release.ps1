param([string]$Root)
$ErrorActionPreference='Stop'
if(-not $Root){$Root=Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)}
$Root=[IO.Path]::GetFullPath($Root)
$version=(Get-Content -Raw -LiteralPath (Join-Path $Root 'VERSION.txt')).Trim()
$dist=Join-Path $Root 'dist'
$runtimeName=("OmniBridge_{0}_Windows_x64" -f $version)
$sourceName=("OmniBridge_{0}_Source" -f $version)
$runtimeDir=Join-Path $dist $runtimeName
$sourceDir=Join-Path $dist $sourceName
$runtimeZip=$runtimeDir+'.zip'
$sourceZip=$sourceDir+'.zip'

& (Join-Path $Root 'scripts\validate-ui.ps1') -Root $Root
Push-Location $Root
try{
    & go test ./...
    if($LASTEXITCODE -ne 0){throw 'go test failed'}
    & go vet ./...
    if($LASTEXITCODE -ne 0){throw 'go vet failed'}
    New-Item -ItemType Directory -Force -Path $dist|Out-Null
    foreach($path in @($runtimeDir,$sourceDir,$runtimeZip,$sourceZip)){if(Test-Path $path){Remove-Item -LiteralPath $path -Recurse -Force}}
    New-Item -ItemType Directory -Force -Path $runtimeDir|Out-Null
    New-Item -ItemType Directory -Force -Path $sourceDir|Out-Null
    $oldGOOS=$env:GOOS;$oldGOARCH=$env:GOARCH;$oldCGO=$env:CGO_ENABLED
    $env:GOOS='windows';$env:GOARCH='amd64';$env:CGO_ENABLED='0'
    try{
        & go build -buildvcs=false -trimpath -ldflags '-s -w -H windowsgui' -o (Join-Path $runtimeDir 'OmniBridge.exe') ./cmd/bridge
        if($LASTEXITCODE -ne 0){throw 'Windows OmniBridge build failed'}
    }finally{$env:GOOS=$oldGOOS;$env:GOARCH=$oldGOARCH;$env:CGO_ENABLED=$oldCGO}
    foreach($dir in @('assets','scripts','ui','docs')){$src=Join-Path $Root $dir;if(Test-Path $src){Copy-Item -LiteralPath $src -Destination (Join-Path $runtimeDir $dir) -Recurse -Force}}
    foreach($dir in @('data','logs')){New-Item -ItemType Directory -Force -Path (Join-Path $runtimeDir $dir)|Out-Null}
    foreach($file in @('README_RU.md','VERSION.txt','RELEASE.json','RELEASE_REPORT.md','IMPLEMENTATION_MATRIX.md','VALIDATION_REPORT.txt','install.cmd','repair.cmd','backup.cmd','restore.cmd','update.cmd','uninstall.cmd')){$src=Join-Path $Root $file;if(Test-Path $src){Copy-Item -LiteralPath $src -Destination $runtimeDir -Force}}
    $releaseMeta=[ordered]@{product='OmniBridge';version=$version;channel='rc';release_train='1.0';config_schema=5;db_schema=3;protocol_engine='3.1';router_runtime='3.1';minimum_supported_upgrade='0.3.2-rc6';built_at_utc=(Get-Date).ToUniversalTime().ToString('o')}
    $releaseMeta|ConvertTo-Json -Depth 4|Set-Content -Encoding UTF8 -LiteralPath (Join-Path $runtimeDir 'RELEASE.json')
    foreach($dir in @('.github','assets','cmd','internal','scripts','ui','docs')){$src=Join-Path $Root $dir;if(Test-Path $src){Copy-Item -LiteralPath $src -Destination (Join-Path $sourceDir $dir) -Recurse -Force}}
    foreach($file in @('go.mod','README_RU.md','VERSION.txt','RELEASE.json','RELEASE_REPORT.md','IMPLEMENTATION_MATRIX.md','VALIDATION_REPORT.txt','REDESIGN_SPEC.md','UI3_DESIGN_SYSTEM.md','UI_IMPLEMENTATION.md','build-release.cmd','install.cmd','repair.cmd','backup.cmd','restore.cmd','update.cmd','uninstall.cmd','start.cmd','stop.cmd','desktop.cmd','desktop-debug.cmd','sync-vscode.cmd','model-doctor.cmd','account-doctor.cmd','accounts.cmd','install-gigachat-cert.cmd')){$src=Join-Path $Root $file;if(Test-Path $src){Copy-Item -LiteralPath $src -Destination $sourceDir -Force}}
    foreach($legacy in @('cmd\accounts','cmd\desktop','cmd\tray','cmd\setup')){$p=Join-Path $sourceDir $legacy;if(Test-Path $p){Remove-Item -LiteralPath $p -Recurse -Force}}
    foreach($pkg in @($runtimeDir,$sourceDir)){
        $forbidden=Get-ChildItem -LiteralPath $pkg -File -Recurse|Where-Object{$_.Name -ieq 'secrets.json' -or $_.Name -ieq 'omnibridge.db' -or $_.Name -ieq '.env' -or $_.Name -ieq 'credentials.json' -or $_.Extension -ieq '.pfx' -or $_.Extension -ieq '.p12' -or $_.Extension -ieq '.key'}
        if($forbidden){throw ('Secret/runtime files found in package: '+(($forbidden|ForEach-Object{$_.FullName}) -join ', '))}
    }
    Get-ChildItem $runtimeDir -File -Recurse|Get-FileHash -Algorithm SHA256|ForEach-Object{"$($_.Hash.ToLower())  $($_.Path.Substring($runtimeDir.Length+1).Replace('\','/'))"}|Set-Content -Encoding UTF8 (Join-Path $runtimeDir 'SHA256SUMS.txt')
    Compress-Archive -Path (Join-Path $runtimeDir '*') -DestinationPath $runtimeZip -CompressionLevel Optimal
    Compress-Archive -Path (Join-Path $sourceDir '*') -DestinationPath $sourceZip -CompressionLevel Optimal
    Write-Host "Runtime release: $runtimeZip" -ForegroundColor Green
    Write-Host "Source release:  $sourceZip" -ForegroundColor Green
}finally{Pop-Location}
