param(
    [string]$Root,
    [switch]$Quick,
    [string]$Provider,
    [string]$Model,
    [int]$Limit = 25,
    [switch]$All
)
$ErrorActionPreference='Stop'
if(-not $Root){$Root=Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)}
. (Join-Path $Root 'scripts\security.ps1')
$c=Get-BridgeConfig $Root
$h=@{Authorization="Bearer $($c.admin_api_key)"}
$reg=Invoke-RestMethod 'http://127.0.0.1:4142/bridge/models' -Headers $h -TimeoutSec 20
$models=@($reg.models)
if($Provider){$models=@($models|Where-Object {$_.provider_id -eq $Provider})}
if($Model){$models=@($models|Where-Object {$_.id -eq $Model -or $_.native_id -eq $Model})}
if(-not $All -and $Limit -gt 0 -and $models.Count -gt $Limit){
    Write-Host ("Найдено {0} моделей. Для защиты квоты этот запуск проверит первые {1}. Используйте -All или -Limit N для расширения." -f $models.Count,$Limit) -ForegroundColor Yellow
    $models=@($models|Select-Object -First $Limit)
}
$rows=@()
foreach($m in $models){
    Write-Host ("Проверка {0} ..." -f $m.id) -ForegroundColor Cyan
    try{
        $body=@{model_id=$m.id;deep=(-not $Quick.IsPresent)}|ConvertTo-Json
        $r=Invoke-RestMethod -Method Post 'http://127.0.0.1:4142/bridge/probe' -Headers $h -ContentType 'application/json' -Body $body -TimeoutSec 180
        $pstatus=''
        if($r.protocols){
            $parts=@()
            foreach($prop in $r.protocols.PSObject.Properties){
                $v=$prop.Value
                $state=if($v.agent_compatible){'agent_ready'}elseif($v.basic){if($v.tool_calling){'agent_partial'}elseif($v.streaming){'stream'}else{'chat'}}else{'error'}
                $parts += ("{0}:{1}" -f $prop.Name,$state)
            }
            $pstatus=$parts -join ', '
        }
        $rows += [pscustomobject]@{Model=$m.id;Protocol=$r.selected_protocol;Status=$r.doctor_status;Basic=$r.basic;Stream=$r.streaming;System=$r.system_prompt;Tools=$r.tool_calling;ToolRoundTrip=$r.tool_round_trip;Parallel=$r.parallel_tools;Structured=$r.structured_output;Vision=$r.vision;Reasoning=$r.reasoning;Agent=$r.agent_compatible;LatencyMs=$r.latency_ms;Protocols=$pstatus;Error=$r.error}
    }catch{
        $rows += [pscustomobject]@{Model=$m.id;Protocol='';Status='error';Basic=$false;Stream=$false;System=$false;Tools=$false;ToolRoundTrip=$false;Parallel=$false;Structured=$false;Vision=$false;Reasoning=$false;Agent=$false;LatencyMs=0;Protocols='';Error=$_.Exception.Message}
    }
}
$rows|Format-Table -AutoSize
$out=Join-Path $Root 'logs\model-doctor.json'
$payload=[ordered]@{
    generated_at=(Get-Date).ToString('o')
    deep=(-not $Quick.IsPresent)
    provider=$Provider
    model=$Model
    checked=$rows.Count
    results=$rows
}
[IO.File]::WriteAllText($out,($payload|ConvertTo-Json -Depth 12),(New-Object Text.UTF8Encoding($false)))
Write-Host "Отчёт: $out" -ForegroundColor Green
Write-Host 'Глубокая проверка делает несколько реальных коротких запросов к каждой модели и может расходовать токены/quota. По умолчанию действует лимит 25 моделей.' -ForegroundColor Yellow
