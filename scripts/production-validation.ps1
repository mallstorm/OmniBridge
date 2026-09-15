param([string]$Root)
$ErrorActionPreference='Stop'
if(-not $Root){$Root=Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)}
$report=Join-Path $Root 'VALIDATION_REPORT.txt'
$lines=New-Object System.Collections.Generic.List[string]
function Step([string]$name,[scriptblock]$block){
    $start=Get-Date
    try{& $block;$lines.Add("PASS  $name  $([int]((Get-Date)-$start).TotalMilliseconds)ms")}
    catch{$lines.Add("FAIL  $name  $($_.Exception.Message)");throw}
}
Push-Location $Root
try{
    Step 'UI contract' {& (Join-Path $Root 'scripts\validate-ui.ps1') -Root $Root;if($LASTEXITCODE-ne 0){throw 'UI validation failed'}}
    Step 'go test ./...' {& go test ./...;if($LASTEXITCODE-ne 0){throw 'go test failed'}}
    Step 'go vet ./...' {& go vet ./...;if($LASTEXITCODE-ne 0){throw 'go vet failed'}}
    Step 'race: critical packages' {& go test -race ./internal/config ./internal/security ./internal/protocol ./internal/providers ./internal/accounts ./internal/server ./internal/stats ./internal/health ./internal/storage ./internal/resilience;if($LASTEXITCODE-ne 0){throw 'race tests failed'}}
    Step 'Windows x64 cross-vet' {
        $og=$env:GOOS;$oa=$env:GOARCH;$oc=$env:CGO_ENABLED
        try{$env:GOOS='windows';$env:GOARCH='amd64';$env:CGO_ENABLED='0';& go vet ./...;if($LASTEXITCODE-ne 0){throw 'Windows vet failed'}}finally{$env:GOOS=$og;$env:GOARCH=$oa;$env:CGO_ENABLED=$oc}
    }
    Step 'Windows x64 bridge/setup build' {
        $og=$env:GOOS;$oa=$env:GOARCH;$oc=$env:CGO_ENABLED;$tmp=Join-Path $env:TEMP ('omnibridge-validation-'+[guid]::NewGuid().ToString('N'));New-Item -ItemType Directory -Force -Path $tmp|Out-Null
    }
    $lines.Insert(0,"OmniBridge production validation $(Get-Date -Format o)")
    $lines|Set-Content -Encoding UTF8 $report
    Write-Host "Validation passed: $report" -ForegroundColor Green
}finally{Pop-Location}
