param([string]$Root)
$ErrorActionPreference='Stop'
if(-not $Root){$Root=Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)}
$version=(Get-Content -Raw -LiteralPath (Join-Path $Root 'VERSION.txt')).Trim()
$dist=Join-Path $Root 'dist'
$package=Join-Path $dist ("OmniBridge_{0}_Windows_x64" -f $version)
$archive=$package+'.zip'

& (Join-Path $Root 'scripts\validate-ui.ps1') -Root $Root
Push-Location $Root
try{
    & go test ./...
    if($LASTEXITCODE -ne 0){throw 'go test failed'}
    & go vet ./...
    if($LASTEXITCODE -ne 0){throw 'go vet failed'}

    if(Test-Path $package){Remove-Item -LiteralPath $package -Recurse -Force}
    if(Test-Path $archive){Remove-Item -LiteralPath $archive -Force}
    New-Item -ItemType Directory -Force -Path $package|Out-Null
    foreach($dir in @('assets','scripts','ui','data','logs','docs')){New-Item -ItemType Directory -Force -Path (Join-Path $package $dir)|Out-Null}

    $oldGOOS=$env:GOOS;$oldGOARCH=$env:GOARCH;$oldCGO=$env:CGO_ENABLED
    $env:GOOS='windows';$env:GOARCH='amd64';$env:CGO_ENABLED='0'
    try{
        & go build -buildvcs=false -trimpath -ldflags '-s -w -H windowsgui' -o (Join-Path $package 'OmniBridge.exe') ./cmd/bridge
        if($LASTEXITCODE -ne 0){throw 'Windows bridge build failed'}
        & go build -buildvcs=false -trimpath -ldflags '-s -w -H windowsgui' -o (Join-Path $package 'OmniBridgeSetup.exe') ./cmd/setup
        if($LASTEXITCODE -ne 0){throw 'Windows setup build failed'}
    }finally{$env:GOOS=$oldGOOS;$env:GOARCH=$oldGOARCH;$env:CGO_ENABLED=$oldCGO}

    Copy-Item (Join-Path $Root 'assets\*') (Join-Path $package 'assets') -Recurse -Force
    Copy-Item (Join-Path $Root 'scripts\*') (Join-Path $package 'scripts') -Recurse -Force
    Copy-Item (Join-Path $Root 'ui\*') (Join-Path $package 'ui') -Recurse -Force
    if(Test-Path (Join-Path $Root 'docs')){Copy-Item (Join-Path $Root 'docs\*') (Join-Path $package 'docs') -Recurse -Force}
    foreach($file in @('README_RU.md','VERSION.txt','RELEASE.json','RELEASE_REPORT.md','IMPLEMENTATION_MATRIX.md','VALIDATION_REPORT.txt','install.cmd','repair.cmd','backup.cmd','restore.cmd','update.cmd','uninstall.cmd')){if(Test-Path (Join-Path $Root $file)){Copy-Item (Join-Path $Root $file) $package -Force}}
    # Release-candidate metadata is regenerated at build time so the package is self-describing.
    $releaseMeta=[ordered]@{
        product='OmniBridge';version=$version;channel='rc';release_train='1.0';
        config_schema=5;db_schema=3;protocol_engine='3.1';router_runtime='3.1';
        minimum_supported_upgrade='0.3.2-rc6';built_at_utc=(Get-Date).ToUniversalTime().ToString('o')
    }
    $releaseMeta|ConvertTo-Json -Depth 4|Set-Content -Encoding UTF8 -LiteralPath (Join-Path $package 'RELEASE.json')

    # Fail closed if a release package accidentally contains credential material.
    $forbidden=Get-ChildItem -LiteralPath $package -File -Recurse|Where-Object{
        $_.Name -ieq 'secrets.json' -or $_.Name -ieq '.env' -or $_.Name -ieq 'credentials.json' -or
        $_.Extension -ieq '.pfx' -or $_.Extension -ieq '.p12' -or $_.Extension -ieq '.key'
    }
    if($forbidden){throw ('Secret-like files found in release package: '+(($forbidden|ForEach-Object{$_.FullName}) -join ', '))}

    Get-ChildItem $package -File -Recurse|Get-FileHash -Algorithm SHA256|ForEach-Object{"$($_.Hash.ToLower())  $($_.Path.Substring($package.Length+1).Replace('\','/'))"}|Set-Content -Encoding UTF8 (Join-Path $package 'SHA256SUMS.txt')
    Compress-Archive -Path $package -DestinationPath $archive -CompressionLevel Optimal
    Write-Host "Release ready: $archive" -ForegroundColor Green
}finally{Pop-Location}
