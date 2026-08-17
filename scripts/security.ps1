# OmniBridge bridge-key DPAPI helper
try{Add-Type -AssemblyName System.Security -ErrorAction Stop}catch{}
# OmniBridge local bridge-key loader. The keys are stored by the Go Core in
# data\secrets.json using Windows DPAPI (CurrentUser scope).
function Get-OmniSecret([string]$Root,[string]$Ref){
    $path=Join-Path $Root 'data\secrets.json'
    if(-not(Test-Path $path)){throw 'OmniBridge secrets store is not available yet.'}
    $d=Get-Content -Raw -LiteralPath $path|ConvertFrom-Json
    $prop=$d.items.PSObject.Properties[$Ref]
    if($null-eq$prop -or [string]::IsNullOrWhiteSpace([string]$prop.Value)){throw "Secret '$Ref' is not available."}
    $enc=[Convert]::FromBase64String([string]$prop.Value)
    $plain=[Security.Cryptography.ProtectedData]::Unprotect($enc,$null,[Security.Cryptography.DataProtectionScope]::CurrentUser)
    return [Text.Encoding]::UTF8.GetString($plain)
}
function Get-BridgeConfig([string]$Root){
    $path=Join-Path $Root 'data\config.json'
    if(-not(Test-Path $path)){throw 'Конфигурация OmniBridge ещё не создана.'}
    $c=Get-Content -Raw -LiteralPath $path|ConvertFrom-Json
    $data=Get-OmniSecret $Root 'bridge:data-api-key'
    $admin=Get-OmniSecret $Root 'bridge:admin-api-key'
    $c|Add-Member -NotePropertyName data_api_key -NotePropertyValue $data -Force
    $c|Add-Member -NotePropertyName admin_api_key -NotePropertyValue $admin -Force
    return $c
}
