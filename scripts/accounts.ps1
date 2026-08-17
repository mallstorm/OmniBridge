param([string]$Root)
if(-not $Root){$Root=Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)}
$exe=Join-Path $Root 'OmniBridge.exe'
if(Test-Path $exe){Start-Process -FilePath $exe -ArgumentList 'accounts' -WorkingDirectory $Root;return}
throw 'OmniBridge.exe не найден.'
