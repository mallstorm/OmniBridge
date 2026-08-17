param(
    [string]$Root,
    [string]$Account,
    [switch]$Probe,
    [switch]$Deep
)
$ErrorActionPreference='Stop'
if(-not $Root){$Root=Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)}
. (Join-Path $Root 'scripts\security.ps1')
$configPath=Join-Path $Root 'data\config.json'
$bridgeExe=Join-Path $Root 'OmniBridge.exe'
function Ensure-Bridge {
    try{$null=Invoke-RestMethod 'http://127.0.0.1:4142/health' -TimeoutSec 1;return}catch{}
    Start-Process $bridgeExe -WorkingDirectory $Root -WindowStyle Hidden
    for($i=0;$i -lt 30;$i++){Start-Sleep -Milliseconds 150;try{$null=Invoke-RestMethod 'http://127.0.0.1:4142/health' -TimeoutSec 1;return}catch{}}
    throw 'OmniBridge не запустился.'
}
function Config {Get-BridgeConfig $Root}
function Admin([string]$Method,[string]$Path,$Body=$null){
    $c=Config;$args=@{Method=$Method;Uri=('http://127.0.0.1:4142'+$Path);Headers=@{Authorization="Bearer $($c.admin_api_key)"};TimeoutSec=60}
    if($null-ne$Body){$args.ContentType='application/json';$args.Body=($Body|ConvertTo-Json -Depth 20)}
    Invoke-RestMethod @args
}
Ensure-Bridge
$accounts=@(Admin GET '/bridge/accounts')
if($Account){$accounts=@($accounts|Where-Object{$_.id -eq $Account -or $_.name -eq $Account})}
$report=[ordered]@{generated_at=(Get-Date).ToString('o');version=(Invoke-RestMethod 'http://127.0.0.1:4142/health').version;accounts=@()}
foreach($a in $accounts){
    $row=[ordered]@{id=$a.id;name=$a.name;kind=$a.kind;configured_status=$a.status;info=$null;usage=$null;refresh='not-run';models=@();probe=$null}
    try{$row.info=Admin GET ("/bridge/accounts/$($a.id)/info")}catch{$row.info=[ordered]@{status='error';error=$_.Exception.Message}}
    try{$row.usage=Admin GET ("/bridge/accounts/$($a.id)/usage")}catch{$row.usage=[ordered]@{status='error';error=$_.Exception.Message}}
    try{$null=Admin POST ("/bridge/accounts/$($a.id)/refresh") @{};$row.refresh='ok'}catch{$row.refresh='error: '+$_.Exception.Message}
    try{
        $reg=Admin GET '/bridge/models'
        $row.models=@($reg.models|Where-Object{$_.source -eq 'account' -and $_.provider_id -eq $a.id}|Select-Object id,name,api_type,tool_calling,streaming,vision,status,error)
        if($Probe -and $row.models.Count -gt 0){
            $m=$row.models[0]
            $row.probe=Admin POST '/bridge/probe' @{model_id=$m.id;deep=[bool]$Deep}
        }
    }catch{}
    $report.accounts += [pscustomobject]$row
}
$logs=Join-Path $Root 'logs';New-Item -ItemType Directory -Force -Path $logs|Out-Null
$out=Join-Path $logs ('account-doctor-'+(Get-Date -Format 'yyyyMMdd-HHmmss')+'.json')
$report|ConvertTo-Json -Depth 30|Set-Content -LiteralPath $out -Encoding UTF8
$report|ConvertTo-Json -Depth 12
Write-Host "`nОтчёт: $out" -ForegroundColor Green
