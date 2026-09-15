param([string]$Root,[string]$Page='dashboard')
$ErrorActionPreference='Stop'
Add-Type -AssemblyName PresentationFramework
Add-Type -AssemblyName PresentationCore
Add-Type -AssemblyName WindowsBase
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing

try{Add-Type -TypeDefinition @"
using System;
using System.Runtime.InteropServices;
public static class OmniShellIdentity {
    [DllImport("shell32.dll", CharSet = CharSet.Unicode)]
    public static extern int SetCurrentProcessExplicitAppUserModelID(string appID);
}
"@
}catch{Write-Warning ('Shell identity helper: '+$_.Exception.Message)}
try{Add-Type -TypeDefinition @"
using System;
using System.Runtime.InteropServices;
public static class OmniTrayNative {
    [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
    [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr hWnd, out RECT lpRect);
    [DllImport("user32.dll")] public static extern bool SetWindowPos(IntPtr hWnd, IntPtr hWndInsertAfter, int X, int Y, int cx, int cy, uint uFlags);
    public const uint SWP_NOSIZE = 0x0001;
    public const uint SWP_NOZORDER = 0x0004;
    public const uint SWP_NOACTIVATE = 0x0010;
}
"@
}catch{Write-Warning ('Tray positioning helper: '+$_.Exception.Message)}

if(-not $Root){$Root=Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)}
$script:Root=$Root
$script:ConfigPath=Join-Path $Root 'data\config.json'
. (Join-Path $Root 'scripts\security.ps1')
$script:BridgeExe=Join-Path $Root 'OmniBridge.exe'
$script:Catalog=@()
$script:ModelRows=@()
$script:LogRows=@()
$script:LoginAttempt=''
$script:LoginAccount=''
$script:CurrentPage='dashboard'
$script:RouteConfig=$null
$script:RouterRefreshing=$false

function Read-Utf8([string]$Path){[IO.File]::ReadAllText($Path,[Text.Encoding]::UTF8)}
function Load-Xaml([string]$Path){
    $text=Read-Utf8 $Path
    $obj=[Windows.Markup.XamlReader]::Parse($text)
    foreach($m in [regex]::Matches($text,'x:Name="([A-Za-z0-9_]+)"')){
        $name=$m.Groups[1].Value
        $control=$obj.FindName($name)
        if($null -ne $control){Set-Variable -Scope Script -Name $name -Value $control}
    }
    return $obj
}
function Get-Config {Get-BridgeConfig $script:Root}
function Admin-Headers {$c=Get-Config;@{Authorization="Bearer $($c.admin_api_key)"}}
function Data-Headers {$c=Get-Config;@{Authorization="Bearer $($c.data_api_key)"}}
function Invoke-Admin([string]$Method,[string]$Path,$Body=$null,[int]$Timeout=60){
    $args=@{Method=$Method;Uri=('http://127.0.0.1:4142'+$Path);Headers=(Admin-Headers);TimeoutSec=$Timeout}
    if($null -ne $Body){$args.ContentType='application/json';$args.Body=($Body|ConvertTo-Json -Depth 12 -Compress)}
    Invoke-RestMethod @args
}
function Invoke-Data([string]$Method,[string]$Path,$Body=$null,[int]$Timeout=30){
    $c=Get-Config
    $args=@{Method=$Method;Uri=('http://'+$c.listen+$Path);Headers=(Data-Headers);TimeoutSec=$Timeout}
    if($null -ne $Body){$args.ContentType='application/json';$args.Body=($Body|ConvertTo-Json -Depth 12 -Compress)}
    Invoke-RestMethod @args
}
function Get-Health {try{Invoke-RestMethod 'http://127.0.0.1:4142/health' -TimeoutSec 2}catch{$null}}
function Ensure-Bridge {
    for($i=0;$i -lt 30;$i++){if(Get-Health){return $true};Start-Sleep -Milliseconds 180}
    throw 'Встроенный Core не перешёл в Ready.'
}
function Asset([string]$Name){
    $p=Join-Path $script:Root ('assets\'+$Name)
    if(-not(Test-Path $p)){return $null}
    $b=New-Object Windows.Media.Imaging.BitmapImage
    $b.BeginInit();$b.CacheOption=[Windows.Media.Imaging.BitmapCacheOption]::OnLoad;$b.UriSource=New-Object Uri($p);$b.EndInit();$b.Freeze();$b
}
function Brush([string]$Hex){New-Object Windows.Media.SolidColorBrush ([Windows.Media.ColorConverter]::ConvertFromString($Hex))}
function Add-FlatItem($Value,[System.Collections.Generic.List[object]]$Target){
    if($null-eq$Value){return}
    if($Value-is[System.Array]){foreach($item in $Value){Add-FlatItem $item $Target};return}
    [void]$Target.Add($Value)
}
function ConvertTo-FlatArray($Value){
    $items=New-Object 'System.Collections.Generic.List[object]'
    Add-FlatItem $Value $items
    return $items.ToArray()
}
function Set-ControlItems($Control,$Rows,$EmptyControl=$null,[bool]$SelectFirst=$false){
    $Control.ItemsSource=$null
    $Control.Items.Clear()
    foreach($row in @($Rows)){if($null-ne$row){[void]$Control.Items.Add($row)}}
    if($null-ne$EmptyControl){$EmptyControl.Visibility=if($Control.Items.Count-eq0){'Visible'}else{'Collapsed'}}
    if($SelectFirst-and$Control.Items.Count-gt0){$Control.SelectedIndex=0}
}
function Format-Num([Int64]$Value){if($Value -ge 1000000){'{0:N1}M' -f ($Value/1000000)}elseif($Value -ge 1000){'{0:N0}K' -f ($Value/1000)}else{[string]$Value}}
function Format-Score($Value){if($null-eq$Value){return '—'};try{return ('{0:N0}' -f [double]$Value)}catch{return '—'}}
function Format-DoctorScore($Model,[string]$Field){
    if($null-eq$Model){return '—'}
    $prop=$Model.PSObject.Properties[$Field]
    if($null-ne$prop -and $null-ne$prop.Value){try{return ('{0:N0}' -f [double]$prop.Value)}catch{}}
    if($Model.doctor_verified){return '0'}
    return '—'
}
function Format-TokenCapacity($Value){try{$n=[int64]$Value;if($n-le0){return '—'};if($n-ge1000000){return ('{0:N1}M' -f ($n/1000000))};if($n-ge1000){return ('{0:N0}K' -f ($n/1000))};return [string]$n}catch{return '—'}}
function Format-Age($Value){
    if(-not $Value){return 'не проверялась'}
    try{$d=(Get-Date)-([datetime]$Value);if($d.TotalMinutes-lt 1){'только что'}elseif($d.TotalHours-lt 1){'{0} мин. назад' -f [int]$d.TotalMinutes}elseif($d.TotalDays-lt 1){'{0} ч. назад' -f [int]$d.TotalHours}else{'{0} дн. назад' -f [int]$d.TotalDays}}catch{'—'}
}
function Format-Uptime($Value){try{$d=(Get-Date)-([datetime]$Value);'{0:00}:{1:00}:{2:00}' -f [int]$d.TotalHours,$d.Minutes,$d.Seconds}catch{'00:00:00'}}
function Provider-Name([string]$Kind,[string]$Fallback){
    $names=@{'openai'='OpenAI';'anthropic'='Anthropic';'gemini'='Google Gemini';'gigachat'='GigaChat';'yandex'='Yandex AI Studio';'openrouter'='OpenRouter';'deepseek'='DeepSeek';'qwen'='Alibaba / Qwen';'groq'='Groq';'ollama'='Ollama';'lmstudio'='LM Studio';'localai'='LocalAI';'vllm'='vLLM';'chatgpt-codex'='ChatGPT / Codex';'gemini-developer-oauth'='Google Gemini'}
    if($names.ContainsKey($Kind)){return $names[$Kind]};if($Fallback){return $Fallback};return $Kind
}
function Model-Icon($Model){
    $modelID=(([string]$Model.native_id)+' '+([string]$Model.name)+' '+([string]$Model.id)).ToLowerInvariant()
    $family=$null
    foreach($spec in @(
        @('openai','gpt-|gpt4|gpt5|o1|o3|o4|openai'),
        @('anthropic','claude|anthropic'),
        @('deepseek','deepseek'),
        @('qwen','qwen'),
        @('llama','llama|meta-llama'),
        @('mistral','mistral|mixtral'),
        @('gemma','gemma'),
        @('gemini','gemini'),
        @('kimi','kimi|moonshot'),
        @('glm','glm-|chatglm|zai'),
        @('minimax','minimax'),
        @('gigachat','gigachat'),
        @('perplexity','sonar|perplexity'),
        @('grok','grok|xai'),
        @('cohere','command-r|cohere'),
        @('phi','phi-|microsoft/phi'),
        @('yi','yi-|01-ai'),
        @('baichuan','baichuan'),
        @('step','step-|stepfun'),
        @('hunyuan','hunyuan')
    )){if($modelID-match$spec[1]){$family=$spec[0];break}}
    if($family){$img=Asset ('model-families\'+$family+'.png');if($img){return $img}}
    $providerIconID=[string]$Model.provider_id
    if($providerIconID){$img=Asset ('providers\'+$providerIconID+'.png');if($img){return $img}}
    return Asset 'nav_models.png'
}
function Model-Class($m){
    if($m.status -eq 'error'){return @{label='Error';bg='#3A1820';fg='#FF8290'}}
    if($m.status -eq 'rate_limited'){return @{label='Rate Limited';bg='#3A2D12';fg='#FFD45D'}}
    if($m.doctor_verified-and$m.chat_verified-and$m.identity_verified-and$m.agent_ready-and$m.doctor_status-eq'agent_ready'){return @{label='Agent Ready';bg='#123922';fg='#64EA88'}}
    if(-not$m.doctor_verified){return @{label='Не проверено';bg='#17304A';fg='#7DCCFF'}}
    if($m.streaming){return @{label='Chat Ready';bg='#11334D';fg='#65C5FF'}}
    if($m.status -eq 'ready'){return @{label='Chat Only';bg='#3A3012';fg='#F0D259'}}
    return @{label='Partial';bg='#29223E';fg='#BCA0FF'}
}
function Show-Toast([string]$Text,[string]$Kind='info'){
    $ToastText.Text=$Text
    $ToastHost.BorderBrush=Brush $(if($Kind-eq'error'){'#8A354A'}elseif($Kind-eq'ok'){'#2E7D4A'}elseif($Kind-eq'warn'){'#7A6729'}else{'#31527A'})
    $ToastText.Foreground=Brush $(if($Kind-eq'error'){'#FF9CAA'}elseif($Kind-eq'ok'){'#73E995'}elseif($Kind-eq'warn'){'#F0D66B'}else{'#DCEBFF'})
    $ToastHost.Visibility='Visible'
    $timer=New-Object Windows.Threading.DispatcherTimer;$timer.Interval=[TimeSpan]::FromSeconds(4)
    $handler={$ToastHost.Visibility='Collapsed';$timer.Stop()}.GetNewClosure()
    $timer.Add_Tick($handler);$timer.Start()
}
function Open-Folder([string]$Path){if(Test-Path $Path){Start-Process explorer.exe -ArgumentList ('"'+$Path+'"')}}
function Sync-VSCode {
    $scriptPath=Join-Path $script:Root 'scripts\sync-vscode.ps1'
    if(-not(Test-Path $scriptPath)){throw 'VS Code Connector script не найден.'}
    & $scriptPath -Root $script:Root | Out-Null
}

# Load the completely new presentation layer.
$app=[Windows.Application]::Current
if($null-eq$app){$app=New-Object Windows.Application}
$app.ShutdownMode=[Windows.ShutdownMode]::OnExplicitShutdown
$theme=[Windows.Markup.XamlReader]::Parse((Read-Utf8 (Join-Path $Root 'ui\Theme.xaml')))
$app.Resources.MergedDictionaries.Add($theme)
$window=Load-Xaml (Join-Path $Root 'ui\Shell.xaml')
$iconPath=Join-Path $Root 'assets\omnibridge.ico'
try{$null=[OmniShellIdentity]::SetCurrentProcessExplicitAppUserModelID('OmniBridge.UniversalAIGateway')}catch{Write-Warning ('AppUserModelID: '+$_.Exception.Message)}
if(Test-Path $iconPath){try{
    $window.Icon=[Windows.Media.Imaging.BitmapFrame]::Create((New-Object Uri($iconPath)))
    $script:TrayIcon=New-Object -TypeName System.Drawing.Icon -ArgumentList @($iconPath)
}catch{Write-Warning ('Application icon: '+$_.Exception.Message);$script:TrayIcon=$null}}
$script:Pages=@{}
foreach($entry in @{
    dashboard='Dashboard.xaml';api='ApiKeys.xaml';accounts='Accounts.xaml';models='Models.xaml';doctor='Doctor.xaml';router='Router.xaml';integration='Integration.xaml';services='Services.xaml';logs='Logs.xaml';settings='Settings.xaml';about='About.xaml'
}.GetEnumerator()){$script:Pages[$entry.Key]=Load-Xaml (Join-Path $Root ('ui\pages\'+$entry.Value))}

$logo=Asset 'omni.png';$BrandLogo.Source=$logo;$AboutLogo.Source=$logo
$DashChatIcon.Source=Asset 'chatgpt.png';$ChatAccountIcon.Source=Asset 'chatgpt.png'
$DashGeminiIcon.Source=Asset 'gemini.png';$GeminiAccountIcon.Source=Asset 'gemini.png'
try{
    $script:TrayDashboard=Load-Xaml (Join-Path $Root 'ui\TrayDashboard.xaml')
    $TrayBrandLogo.Source=$logo
}catch{Write-Warning ('Tray Dashboard XAML: '+$_.Exception.Message);$script:TrayDashboard=$null}

function Set-ServerPresentation($online,$state='Ready'){
    $ok=$online -and $state-ne'Error' -and $state-ne'Stopped'
    $color=if($ok){'#45DC6D'}else{'#FF6868'}
    $SidebarServerDot.Fill=Brush $color;$DashServerDot.Fill=Brush $color
    $SidebarServerText.Foreground=Brush $color;$SystemStatus.Foreground=Brush $color;$SystemStatusDot.Fill=Brush $color
    if($ok){$SidebarServerText.Text='Сервер запущен';$DashServerState.Text='Локальный сервер';$SystemStatus.Text=$state}else{$SidebarServerText.Text='Нет соединения';$DashServerState.Text='Core недоступен';$SystemStatus.Text='Ошибка'}
}
function Refresh-Dashboard {
    try{
        $st=Invoke-Admin GET '/bridge/status';$accounts=@(ConvertTo-FlatArray (Invoke-Admin GET '/bridge/accounts'));$snap=Invoke-Admin GET '/bridge/models';$models=@(ConvertTo-FlatArray $snap.models)
        $MetricProviders.Text=[string]$st.providers;$MetricAccounts.Text=[string]$st.accounts;$MetricModels.Text=[string]$st.models;$MetricReady.Text=[string]$st.agent_ready_models
        $state=if($st.lifecycle){[string]$st.lifecycle}else{'Ready'};Set-ServerPresentation $true $state
        $HeroState.Text=if($state-eq'Degraded'){'РАБОТАЕТ С ОГРАНИЧЕНИЯМИ'}else{'ВСЁ РАБОТАЕТ'}
        $HeroState.Foreground=Brush $(if($state-eq'Degraded'){'#E7C342'}else{'#45DC6D'})
        $HeroDescription.Text=if($state-eq'Degraded'){'Часть подключений требует внимания'}else{'OmniBridge активен и готов'}
        $endpoint='127.0.0.1:4142';if($st.listen){$endpoint=[string]$st.listen}
        $DashServerEndpoint.Text=$endpoint;$SidebarEndpoint.Text=('http://'+$endpoint);$SystemServer.Text=$endpoint
        $SystemPort.Text=if($endpoint-match':(\d+)$'){$Matches[1]}else{'—'}
        $DashServerVersion.Text=('Версия: '+$st.version);$SidebarVersion.Text=('v'+$st.version);$SystemVersion.Text=[string]$st.version;$SystemUptime.Text=Format-Uptime $st.started_at
        $SystemRegistry.Text=if($st.registry_updated_at){'В актуальном состоянии'}else{'Ожидает обновления'}
        $stat=$st.stats_24h;$StatsRequests.Text=Format-Num ([int64]$stat.requests);$StatsInput.Text=((Format-Num ([int64]$stat.input_tokens))+' / '+(Format-Num ([int64]$stat.cached_input_tokens)));$StatsOutput.Text=Format-Num ([int64]$stat.output_tokens)
        $StatsLatency.Text=if([int64]$stat.requests-gt 0){'{0:N2}s' -f (([double]$stat.latency_ms/[double]$stat.requests)/1000)}else{'—'}
        $modelCounts=@{};$readyCounts=@{};foreach($m in $models){$providerID=[string]$m.provider_id;$modelCounts[$providerID]=1+$modelCounts[$providerID];if(($m.agent_ready -or $m.doctor_status -eq 'agent_ready')-and$m.doctor_verified-and$m.status-ne'error'){$readyCounts[$providerID]=1+$readyCounts[$providerID]}}
        $byKind=@{};foreach($a in $accounts){$byKind[[string]$a.kind]=$a}
        $DashAccountsConnected.Text=('{0} подключено' -f @($accounts|Where-Object{$_.status-eq'connected'}).Count)
        foreach($spec in @(@{kind='chatgpt-codex';prefix='Chat'},@{kind='gemini-developer-oauth';prefix='Gemini'})){
            $a=$byKind[$spec.kind];$p=$spec.prefix
            $email=Get-Variable -Scope Script -Name ('Dash'+$p+'Email') -ValueOnly;$plan=Get-Variable -Scope Script -Name ('Dash'+$p+'Plan') -ValueOnly;$count=Get-Variable -Scope Script -Name ('Dash'+$p+'Models') -ValueOnly;$ready=Get-Variable -Scope Script -Name ('Dash'+$p+'Ready') -ValueOnly;$label=Get-Variable -Scope Script -Name ('Dash'+$p+'State') -ValueOnly;$dot=Get-Variable -Scope Script -Name ('Dash'+$p+'Dot') -ValueOnly
            if($a){$accountID=[string]$a.id;$email.Text=if($a.email){$a.email}else{$a.name};$plan.Text=if($a.plan){$a.plan}else{'Не предоставлен'};$count.Text=[string]$modelCounts[$accountID];$ready.Text=[string]$readyCounts[$accountID];$connected=$a.status-eq'connected';$label.Text=if($connected){'Подключен'}else{'Требуется вход'};$label.Foreground=Brush $(if($connected){'#45DC6D'}else{'#E7C342'});$dot.Fill=$label.Foreground}else{$email.Text='Не подключён';$plan.Text='—';$count.Text='0';$ready.Text='0';$label.Text='Ожидает входа';$label.Foreground=Brush '#8496AD';$dot.Fill=$label.Foreground}
        }
        $recent=@();foreach($m in @($models|Sort-Object {[datetime]$_.last_checked} -Descending|Select-Object -First 5)){$c=Model-Class $m;$protocol=if($m.preferred_protocol){$m.preferred_protocol}elseif($m.api_type){$m.api_type}else{'—'};$recent+=[pscustomobject]@{icon=(Model-Icon $m);name=$m.name;provider=$m.provider_name;badge=$c.label;badge_bg=(Brush $c.bg);badge_fg=(Brush $c.fg);protocol=$protocol;age=(Format-Age $m.last_checked);state_brush=(Brush $c.fg)}}
        Set-ControlItems $DashModelsList $recent $DashModelsEmpty
    }catch{Set-ServerPresentation $false 'Error';$HeroState.Text='CORE НЕДОСТУПЕН';$HeroState.Foreground=Brush '#FF6868';$HeroDescription.Text='Не удалось получить состояние';Show-Toast $_.Exception.Message 'error'}
}

function Refresh-Catalog {
    $script:Catalog=@();$catalogItems=@(ConvertTo-FlatArray (Invoke-Admin GET '/bridge/catalog'));foreach($p in $catalogItems){if($p-and$p.id-and$p.name){$script:Catalog+=[pscustomobject]@{display=[string]$p.name;id=[string]$p.id;raw=$p}}}
    Set-ControlItems $ProviderPreset $script:Catalog $null $true
}
function Apply-ProviderPreset {
    $x=$ProviderPreset.SelectedItem;if($null-eq$x){return};$p=$x.raw
    $ProviderName.Text=$p.name;$ProviderBaseUrl.Text=$p.base_url
    $ProviderScopePanel.Visibility=if($p.id-eq'gigachat'){'Visible'}else{'Collapsed'}
    $ProviderYandexPanel.Visibility=if($p.id-eq'yandex'){'Visible'}else{'Collapsed'}
}
function Get-ModelSnapshot {try{@(ConvertTo-FlatArray ((Invoke-Admin GET '/bridge/models').models))}catch{@()}}
function Refresh-Providers {
    try{$models=Get-ModelSnapshot;$counts=@{};foreach($m in $models){$counts[[string]$m.provider_id]=1+$counts[[string]$m.provider_id]};$rows=@();$providerItems=@(ConvertTo-FlatArray (Invoke-Admin GET '/bridge/providers'));foreach($p in $providerItems){if($p-and$p.id-and$p.name){$rows+=[pscustomobject]@{name=[string]$p.name;kind=(Provider-Name $p.kind $p.kind);endpoint=if($p.base_url){[string]$p.base_url}else{'Локальный runtime'};models=('{0} моделей' -f [int]$counts[[string]$p.id]);state=if($p.enabled){'Подключён'}else{'Отключён'};raw=$p}}};Set-ControlItems $ProvidersList $rows $ProvidersEmpty;$BtnProviderRefresh.IsEnabled=$rows.Count-gt0;$BtnProviderDelete.IsEnabled=$rows.Count-gt0}catch{Show-Toast $_.Exception.Message 'error'}
}
function Add-Provider {
    $x=$ProviderPreset.SelectedItem;if($null-eq$x){return};$p=$x.raw
    $body=@{kind=$p.id;name=$ProviderName.Text.Trim();base_url=$ProviderBaseUrl.Text.Trim();api_key=$ProviderApiKey.Password;allow_unverified=[bool]$ProviderAllowUnverified.IsChecked}
    if($p.id-eq'gigachat'){$body.scope=[string]$ProviderScope.Text}
    if($p.id-eq'yandex'){
        $folder=$ProviderYandexFolderID.Text.Trim()
        if([string]::IsNullOrWhiteSpace($folder)){Show-Toast 'Укажите Yandex Cloud Folder ID (заголовок OpenAI-Project).' 'warn';return}
        $body.project_id=$folder
    }
    try{Show-Toast 'Проверяю endpoint и получаю модели…';$r=Invoke-Admin POST '/bridge/providers' $body 100;$ProviderApiKey.Clear();Refresh-Providers;Refresh-Models;Refresh-Dashboard
        $disc=[int]$r.models_discovered;if($r.warning){Show-Toast ("Провайдер добавлен ({0} моделей). Предупреждение: {1}" -f $disc,$r.warning) 'warn'}else{Show-Toast ("Провайдер подключён: найдено моделей {0}." -f $disc) 'ok'}
    }catch{Show-Toast $_.Exception.Message 'error'}
}

function Refresh-Accounts {
    try{
        $rows=@();$accountItems=@(ConvertTo-FlatArray (Invoke-Admin GET '/bridge/accounts'))
        foreach($a in $accountItems){
            if($a.kind-eq'chatgpt-codex' -and $a.settings){$saved=if($a.settings.command){$a.settings.command}else{$a.settings.codex_path};if($saved -and [string]::IsNullOrWhiteSpace($CodexPath.Text)){$CodexPath.Text=[string]$saved}}
            if($a.kind-eq'gemini-developer-oauth' -and $a.settings -and $a.settings.client_json -and [string]::IsNullOrWhiteSpace($GeminiClientPath.Text)){$GeminiClientPath.Text=[string]$a.settings.client_json}
            if($a-and$a.id-and$a.name-and$a.status-eq'connected'){$rows+=[pscustomobject]@{name=[string]$a.name;kind=(Provider-Name $a.kind $a.kind);email=if($a.email){$a.email}else{'—'};plan=if($a.plan){$a.plan}else{'Не предоставлен'};state='Подключён';raw=$a}}
        }
        Set-ControlItems $AccountsList $rows $AccountsEmpty;$enabled=$rows.Count-gt0;$BtnAccountInfo.IsEnabled=$enabled;$BtnAccountRefresh.IsEnabled=$enabled;$BtnAccountLogout.IsEnabled=$enabled
    }catch{Show-Toast $_.Exception.Message 'error'}
}
function Existing-Account([string]$Kind){@((ConvertTo-FlatArray (Invoke-Admin GET '/bridge/accounts'))|Where-Object{$_.kind-eq$Kind}|Select-Object -First 1)}
function Start-AccountLogin([string]$Kind,[bool]$Device){
    try{
        $a=Existing-Account $Kind|Select-Object -First 1
        $settings=@{}
        if($null-ne$a -and $null-ne$a.settings){foreach($prop in $a.settings.PSObject.Properties){$settings[$prop.Name]=[string]$prop.Value}}
        if($Kind-eq'chatgpt-codex' -and -not[string]::IsNullOrWhiteSpace($CodexPath.Text)){$settings.command=$CodexPath.Text.Trim();$settings.codex_path=$CodexPath.Text.Trim()}
        if($Kind-eq'gemini-developer-oauth' -and -not[string]::IsNullOrWhiteSpace($GeminiClientPath.Text)){$settings.client_json=$GeminiClientPath.Text.Trim()}
        $name=if($Kind-eq'chatgpt-codex'){'ChatGPT / Codex'}else{'Google Gemini'}
        if($null-eq$a){$a=Invoke-Admin POST '/bridge/accounts' @{name=$name;kind=$Kind;settings=$settings}}
        else{
            # Persist paths entered after the connector was first created. Older
            # builds silently ignored these edits, which broke both account flows.
            $a=Invoke-Admin POST '/bridge/accounts' @{id=$a.id;name=$a.name;kind=$Kind;settings=$settings}
        }

        # Fail before opening a browser when the helper/JSON/loopback prerequisites
        # are invalid. The endpoint is secret-safe and returns only diagnostics.
        $pre=Invoke-Admin GET ("/bridge/accounts/$($a.id)/preflight") $null 20
        if(-not$pre.ready){throw $(if($pre.error){[string]$pre.error}else{'Проверка подключения не пройдена.'})}

        $r=Invoke-Admin POST ("/bridge/accounts/$($a.id)/login/start") @{device=$Device} 30
        $script:LoginAttempt=$r.attempt_id;$script:LoginAccount=$a.id
        $authUrl=if($r.auth_url){[string]$r.auth_url}elseif($r.url){[string]$r.url}else{''}
        $verificationUrl=if($r.verification_url){[string]$r.verification_url}else{''}
        $browserManaged=($r.browser_managed-eq$true)
        if($authUrl){Start-Process $authUrl}
        elseif($verificationUrl){Start-Process $verificationUrl}
        elseif($browserManaged){Show-Toast 'Официальный Codex CLI управляет входом и открытием браузера…'}
        if($r.user_code){$codeMessage='Код входа: '+[string]$r.user_code;if($verificationUrl){$codeMessage+="`n`nОткрыто: "+$verificationUrl};[Windows.MessageBox]::Show($codeMessage,'OmniBridge')|Out-Null}
        if(-not$authUrl -and -not$verificationUrl -and -not$browserManaged){throw 'Коннектор не вернул способ авторизации.'}
        $LoginTimer.Start();Show-Toast 'Ожидаю завершения входа в браузере…'
    }catch{Show-Toast $_.Exception.Message 'error'}
}
$LoginTimer=New-Object Windows.Threading.DispatcherTimer;$LoginTimer.Interval=[TimeSpan]::FromSeconds(2)
$LoginTimer.Add_Tick({if(-not$script:LoginAttempt){$LoginTimer.Stop();return};try{$s=Invoke-Admin GET ('/bridge/accounts/login/status?attempt='+[uri]::EscapeDataString($script:LoginAttempt));if($s.status-eq'completed' -and $s.success){$LoginTimer.Stop();$script:LoginAttempt='';$null=Invoke-Admin POST ("/bridge/accounts/$($script:LoginAccount)/refresh") @{} 90;Refresh-Accounts;Refresh-Models;Refresh-Dashboard;Show-Toast 'Аккаунт подключён.' 'ok'}elseif($s.status-eq'failed'){$LoginTimer.Stop();$script:LoginAttempt='';Show-Toast ('Ошибка входа: '+$s.error) 'error'}}catch{}})
function Selected-Account {if($AccountsList.SelectedItem){$AccountsList.SelectedItem.raw}else{$null}}

function Build-ModelRows {
    $rows=@();foreach($m in (Get-ModelSnapshot)){if(-not$m-or-not$m.id){continue};$c=Model-Class $m;$source=if($m.source-eq'account'){'Account'}elseif($m.source-eq'local'){'Local'}elseif($m.source-eq'curated'){'Curated'}else{'API Ключ'};$rows+=[pscustomobject]@{icon=(Model-Icon $m);name=$(if($m.name){$m.name}else{$m.native_id});provider=$(if($m.provider_name){$m.provider_name}else{$m.provider_id});provider_id=$m.provider_id;source=$source;badge=$c.label;badge_bg=(Brush $c.bg);badge_fg=(Brush $c.fg);protocol=$(if($m.preferred_protocol){$m.preferred_protocol}elseif($m.api_type){$m.api_type}else{'—'});latency=$(if($m.latency_ms){$m.latency_ms.ToString()+' ms'}else{'—'});doctor_verified=[bool]$m.doctor_verified;raw=$m}}
    $script:ModelRows=$rows
}
function Apply-ModelFilter {
    $q=$ModelsSearch.Text.Trim().ToLower();$provider=if($ModelsProviderFilter.SelectedItem){[string]$ModelsProviderFilter.SelectedItem}else{'Все провайдеры'};$source=if($ModelsSourceFilter.SelectedItem){[string]$ModelsSourceFilter.SelectedItem.Content}else{'Все источники'}
    $out=@($script:ModelRows|Where-Object{($q-eq'' -or $_.name.ToLower().Contains($q) -or $_.provider.ToLower().Contains($q))-and($provider-eq'Все провайдеры'-or$_.provider-eq$provider)-and($source-eq'Все источники'-or$_.source-eq$source)})
    $sort=if($ModelsSort -and $ModelsSort.SelectedItem){[string]$ModelsSort.SelectedItem.id}else{'provider'}
    switch($sort){
        'name'{$out=@($out|Sort-Object name,provider)}
        'doctor'{$out=@($out|Sort-Object @{Expression={[double]$_.raw.doctor_score};Descending=$true},name)}
        'latency'{$out=@($out|Sort-Object @{Expression={if([int64]$_.raw.latency_ms-gt0){[int64]$_.raw.latency_ms}else{[int64]::MaxValue}}},name)}
        'context'{$out=@($out|Sort-Object @{Expression={[int64]$_.raw.context_window};Descending=$true},name)}
        'agent'{$out=@($out|Sort-Object @{Expression={[bool]$_.raw.agent_ready};Descending=$true},@{Expression={[bool]$_.raw.identity_verified};Descending=$true},name)}
        default{$out=@($out|Sort-Object provider,name)}
    }
    Set-ControlItems $ModelsList $out $ModelsEmpty;$ModelsCount.Text=('Моделей: '+$out.Count);$BtnProbeSelectedModel.IsEnabled=$false
}
function Refresh-Models {
    try{
        $selectedID=$null;if($ModelsList.SelectedItem -and $ModelsList.SelectedItem.raw){$selectedID=[string]$ModelsList.SelectedItem.raw.id}
        Build-ModelRows;$providers=@('Все провайдеры')+@($script:ModelRows.provider|Sort-Object -Unique);Set-ControlItems $ModelsProviderFilter $providers $null $true
        if($ModelsSort.Items.Count-eq0){Set-ControlItems $ModelsSort @([pscustomobject]@{id='provider';display='Провайдер'},[pscustomobject]@{id='name';display='Имя'},[pscustomobject]@{id='doctor';display='Doctor score'},[pscustomobject]@{id='agent';display='Agent Ready'},[pscustomobject]@{id='latency';display='Задержка'},[pscustomobject]@{id='context';display='Контекст'}) $null $true}
        Apply-ModelFilter
        if($selectedID){for($i=0;$i-lt$ModelsList.Items.Count;$i++){if([string]$ModelsList.Items[$i].raw.id-eq$selectedID){$ModelsList.SelectedIndex=$i;break}}}
        if($ModelsList.SelectedItem){Update-ModelDetail}
        $routeModels=@($script:ModelRows|Where-Object{$_.raw.chat_verified}|ForEach-Object{[pscustomobject]@{display=($_.provider+' · '+$_.name);id=$_.raw.id}});Set-ControlItems $RouteModel $routeModels $null $true
        if($RouteStrategy.Items.Count-eq0){Set-ControlItems $RouteStrategy @([pscustomobject]@{id='priority';display='Приоритет + fallback'},[pscustomobject]@{id='weighted';display='Weighted'},[pscustomobject]@{id='round_robin';display='Round-robin'},[pscustomobject]@{id='fastest';display='Самая быстрая'},[pscustomobject]@{id='cost_optimized';display='Самая дешёвая'},[pscustomobject]@{id='agent_ready_only';display='Только Agent Ready'},[pscustomobject]@{id='local_only';display='Только локальные'}) $null $true}
    }catch{Show-Toast $_.Exception.Message 'error'}
}
function Update-ModelDetail {
    $r=$ModelsList.SelectedItem;if($null-eq$r){$BtnProbeSelectedModel.IsEnabled=$false;return};$BtnProbeSelectedModel.IsEnabled=$true;$m=$r.raw;$ModelDetailTitle.Text=$r.name;$ModelDetailProvider.Text=$r.provider;$ModelDetailProtocol.Text=if($m.preferred_protocol){$m.preferred_protocol}else{$m.api_type};$ModelDetailChecked.Text=Format-Age $(if($m.doctor_checked_at){$m.doctor_checked_at}else{$m.last_checked});$ModelDetailError.Text=$m.error
    $ModelDoctorScore.Text=Format-DoctorScore $m 'doctor_score';$ModelAgentScore.Text=Format-DoctorScore $m 'agent_score';$ModelCodingScore.Text=Format-DoctorScore $m 'coding_score';$ModelReliabilityScore.Text=Format-DoctorScore $m 'reliability_score'
    $ModelDetailContext.Text=Format-TokenCapacity $m.context_window;$ModelDetailOutput.Text=Format-TokenCapacity $m.max_output_tokens
    $CapChat.Opacity=if($m.chat_verified){1}else{0.35};$CapIdentity.Opacity=if($m.identity_verified){1}else{0.35};$CapStream.Opacity=if($m.streaming-and$m.chat_verified){1}else{0.35};$CapTools.Opacity=if($m.agent_ready-and$m.identity_verified){1}else{0.35};$CapParallel.Opacity=if($m.parallel_tools){1}else{0.35};$CapStructured.Opacity=if($m.structured_output){1}else{0.35};$CapReasoning.Opacity=if($m.reasoning){1}else{0.35};$CapVision.Opacity=if($m.vision){1}else{0.35}
}
function Probe-SelectedModel {
    $r=$ModelsList.SelectedItem;if($null-eq$r){return};try{Show-Toast ('Проверяю '+$r.name+'…');$res=Invoke-Admin POST '/bridge/probe' @{model_id=$r.raw.id;deep=$true} 220;Refresh-Models;Refresh-DoctorReport;Refresh-Dashboard;Show-Toast ("Проверка завершена: Doctor={0:N0} · Agent={1:N0} · Coding={2:N0} · Reliability={3:N0}" -f [double]$res.doctor_score,[double]$res.agent_score,[double]$res.coding_score,[double]$res.reliability_score) $(if($res.basic){'ok'}else{'warn'})}catch{Show-Toast $_.Exception.Message 'error'}
}

function Refresh-DoctorProviders {
    try{$rows=@([pscustomobject]@{display='Все подключения';id='*'});foreach($p in @(ConvertTo-FlatArray (Invoke-Admin GET '/bridge/providers'))){if($p-and$p.id-and$p.name){$rows+=[pscustomobject]@{display=[string]$p.name;id=[string]$p.id}}};foreach($a in @(ConvertTo-FlatArray (Invoke-Admin GET '/bridge/accounts'))){if($a-and$a.id-and$a.name-and$a.status-eq'connected'){$rows+=[pscustomobject]@{display=[string]$a.name;id=[string]$a.id}}};Set-ControlItems $DoctorProvider $rows $null $true;$BtnDoctorStart.IsEnabled=$rows.Count-gt1}catch{$BtnDoctorStart.IsEnabled=$false}
}
function Refresh-DoctorReport {
    Build-ModelRows;$rows=@();foreach($x in $script:ModelRows){$m=$x.raw;$c=Model-Class $m;$rows+=[pscustomobject]@{name=$x.name;provider=$x.provider;chat=if($m.chat_verified){'Chat ✓'}else{'Chat ×'};chat_color=Brush $(if($m.chat_verified){'#64EA88'}else{'#FF8290'});stream=if($m.streaming-and$m.chat_verified){'Stream ✓'}else{'Stream —'};stream_color=Brush $(if($m.streaming-and$m.chat_verified){'#64EA88'}else{'#E7C342'});tools=if($m.agent_ready-and$m.identity_verified){'Agent ✓'}else{'Agent —'};tools_color=Brush $(if($m.agent_ready-and$m.identity_verified){'#64EA88'}else{'#E7C342'});icon=(Model-Icon $m);doctor_score=Format-DoctorScore $m 'doctor_score';agent_score=Format-DoctorScore $m 'agent_score';coding_score=Format-DoctorScore $m 'coding_score';reliability_score=Format-DoctorScore $m 'reliability_score';class=$c.label;class_color=Brush $c.fg}};Set-ControlItems $DoctorReport $rows $DoctorEmpty
}
function Start-Doctor {
    $p=$DoctorProvider.SelectedItem;if($null-eq$p){Show-Toast 'Сначала выберите подключение.' 'warn';return};$targets=@((Get-ModelSnapshot)|Where-Object{($p.id-eq'*'-or$_.provider_id-eq$p.id)-and(-not[bool]$DoctorFailedOnly.IsChecked-or-not$_.chat_verified-or$_.status-eq'error')});if(-not$targets.Count){Show-Toast 'Нет моделей, подходящих под выбранный фильтр.' 'warn';return};$BtnDoctorStart.IsEnabled=$false
    $selectedProvider=@((ConvertTo-FlatArray (Invoke-Admin GET '/bridge/providers'))|Where-Object{$_.id-eq$p.id-or($p.id-eq'*'-and$_.kind-eq'yandex')}|Select-Object -First 1)
    if($selectedProvider -and ($selectedProvider.kind-eq'yandex'-or$p.id-eq'*')){
        $answer=[Windows.MessageBox]::Show(("Глубокая проверка Yandex выполнит несколько платных запросов для каждой из {0} моделей. Автоматический prompt cache уменьшает стоимость повторяющихся префиксов, но первая проверка тарифицируется. Продолжить?"-f$targets.Count),'OmniBridge · Yandex Model Doctor','YesNo','Warning')
        if($answer-ne'Yes'){$BtnDoctorStart.IsEnabled=$true;return}
    }
    try{$done=0;$failed=0;foreach($m in $targets){try{$null=Invoke-Admin POST '/bridge/probe' @{model_id=$m.id;deep=$true} 220}catch{$failed++};$done++;$BtnDoctorStart.Content=("{0}/{1}"-f$done,$targets.Count);[Windows.Threading.Dispatcher]::CurrentDispatcher.Invoke([action]{},'Background')};Refresh-Models;Refresh-DoctorReport;Refresh-Dashboard;Show-Toast ("Model Doctor: проверено {0}, ошибок {1}."-f$done,$failed) $(if($failed){'warn'}else{'ok'})}finally{$BtnDoctorStart.IsEnabled=$true;$BtnDoctorStart.Content='Запустить'}
}

function Refresh-Integration {
    try{
        $c=Get-Config;$models=Get-ModelSnapshot;$err=@($models|Where-Object{$_.status-eq'error'}).Count
        $vsExport=Invoke-Admin GET '/bridge/vscode/export';$agent=[int]$vsExport.agent_picker_models;$chat=[int]$vsExport.models_total
        $VSEndpoint.Text=('http://'+$c.listen+'/v1');$VSKey.Text=('••••••••'+$c.data_api_key.Substring([Math]::Max(0,$c.data_api_key.Length-6)));$VSAgentCount.Text=[string]$agent;$VSChatCount.Text=[string]$chat;$VSErrorCount.Text=[string]$err
        try{
            $catalog=Invoke-Data GET '/v1/models' $null 5;$clineModels=@(ConvertTo-FlatArray $catalog.data)
            $ClineStatus.Text=('Cline API: {0} моделей доступно' -f $clineModels.Count)
            $ClineStatus.Foreground=Brush $(if($clineModels.Count-gt0){'#64EA88'}else{'#F0D259'})
        }catch{
            $ClineStatus.Text='Cline API: ошибка доступа к /v1/models'
            $ClineStatus.Foreground=Brush '#FF8290'
        }
        try{
            $hub=Invoke-Admin GET '/bridge/integrations';$items=@(ConvertTo-FlatArray $hub.integrations);$det=@($items|Where-Object{$_.detected});$cfg=@($items|Where-Object{$_.status-eq'configured'})
            $IntegrationHubDetected.Text=('обнаружено: {0} / {1}'-f$det.Count,$items.Count)
            $names=@($det|Select-Object -First 4|ForEach-Object{$_.name});$summary=if($names.Count){$names-join', '}else{'Клиенты пока не обнаружены'}
            if($det.Count-gt4){$summary+=('  +{0}'-f($det.Count-4))}
            if($cfg.Count){$summary+=(' · настроено: {0}'-f$cfg.Count)}
            $IntegrationHubState.Text=$summary
        }catch{$IntegrationHubState.Text='Integration Hub временно недоступен';$IntegrationHubDetected.Text='обнаружено: —'}
    }catch{Show-Toast $_.Exception.Message 'error'}
}

function Refresh-Services {
    try{
        $qc=Invoke-Admin GET '/bridge/quota-cost';$quotas=@(ConvertTo-FlatArray $qc.quota);$prices=@(ConvertTo-FlatArray $qc.prices);$known=@($quotas|Where-Object{$_.remaining_known})
        $SvcQuotaKnown.Text=('{0} подключений'-f$known.Count);$SvcPricingKnown.Text=('{0} моделей'-f$prices.Count)
        $daily=0.0;if($qc.budget-and$null-ne$qc.budget.daily_spent_usd){$daily=[double]$qc.budget.daily_spent_usd};$SvcDailyCost.Text=[string]::Format([Globalization.CultureInfo]::InvariantCulture,'${0:0.0000}',$daily)
        $SvcQuotaState.Text=if($known.Count){'Подтверждённые лимиты доступны'}else{'Нет подтверждённых лимитов'};$SvcQuotaBadge.Text=if($qc.version){[string]$qc.version}else{'1.0'}
    }catch{$SvcQuotaState.Text='Quota & Cost недоступен';$SvcQuotaKnown.Text='—';$SvcPricingKnown.Text='—'}
    try{
        $cg=Invoke-Admin GET '/bridge/context-guard';$cc=$cg.config;$enabled=[bool]$cc.enabled;$mode=if($cc.mode){[string]$cc.mode}else{'compact'}
        $SvcContextState.Text=if($enabled){((Get-Culture).TextInfo.ToTitleCase($mode))+' · включён'}else{'Выключен'};$SvcContextBadge.Text=if($cg.version){[string]$cg.version}else{'1.0'}
        $events=0;$saved=0;if($cg.stats){$events=[int64]$cg.stats.events;$saved=[int64]$cg.stats.saved_tokens};$SvcContextEvents.Text=[string]$events;$SvcContextSaved.Text=(Format-TrayNumber $saved)
        $pct=if($null-ne$cc.max_context_percent){[double]$cc.max_context_percent}else{90};$SvcContextPercent.Text=('{0:0.#}%'-f$pct);$BtnContextToggle.Content=if($enabled){'Отключить Guard'}else{'Включить Guard'}
    }catch{$SvcContextState.Text='Context Guard недоступен'}
    try{
        $m=Invoke-Admin GET '/bridge/mcp';$SvcMCPCount.Text=[string]$m.count;$SvcMCPEnabled.Text=[string]$m.enabled;$SvcMCPReady.Text=[string]$m.ready;$SvcMCPState.Text=if([int]$m.count){('{0} серверов настроено'-f$m.count)}else{'Серверы не настроены'}
    }catch{$SvcMCPState.Text='MCP Control Plane недоступен'}
    try{
        $o=Invoke-Admin GET '/bridge/observability';$snap=$o.snapshot;$SvcObsRequests.Text=[string]$snap.requests;$SvcObsErrors.Text=[string]$snap.errors;$SvcTraceState.Text=if($o.persistent){'Persistent · SQLite'}else{'Runtime only'}
        $remote=Invoke-Admin GET '/bridge/remote';$SvcRemoteState.Text=if($remote.config.enabled){'Remote включён · TLS + CIDR'}else{'Remote выключен · localhost only'}
    }catch{$SvcRemoteState.Text='Observability недоступна'}
}

function Parse-Log([string]$Line,[string]$Source){
    $level='INFO';if($Line-match'(?i)fatal|error|failed'){$level='ERROR'}elseif($Line-match'(?i)warn|degraded'){$level='WARN'}elseif($Line-match'(?i)debug'){$level='DEBUG'}
    $time='—';if($Line-match'^(\d{4}/\d{2}/\d{2}\s+\d{2}:\d{2}:\d{2})(?:\.\d+)?'){$time=$Matches[1].Substring(5);$Line=$Line.Substring($Matches[0].Length).Trim()}
    [pscustomobject]@{time=$time;level=$level;source=$Source;message=$Line}
}
function Apply-LogFilter {$q=$LogsSearch.Text.Trim().ToLower();$lv=[string]$LogsLevel.Text;if(-not$lv){$lv='Все уровни'};$rows=@($script:LogRows|Where-Object{($q-eq''-or$_.message.ToLower().Contains($q))-and($lv-eq'Все уровни'-or$_.level-eq$lv)});Set-ControlItems $LogsList $rows $LogsEmpty;$LogsCount.Text=('{0} событий'-f$rows.Count)}
function Refresh-Logs {
    $rows=@()
    foreach($spec in @(@{file='bridge.log';source='Core'},@{file='ui.log';source='Desktop'})){
        $p=Join-Path $script:Root ('logs\'+$spec.file)
        if(Test-Path $p){foreach($line in @(Get-Content -LiteralPath $p -Tail 350 -ErrorAction SilentlyContinue)){if($line){$rows+=Parse-Log $line $spec.source}}}
    }
    $script:LogRows=$rows;Apply-LogFilter
}

function Refresh-Routes {
    try{$script:RouteConfig=Invoke-Admin GET '/bridge/router';$rows=@();if($script:RouteConfig.aliases){foreach($prop in $script:RouteConfig.aliases.psobject.Properties){$policy=$null;if($script:RouteConfig.policies){$pp=$script:RouteConfig.policies.psobject.Properties[$prop.Name];if($pp){$policy=$pp.Value}};$count=if($policy){@($policy.candidates).Count}else{1};$mode=if($policy){[string]$policy.mode}else{'direct'};$rows+=[pscustomobject]@{alias=$prop.Name;target=('{0} · {1} моделей'-f$mode,$count)}}};Set-ControlItems $RoutesList $rows $RoutesEmpty;$BtnRouteDelete.IsEnabled=$rows.Count-gt0}catch{Set-ControlItems $RoutesList @() $RoutesEmpty;$BtnRouteDelete.IsEnabled=$false}
}
function Copy-RouteArrayMap($Value){
    $map=@{}
    if($Value){foreach($p in $Value.psobject.Properties){$map[$p.Name]=@($p.Value)}}
    return $map
}
function Copy-PolicyMap($Value){
    $map=@{}
    if($Value){foreach($p in $Value.psobject.Properties){$x=$p.Value;$weights=@{};$quota=@{};$cost=@{};if($x.weights){foreach($w in $x.weights.psobject.Properties){$weights[$w.Name]=[double]$w.Value}};if($x.quota_hints){foreach($w in $x.quota_hints.psobject.Properties){$quota[$w.Name]=[double]$w.Value}};if($x.cost_hints){foreach($w in $x.cost_hints.psobject.Properties){$cost[$w.Name]=[double]$w.Value}};$map[$p.Name]=@{mode=[string]$x.mode;candidates=@($x.candidates);weights=$weights;quota_hints=$quota;cost_hints=$cost}}}
    return $map
}
function Save-Route {
    $alias=$RouteAlias.Text.Trim();$selected=@($RouteModel.SelectedItems);if(-not$selected.Count-and$RouteModel.SelectedItem){$selected=@($RouteModel.SelectedItem)};if(-not$alias-or-not$selected.Count){Show-Toast 'Укажите alias и выберите одну или несколько проверенных моделей.' 'warn';return}
    try{
        $ids=@($selected|ForEach-Object{[string]$_.id});$aliases=@{};if($script:RouteConfig.aliases){foreach($p in $script:RouteConfig.aliases.psobject.Properties){$aliases[$p.Name]=[string]$p.Value}};$aliases[$alias]=$ids[0]
        $priorities=Copy-RouteArrayMap $script:RouteConfig.priorities;$fallbacks=Copy-RouteArrayMap $script:RouteConfig.fallbacks;$policies=Copy-PolicyMap $script:RouteConfig.policies
        $priorities[$alias]=$ids;$fallbacks[$alias]=if($ids.Count-gt1){@($ids[1..($ids.Count-1)])}else{@()}
        $mode=if($RouteStrategy.SelectedItem){[string]$RouteStrategy.SelectedItem.id}else{'priority'};$weights=@{};$rawWeights=@($RouteWeights.Text-split'[,; ]+'|Where-Object{$_})
        for($i=0;$i-lt$ids.Count;$i++){$weight=1.0;if($i-lt$rawWeights.Count){$null=[double]::TryParse($rawWeights[$i],[Globalization.NumberStyles]::Float,[Globalization.CultureInfo]::InvariantCulture,[ref]$weight);if($weight-le0){$weight=1.0}};$weights[$ids[$i]]=$weight}
        $policies[$alias]=@{mode=$mode;candidates=$ids;weights=$weights}
        $maxRetries=if($null-ne$script:RouteConfig.max_retries){[int]$script:RouteConfig.max_retries}else{2}
        $script:RouteConfig=Invoke-Admin PUT '/bridge/router' @{aliases=$aliases;priorities=$priorities;fallbacks=$fallbacks;policies=$policies;max_retries=$maxRetries;session_stickiness=[bool]$RouteStickiness.IsChecked;session_ttl_seconds=1800}
        Refresh-Routes;Show-Toast ('Маршрут сохранён: '+$ids.Count+' моделей.') 'ok'
    }catch{Show-Toast $_.Exception.Message 'error'}
}
function Get-RouterProfiles {
    @(
        [pscustomobject]@{id='auto/smart';display='Auto Smart'},
        [pscustomobject]@{id='auto/coding';display='Auto Coding'},
        [pscustomobject]@{id='auto/fast';display='Auto Fast'},
        [pscustomobject]@{id='auto/cheap';display='Auto Cheap'},
        [pscustomobject]@{id='auto/local';display='Auto Local'},
        [pscustomobject]@{id='auto/reasoning';display='Auto Reasoning'},
        [pscustomobject]@{id='auto/vision';display='Auto Vision'},
        [pscustomobject]@{id='auto/agent';display='Auto Agent'}
    )
}
function Get-RouterRequestTypes {
    @(
        [pscustomobject]@{id='chat';display='Обычный Chat'},
        [pscustomobject]@{id='tools';display='Tool Calling'},
        [pscustomobject]@{id='parallel';display='Parallel Tools'},
        [pscustomobject]@{id='vision';display='Vision'},
        [pscustomobject]@{id='reasoning';display='Reasoning'},
        [pscustomobject]@{id='json';display='Structured JSON'},
        [pscustomobject]@{id='agent';display='Agent Loop'}
    )
}
function Ensure-RouterSelectors {
    if($RouterProfile.Items.Count-eq0){Set-ControlItems $RouterProfile (Get-RouterProfiles) $null $true}
    if($RouterTestProfile.Items.Count-eq0){Set-ControlItems $RouterTestProfile (Get-RouterProfiles) $null $true}
    if($RouterTestType.Items.Count-eq0){Set-ControlItems $RouterTestType (Get-RouterRequestTypes) $null $true}
}
function Router-ScoreDetail($Score){
    if($null-eq$Score-or$null-eq$Score.components){return 'runtime telemetry'}
    $c=$Score.components
    $parts=@()
    foreach($spec in @(@('Doctor','doctor'),@('Agent','agent_score'),@('Coding','coding_score'),@('Health','health'),@('Latency','latency'))){
        $p=$c.psobject.Properties[$spec[1]]
        if($p -and [math]::Abs([double]$p.Value)-gt0.01){$parts+=('{0} {1:N0}' -f $spec[0],[double]$p.Value)}
    }
    if(-not$parts.Count){return 'runtime telemetry'}
    return ($parts -join ' · ')
}
function Refresh-RouterUI {
    if($script:RouterRefreshing){return}
    $script:RouterRefreshing=$true
    try{
        Ensure-RouterSelectors
        $selected=$RouterProfile.SelectedItem
        $profile=if($selected){[string]$selected.id}else{'auto/smart'}
        $diag=Invoke-Admin GET ('/bridge/router/diagnostics?model='+[uri]::EscapeDataString($profile)) $null 15
        $res=Invoke-Admin GET '/bridge/resilience' $null 15
        $models=@(Get-ModelSnapshot);$byID=@{};foreach($m in $models){$byID[[string]$m.id]=$m}
        $scoreMap=@{};foreach($sc in @(ConvertTo-FlatArray $diag.scores)){if($sc-and$sc.candidate_id){$scoreMap[[string]$sc.candidate_id]=$sc}}
        $rows=@();$rank=0
        foreach($id in @(ConvertTo-FlatArray $diag.ordered_candidates)){
            $rank++;$key=[string]$id;$m=$byID[$key];$sc=$scoreMap[$key]
            $modelName=if($m){if($m.name){[string]$m.name}else{[string]$m.native_id}}else{$key}
            $provider=if($m){if($m.provider_name){[string]$m.provider_name}else{[string]$m.provider_id}}else{'—'}
            $scoreText=if($sc){'{0:N1}' -f [double]$sc.total}else{'—'}
            $rows+=[pscustomobject]@{rank=('#'+$rank);model=$modelName;provider=$provider;score=$scoreText;detail=(Router-ScoreDetail $sc)}
        }
        Set-ControlItems $RouterCandidates $rows $RouterCandidatesEmpty
        $RouterCandidateCount.Text=('{0} кандидатов' -f $rows.Count)
        $RouterVirtualCount.Text=[string]@($diag.virtual_models).Count
        $RouterAffinityCount.Text=if($diag.runtime){[string]$diag.runtime.affinity_bindings}else{'0'}
        $RouterVersion.Text=if($diag.runtime-and$diag.runtime.version){[string]$diag.runtime.version}else{'3.1'}
        $RouterProfileHint.Text=switch($profile){'auto/coding'{'Приоритет Agent Ready, Tools, Streaming и Coding Score.'}'auto/fast'{'Приоритет минимального P95/AVG latency и текущего health.'}'auto/cheap'{'Стоимость учитывается только когда pricing действительно известен.'}'auto/local'{'Используются только локальные модели.'}'auto/reasoning'{'Требуется подтверждённый Reasoning capability.'}'auto/vision'{'Требуется подтверждённый Vision capability.'}'auto/agent'{'Требуется Agent Ready и Doctor proof.'}default{'Баланс Doctor, health, reliability, latency, load, quota и cost.'}}

        $poolRows=@();$poolCount=0;$healthyPools=0
        foreach($pool in @(ConvertTo-FlatArray $res.connection_pools)){
            $poolCount++;$healthy=[int]$pool.healthy;$degraded=[int]$pool.degraded;$open=[int]$pool.open
            if($healthy-gt0-and$open-eq0){$healthyPools++}
            $state=if($open-gt0){'OPEN'}elseif($degraded-gt0){'DEGRADED'}else{'HEALTHY'}
            $color=Brush $(if($state-eq'HEALTHY'){'#64EA88'}elseif($state-eq'DEGRADED'){'#FFD45D'}else{'#FF8290'})
            $modelsCount=0;foreach($member in @(ConvertTo-FlatArray $pool.members)){$modelsCount+=[int]$member.models}
            $poolRows+=[pscustomobject]@{name=[string]$pool.pool;members=('{0} подключений · {1} healthy' -f [int]$pool.member_count,$healthy);state=$state;state_color=$color;models=('{0} моделей' -f $modelsCount)}
        }
        Set-ControlItems $RouterPoolsList $poolRows $RouterPoolsEmpty
        $RouterPoolsHealthy.Text=('{0} / {1}' -f $healthyPools,$poolCount)
        $openBreakers=@(ConvertTo-FlatArray $res.breakers|Where-Object{$_.state-eq'OPEN'}).Count
        $degradedBreakers=@(ConvertTo-FlatArray $res.breakers|Where-Object{$_.state-eq'DEGRADED'-or$_.state-eq'HALF_OPEN'}).Count
        $RouterBreakerSummary.Text=('{0} open · {1} degraded' -f $openBreakers,$degradedBreakers)
        $RouterResilienceState.Text=if($res.persistent){'Persistent · SQLite'}else{'Runtime only'}
        $RouterResilienceState.Foreground=Brush $(if($res.persistent){'#64EA88'}else{'#FFD45D'})
    }catch{
        Set-ControlItems $RouterCandidates @() $RouterCandidatesEmpty;Set-ControlItems $RouterPoolsList @() $RouterPoolsEmpty
        $RouterTestState.Text='Router недоступен';$RouterDecision.Text=$_.Exception.Message
    }finally{$script:RouterRefreshing=$false}
}
function Invoke-RouterDryRun {
    try{
        Ensure-RouterSelectors
        $p=$RouterTestProfile.SelectedItem;$t=$RouterTestType.SelectedItem
        if($null-eq$p-or$null-eq$t){return}
        $model=[string]$p.id;$kind=[string]$t.id
        $body=@{model=$model;stream=$true;messages=@(@{role='user';content='OmniBridge routing dry-run'})}
        $tool=@{type='function';function=@{name='lookup';description='Synthetic routing capability probe';parameters=@{type='object';properties=@{query=@{type='string'}};required=@('query')}}}
        switch($kind){
            'tools'{$body.tools=@($tool)}
            'parallel'{$body.tools=@($tool);$body.parallel_tool_calls=$true}
            'vision'{$body.messages=@(@{role='user';content=@(@{type='text';text='Analyze'},@{type='image_url';image_url=@{url='data:image/png;base64,iVBORw0KGgo='}})})}
            'reasoning'{$body.reasoning_effort='medium'}
            'json'{$body.response_format=@{type='json_schema';json_schema=@{name='route_test';schema=@{type='object';properties=@{ok=@{type='boolean'}};required=@('ok')}}}}
            'agent'{$body.tools=@($tool);$body.tool_choice=@{type='function';function=@{name='lookup'}};$body.previous_response_id='dry-run-session'}
        }
        $result=Invoke-Admin POST '/bridge/router/evaluate?api_type=chat-completions' $body 20
        $req=$result.requirements;$flags=@()
        foreach($spec in @(@('Stream','streaming'),@('System','system_prompt'),@('Tools','tool_calling'),@('Parallel','parallel_tools'),@('JSON','structured_output'),@('Vision','vision'),@('Reasoning','reasoning'),@('Agent','agent_mode'))){$prop=$req.psobject.Properties[$spec[1]];if($prop-and$prop.Value-eq$true){$flags+=$spec[0]}}
        if(-not$flags.Count){$flags+='Chat'}
        $RouterTestState.Text=if($result.compatible){'Совместимый маршрут найден'}else{'Совместимых моделей нет'}
        $RouterTestState.Foreground=Brush $(if($result.compatible){'#64EA88'}else{'#FF8290'})
        $RouterRequirements.Text=('Requirements: '+($flags-join', ')+' · context ≥ '+(Format-TokenCapacity $req.minimum_context_tokens))
        $ordered=@(ConvertTo-FlatArray $result.ordered_candidates);$preview=@($ordered|Select-Object -First 4)
        $RouterDecision.Text=if($preview.Count){'Маршрут: '+($preview-join' → ')}else{'Маршрут: —'}
    }catch{$RouterTestState.Text='Dry-run завершился ошибкой';$RouterTestState.Foreground=Brush '#FF8290';$RouterDecision.Text=$_.Exception.Message}
}

function Refresh-Settings {try{$c=Get-Config;$SettingsListen.Text=$c.listen;$path=Join-Path $script:Root 'data';$SettingsDataPath.Text=$path;$SettingsDataPath.ToolTip=$path;$SettingsVersion.Text=$c.version;Refresh-Routes}catch{}}
function Create-Backup {
    try{
        $dir=Join-Path $script:Root 'data\backups';New-Item -ItemType Directory -Force -Path $dir|Out-Null
        $stamp=Get-Date -Format 'yyyyMMdd-HHmmss';$zip=Join-Path $dir ('OmniBridge-backup-'+$stamp+'.zip')
        $dataDir=Join-Path $script:Root 'data'
        $files=@('config.json','model-registry.json','usage-stats.json','routing.json')|ForEach-Object{Join-Path $dataDir $_}|Where-Object{Test-Path $_}
        if(-not $files.Count){throw 'Нет данных для резервного копирования.'}
        Compress-Archive -Path $files -DestinationPath $zip -CompressionLevel Optimal
        Show-Toast ('Backup создан: '+$zip) 'ok'
    }catch{Show-Toast $_.Exception.Message 'error'}
}
function Restart-Server {
    try{
        New-Item -ItemType File -Force -Path (Join-Path $script:Root 'data\restart.request')|Out-Null
        $script:RestartRequested=$true
        Request-ApplicationExit
    }catch{Show-Toast $_.Exception.Message 'error'}
}
function Refresh-About {$h=Get-Health;if($h){$AboutVersion.Text=('Версия '+$h.version);$SidebarVersion.Text=('v'+$h.version)}}
function Refresh-All {Refresh-Dashboard;Refresh-Providers;Refresh-Accounts;Refresh-Models;Refresh-DoctorProviders;Refresh-DoctorReport;Refresh-RouterUI;Refresh-Integration;Refresh-Services;Refresh-Settings;Refresh-About}

function Show-Page([string]$Name){
    if(-not$script:Pages.ContainsKey($Name)){$Name='dashboard'};$script:CurrentPage=$Name;$PageHost.Children.Clear();[void]$PageHost.Children.Add($script:Pages[$Name])
    $hints=@{dashboard=@('Подсказка','Model Doctor проверяет реальные возможности моделей перед экспортом.');api=@('Автонастройка','Вставьте ключ — endpoint, модели и протокол будут определены автоматически.');accounts=@('Безопасный вход','Пароли не сохраняются; используется официальный browser/device flow.');models=@('Model Registry','Doctor Score и capability показывают фактически проверенное состояние модели.');doctor=@('Практическая проверка','Глубокий тест формирует Doctor, Agent, Coding и Reliability Score.');router=@('Doctor-Aware Router','auto/* выбирает upstream по реальным capability, health, latency и runtime telemetry.');integration=@('Интеграция','Integration Hub обнаруживает поддерживаемые клиенты и выдаёт безопасный план подключения без перезаписи чужой конфигурации.');services=@('Сервисы Core 1.0','Quota & Cost, Context Guard, MCP и Observability используют один защищённый локальный Core.');logs=@('Безопасные журналы','Секреты и полные токены не должны попадать в логи.');settings=@('Единое приложение','Сворачивание скрывает окно в трей, крестик полностью завершает OmniBridge.');about=@('OmniBridge','Один локальный endpoint для всех поддерживаемых AI.')};$HintTitle.Text=$hints[$Name][0];$HintText.Text=$hints[$Name][1]
    switch($Name){'dashboard'{Refresh-Dashboard}'api'{if(-not$script:Catalog.Count){Refresh-Catalog};Refresh-Providers}'accounts'{Refresh-Accounts}'models'{Refresh-Models}'doctor'{Refresh-DoctorProviders;Refresh-DoctorReport}'router'{Refresh-RouterUI}'integration'{Refresh-Integration}'services'{Refresh-Services}'logs'{Refresh-Logs}'settings'{Refresh-Settings}'about'{Refresh-About}}
}

# Integrated Shell + tray lifecycle.
$script:Exiting=$false
$script:RestartRequested=$false
$script:NotifyIcon=$null
$script:TrayReady=$false
$script:TrayHintShown=$false
$script:TrayState=$null
$script:TrayGigaIndex=0
$script:TrayGigaExpiresAt=$null
$script:TrayTick=0
$script:TrayPopupTimer=$null
$script:TrayToastTimer=$null
$script:TrayTimer=New-Object Windows.Threading.DispatcherTimer
$script:TrayTimer.Interval=[TimeSpan]::FromSeconds(1)

function Show-MainWindow([string]$PageName=''){
    Hide-TrayDashboard
    if($PageName){Show-Page $PageName}
    $window.ShowInTaskbar=$true
    if(-not$window.IsVisible){$window.Show()}
    $window.WindowState='Normal'
    $window.Activate()|Out-Null
    $window.Topmost=$true;$window.Topmost=$false
}
function Hide-MainWindow {
    if($script:TrayReady-and$script:NotifyIcon){
        $window.ShowInTaskbar=$false
        $window.Hide()
        if(-not$script:TrayHintShown){
            $script:NotifyIcon.BalloonTipTitle='OmniBridge работает в Tray'
            $script:NotifyIcon.BalloonTipText='Нажмите значок OmniBridge, чтобы открыть панель Tray.'
            $script:NotifyIcon.ShowBalloonTip(1800)
            $script:TrayHintShown=$true
        }
    }else{
        $window.ShowInTaskbar=$true
        $window.WindowState='Minimized'
    }
}
function Request-ApplicationExit {
    if($script:Exiting){return}
    $script:Exiting=$true
    Hide-TrayDashboard
    $window.Close()
}
function Format-TrayNumber($Value){
    try{$n=[double]$Value}catch{$n=0}
    if([math]::Abs($n)-ge 1000000){return ('{0:0.##}M'-f($n/1000000))}
    if([math]::Abs($n)-ge 1000){return ('{0:0.#}K'-f($n/1000))}
    return ('{0:0}'-f$n)
}
function Format-TrayDuration([int64]$Seconds){
    if($Seconds-lt0){$Seconds=0}
    $span=[TimeSpan]::FromSeconds($Seconds)
    if($span.TotalDays-ge1){return ('{0}д {1:00}:{2:00}:{3:00}'-f[int]$span.TotalDays,$span.Hours,$span.Minutes,$span.Seconds)}
    return ('{0:00}:{1:00}:{2:00}'-f[int]$span.TotalHours,$span.Minutes,$span.Seconds)
}
function Show-TrayToast([string]$Text,[string]$Kind='info'){
    if(-not$script:TrayDashboard){return}
    $TrayToastText.Text=$Text
    $TrayToast.BorderBrush=Brush $(if($Kind-eq'error'){'#8A354A'}elseif($Kind-eq'ok'){'#2E7D4A'}elseif($Kind-eq'warn'){'#7A6729'}else{'#31557A'})
    $TrayToastText.Foreground=Brush $(if($Kind-eq'error'){'#FF9CAA'}elseif($Kind-eq'ok'){'#73E995'}elseif($Kind-eq'warn'){'#F0D66B'}else{'#DCEBFF'})
    $TrayToast.Visibility='Visible'
    if($script:TrayToastTimer){$script:TrayToastTimer.Stop()}
    $script:TrayToastTimer=New-Object Windows.Threading.DispatcherTimer;$script:TrayToastTimer.Interval=[TimeSpan]::FromSeconds(3.4)
    $script:TrayToastTimer.Add_Tick({$TrayToast.Visibility='Collapsed';$script:TrayToastTimer.Stop()});$script:TrayToastTimer.Start()
}
function Get-TrayGigaRows {
    if(-not$script:TrayState-or$null-eq$script:TrayState.giga){return @()}
    return @(ConvertTo-FlatArray $script:TrayState.giga)
}
function Update-TrayGigaPresentation {
    $rows=@(Get-TrayGigaRows)
    if(-not$rows.Count){$TrayGigaCard.Visibility='Collapsed';$script:TrayGigaExpiresAt=$null;return}
    $TrayGigaCard.Visibility='Visible'
    if($script:TrayGigaIndex-ge$rows.Count){$script:TrayGigaIndex=0}
    if($script:TrayGigaIndex-lt0){$script:TrayGigaIndex=0}
    $g=$rows[$script:TrayGigaIndex]
    $TrayGigaNext.Content=if($rows.Count-gt1){('{0}  {1}/{2}  ›'-f$g.name,($script:TrayGigaIndex+1),$rows.Count)}else{[string]$g.name}
    $u24=$g.usage_24h;$us=$g.usage_session
    $TrayGigaTokens.Text=((Format-TrayNumber ([int64]$u24.total_tokens))+' токенов за 24ч')
    $TrayGigaSession.Text=('· сессия '+(Format-TrayNumber ([int64]$us.total_tokens)))
    $uptime=1;if($script:TrayState.server.uptime_seconds){$uptime=[math]::Max(60,[double]$script:TrayState.server.uptime_seconds)}
    $rate=([double]$us.total_tokens)/($uptime/60)
    $TrayGigaRate.Text=('Скорость сессии: '+(Format-TrayNumber $rate)+' ток/мин')
    if($g.oauth_token_cached-and$g.oauth_token_expires_at){try{$script:TrayGigaExpiresAt=[datetime]$g.oauth_token_expires_at}catch{$script:TrayGigaExpiresAt=$null}}else{$script:TrayGigaExpiresAt=$null}
    Update-TrayCountdown
}
function Update-TrayCountdown {
    if($TrayGigaCard.Visibility-ne'Visible'){return}
    if($script:TrayGigaExpiresAt){
        $left=[int64][math]::Max(0,($script:TrayGigaExpiresAt-(Get-Date)).TotalSeconds)
        if($left-ge3600){$remainText=('{0}ч {1:00}м'-f[int]($left/3600),[int](($left%3600)/60))}else{$remainText=('{0:00}:{1:00}'-f[int]($left/60),[int]($left%60))}
        $TrayGigaCountdown.Text=('OAuth: '+$remainText)
        $TrayGigaProgress.Maximum=1800;$TrayGigaProgress.Value=[double][math]::Min(1800,[double]$left)
    }else{$TrayGigaCountdown.Text='OAuth: будет получен при запросе';$TrayGigaProgress.Value=0}
}
function Refresh-TrayDashboard {
    if(-not$script:TrayDashboard-or-not$script:TrayDashboard.IsVisible){return}
    try{
        $d=Invoke-Admin GET '/bridge/tray' $null 3;$script:TrayState=$d
        $state=[string]$d.server.status;$paused=[bool]$d.server.paused
        $ready=(-not$paused)-and($state-eq'Ready'-or$state-eq'Degraded'-or$state-eq'Starting')
        $TrayEndpoint.Text=[string]$d.server.listen;$TrayUptime.Text=('Uptime: '+(Format-TrayDuration ([int64]$d.server.uptime_seconds)))
        $TrayVersion.Text=('OmniBridge '+[string]$d.version)
        $TrayApiCount.Text=[string]$d.connections.api_active;$TrayApiSub.Text=('{0} настроено'-f$d.connections.api_enabled)
        $TrayModelCount.Text=[string]$d.models.available;$TrayAccountCount.Text=[string]$d.connections.accounts_connected;$TrayAccountSub.Text=('{0} настроено'-f$d.connections.accounts_enabled);$TrayAgentCount.Text=[string]$d.models.agent_ready
        if($paused){$TrayActiveBadge.Background=Brush '#3A1A22';$TrayActiveBadge.BorderBrush=Brush '#713044';$TrayActiveBadgeText.Text='ОСТАНОВЛЕН';$TrayActiveBadgeText.Foreground=Brush '#FF8C9A';$TrayServerBadge.Background=Brush '#3A1A22';$TrayServerBadge.BorderBrush=Brush '#713044';$TrayServerBadgeText.Text='ОСТАНОВЛЕН';$TrayServerBadgeText.Foreground=Brush '#FF8C9A';$TrayConnectionDot.Fill=Brush '#FF6868';$TrayConnectionText.Text='Gateway остановлен'}
        elseif($state-eq'Degraded'){$TrayActiveBadge.Background=Brush '#3A3012';$TrayActiveBadge.BorderBrush=Brush '#6A5820';$TrayActiveBadgeText.Text='ЧАСТИЧНО';$TrayActiveBadgeText.Foreground=Brush '#F0D259';$TrayServerBadge.Background=Brush '#3A3012';$TrayServerBadge.BorderBrush=Brush '#6A5820';$TrayServerBadgeText.Text='С ОГРАНИЧЕНИЯМИ';$TrayServerBadgeText.Foreground=Brush '#F0D259';$TrayConnectionDot.Fill=Brush '#E7C342';$TrayConnectionText.Text='Часть подключений'}
        else{$TrayActiveBadge.Background=Brush '#103B2D';$TrayActiveBadge.BorderBrush=Brush '#176044';$TrayActiveBadgeText.Text='АКТИВЕН';$TrayActiveBadgeText.Foreground=Brush '#56E68A';$TrayServerBadge.Background=Brush '#123A26';$TrayServerBadge.BorderBrush=Brush '#1F6B40';$TrayServerBadgeText.Text='ВСЁ РАБОТАЕТ';$TrayServerBadgeText.Foreground=Brush '#56E68A';$TrayConnectionDot.Fill=Brush '#45DC6D';$TrayConnectionText.Text='Подключено'}
        $TrayStartButton.IsEnabled=$paused;$TrayStopButton.IsEnabled=(-not$paused);$TrayStartSub.Text=if($paused){'Gateway остановлен'}else{'Сервер работает'}
        $s=$d.statistics_24h;$req=[int64]$s.requests;$err=[int64]$s.errors;$ok=[math]::Max(0,$req-$err)
        $TrayRequests.Text=Format-TrayNumber $req;$TrayErrors.Text=('Ошибки: '+(Format-TrayNumber $err));$TraySuccess.Text=Format-TrayNumber $ok
        $TraySuccessRate.Text=('{0:0.0}% success'-f[double]$s.success_rate);$TrayInputTokens.Text=Format-TrayNumber ([int64]$s.input_tokens);$TrayOutputTokens.Text=Format-TrayNumber ([int64]$s.output_tokens)
        $TrayAvgLatency.Text=if([double]$s.average_latency_ms-gt0){'AVG: {0:0} ms'-f[double]$s.average_latency_ms}else{'AVG: —'};$TrayP95Latency.Text=if([int64]$s.p95_latency_ms-gt0){'P95: '+[string]$s.p95_latency_ms+' ms'}else{'P95: —'}
        $TrayVSCodeSub.Text=if($d.vscode.status-eq'ready'){'Готово к синхронизации'}else{'Нет рабочих моделей'}
        if($d.refresh.running){$TrayRefreshSub.Text='Обновление…';$TrayRefreshSessions.IsEnabled=$false}else{$TrayRefreshSessions.IsEnabled=$true;if($d.refresh.error){$TrayRefreshSub.Text='Последнее обновление с ошибкой'}else{$TrayRefreshSub.Text='API и Account Connector'}}
        Update-TrayGigaPresentation
        if($script:NotifyIcon){$script:NotifyIcon.Text=if($paused){'OmniBridge · Gateway остановлен'}elseif($state-eq'Degraded'){'OmniBridge · требуется внимание'}else{'OmniBridge · всё работает'}}
    }catch{
        $TrayConnectionDot.Fill=Brush '#FF6868';$TrayConnectionText.Text='Нет соединения';$TrayServerBadgeText.Text='НЕТ СОЕДИНЕНИЯ';$TrayServerBadgeText.Foreground=Brush '#FF8C9A';$TrayStartButton.IsEnabled=$false;$TrayStopButton.IsEnabled=$false
    }
}
function Position-TrayDashboard {
    if(-not$script:TrayDashboard){return}
    try{
        $cursor=[Windows.Forms.Cursor]::Position;$screen=[Windows.Forms.Screen]::FromPoint($cursor);$wa=$screen.WorkingArea
        $helper=New-Object Windows.Interop.WindowInteropHelper -ArgumentList $script:TrayDashboard;$hwnd=$helper.Handle
        if($hwnd-eq[IntPtr]::Zero){return}
        $rect=New-Object OmniTrayNative+RECT
        if([OmniTrayNative]::GetWindowRect($hwnd,[ref]$rect)){
            $w=$rect.Right-$rect.Left;$h=$rect.Bottom-$rect.Top
            $x=$wa.Right-$w-10;$y=$wa.Bottom-$h-10
            if($x-lt$wa.Left){$x=$wa.Left+8};if($y-lt$wa.Top){$y=$wa.Top+8}
            [void][OmniTrayNative]::SetWindowPos($hwnd,[IntPtr]::Zero,$x,$y,0,0,([OmniTrayNative]::SWP_NOSIZE -bor [OmniTrayNative]::SWP_NOZORDER))
        }
    }catch{Write-Warning ('Tray position: '+$_.Exception.Message)}
}
function Show-TrayDashboard {
    if(-not$script:TrayDashboard){Show-MainWindow 'dashboard';return}
    if($script:TrayDashboard.IsVisible){Hide-TrayDashboard;return}
    $script:TrayDashboard.Opacity=0
    $script:TrayDashboard.Show();Position-TrayDashboard;$script:TrayDashboard.Opacity=1;$script:TrayDashboard.Activate()|Out-Null
    $script:TrayTick=0;Refresh-TrayDashboard;$script:TrayTimer.Start()
}
function Hide-TrayDashboard {
    if($script:TrayTimer){$script:TrayTimer.Stop()}
    if($script:TrayDashboard-and$script:TrayDashboard.IsVisible){$script:TrayDashboard.Hide()}
}
function Toggle-TrayDashboard {if($script:TrayDashboard-and$script:TrayDashboard.IsVisible){Hide-TrayDashboard}else{Show-TrayDashboard}}
function Invoke-TrayRefresh {
    try{$null=Invoke-Admin POST '/bridge/tray/refresh' @{} 3;$TrayRefreshSub.Text='Обновление…';$TrayRefreshSessions.IsEnabled=$false;Show-TrayToast 'Обновление сессий запущено.' 'ok'}catch{Show-TrayToast $_.Exception.Message 'error'}
}
function Set-TrayGateway([bool]$Run){
    try{$null=Invoke-Admin POST $(if($Run){'/bridge/tray/start'}else{'/bridge/tray/stop'}) @{} 3;Refresh-TrayDashboard;Show-TrayToast $(if($Run){'Gateway запущен.'}else{'Gateway остановлен. Панель управления остаётся активной.'}) $(if($Run){'ok'}else{'warn'})}catch{Show-TrayToast $_.Exception.Message 'error'}
}
function Check-TrayUpdates {
    $feed=Join-Path $script:Root 'data\update-feed.json'
    if(-not(Test-Path $feed)){$TrayUpdateSub.Text='Источник обновлений не настроен';Show-TrayToast 'Источник обновлений не настроен. Обновление можно выполнить через OmniBridge.exe --update.' 'info';return}
    try{
        $cfg=Get-Content -Raw -LiteralPath $feed|ConvertFrom-Json;$manifest=$null
        if($cfg.manifest_url){$manifest=Invoke-RestMethod -Uri ([string]$cfg.manifest_url) -TimeoutSec 7}elseif($cfg.version){$manifest=$cfg}else{throw 'В update-feed.json нет manifest_url или version.'}
        $current=(Get-Health).version;$latest=[string]$manifest.version
        if(-not$latest){throw 'Manifest не содержит version.'}
        if($latest-ne$current){$TrayUpdateSub.Text=('Доступна '+$latest);Show-TrayToast ('Доступна версия '+$latest+'. Установка выполняется через OmniBridge.exe --update.') 'warn'}else{$TrayUpdateSub.Text='Текущая версия актуальна';Show-TrayToast 'Установлена актуальная версия.' 'ok'}
    }catch{$TrayUpdateSub.Text='Ошибка проверки обновлений';Show-TrayToast $_.Exception.Message 'error'}
}
function Initialize-IntegratedTray {
    $notify=$null
    try{
        $notify=New-Object Windows.Forms.NotifyIcon;$notify.Icon=if($script:TrayIcon){$script:TrayIcon}else{[Drawing.SystemIcons]::Application};$notify.Text='OmniBridge · всё работает';$notify.Visible=$true
        $notify.Add_MouseClick({param($sender,$eventArgs);if($eventArgs.Button-ne[Windows.Forms.MouseButtons]::Left-and$eventArgs.Button-ne[Windows.Forms.MouseButtons]::Right){return};if($script:TrayPopupTimer){$script:TrayPopupTimer.Stop()};$script:TrayPopupTimer=New-Object Windows.Threading.DispatcherTimer;$script:TrayPopupTimer.Interval=[TimeSpan]::FromMilliseconds(180);$script:TrayPopupTimer.Add_Tick({$script:TrayPopupTimer.Stop();Toggle-TrayDashboard});$script:TrayPopupTimer.Start()})
        $notify.Add_MouseDoubleClick({if($script:TrayPopupTimer){$script:TrayPopupTimer.Stop()};Hide-TrayDashboard;Show-MainWindow 'dashboard'})
        $script:NotifyIcon=$notify;$script:TrayReady=$true
    }catch{if($notify){$notify.Visible=$false;$notify.Dispose()};$script:NotifyIcon=$null;$script:TrayReady=$false;Write-Warning ('Tray icon: '+$_.Exception.Message)}
}

$script:TrayTimer.Add_Tick({$script:TrayTick++;Update-TrayCountdown;if(($script:TrayTick%3)-eq0){Refresh-TrayDashboard}})
if($script:TrayDashboard){
    $script:TrayDashboard.Add_Deactivated({if(-not$script:Exiting){Hide-TrayDashboard}})
    $script:TrayDashboard.Add_Closing({param($sender,$e);if(-not$script:Exiting){$e.Cancel=$true;Hide-TrayDashboard}})
    $TrayGigaNext.Add_Click({$rows=@(Get-TrayGigaRows);if($rows.Count-gt1){$script:TrayGigaIndex=($script:TrayGigaIndex+1)%$rows.Count;Update-TrayGigaPresentation}})
    $TrayStartButton.Add_Click({Set-TrayGateway $true});$TrayStopButton.Add_Click({Set-TrayGateway $false});$TrayRefreshSessions.Add_Click({Invoke-TrayRefresh});$TrayCheckUpdates.Add_Click({Check-TrayUpdates})
    $TraySettingsButton.Add_Click({Show-MainWindow 'settings'});$TrayOpenDashboard.Add_Click({Show-MainWindow 'dashboard'});$TrayStatisticsButton.Add_Click({Show-MainWindow 'dashboard'});$TrayVSCodeButton.Add_Click({Show-MainWindow 'integration'});$TrayHelpButton.Add_Click({Hide-TrayDashboard;Open-Folder (Join-Path $script:Root 'docs')});$TrayLogsLink.Add_Click({Show-MainWindow 'logs'});$TrayExitButton.Add_Click({Request-ApplicationExit})
}

$TitleBar.Add_MouseLeftButtonDown({if($_.ClickCount-eq2){return};$window.DragMove()})
$BtnMinimize.Add_Click({Hide-MainWindow})
$BtnClose.Add_Click({Request-ApplicationExit})
$window.Add_StateChanged({if($window.WindowState-eq'Minimized'){Hide-MainWindow}})
$window.Add_Closing({$script:Exiting=$true})
$window.Add_Closed({
    $LoginTimer.Stop()
    $script:TrayReady=$false
    if($script:NotifyIcon){$script:NotifyIcon.Visible=$false;$script:NotifyIcon.Dispose();$script:NotifyIcon=$null}
    if($script:TrayTimer){$script:TrayTimer.Stop()}
    if($script:TrayPopupTimer){$script:TrayPopupTimer.Stop()}
    if($script:TrayToastTimer){$script:TrayToastTimer.Stop()}
    if($script:TrayDashboard){try{$script:TrayDashboard.Close()}catch{};$script:TrayDashboard=$null}
    if($script:TrayIcon){$script:TrayIcon.Dispose();$script:TrayIcon=$null}
    try{$c=Get-Config;Invoke-RestMethod -Method POST 'http://127.0.0.1:4142/control/shutdown' -Headers @{Authorization="Bearer $($c.admin_api_key)"} -TimeoutSec 2|Out-Null}catch{}
    $app.Shutdown()
})
$nav=@{NavDashboard='dashboard';NavApi='api';NavAccounts='accounts';NavModels='models';NavDoctor='doctor';NavRouter='router';NavIntegration='integration';NavServices='services';NavLogs='logs';NavSettings='settings';NavAbout='about'}
foreach($key in $nav.Keys){$control=Get-Variable -Scope Script -Name $key -ValueOnly;$target=$nav[$key];$control.Add_Click([scriptblock]::Create("Show-Page '$target'"))}
$QuickApi.Add_Click({Show-Page 'api'});$QuickAccounts.Add_Click({Show-Page 'accounts'});$QuickDoctor.Add_Click({Show-Page 'doctor'});$QuickIntegration.Add_Click({Show-Page 'integration'});$DashManageAccounts.Add_Click({Show-Page 'accounts'});$DashOpenChat.Add_Click({Show-Page 'accounts'});$DashChatMore.Add_Click({Show-Page 'accounts'});$DashOpenGemini.Add_Click({Show-Page 'accounts'});$DashGeminiMore.Add_Click({Show-Page 'accounts'});$DashAllModels.Add_Click({Show-Page 'models'})
$BtnGlobalDoctor.Add_Click({Show-Page 'doctor'})
$BtnGlobalRefresh.Add_Click({Refresh-All;Show-Toast 'Данные обновлены.' 'ok'})
$BtnGlobalSettings.Add_Click({Show-Page 'settings'})

# API events.
$ProviderPreset.Add_SelectionChanged({Apply-ProviderPreset})
$BtnProviderAdd.Add_Click({Add-Provider})
$BtnApiRefresh.Add_Click({Refresh-Catalog;Refresh-Providers})
$BtnLocalScan.Add_Click({
    try{
        Show-Toast 'Ищу локальные AI runtime…'
        $found=@(ConvertTo-FlatArray (Invoke-Admin GET '/bridge/local-scan' $null 15)|Where-Object{$_.kind-and$_.name-and$_.base_url-and[int]$_.models-gt0})
        $added=@();foreach($x in $found){try{$null=Invoke-Admin POST '/bridge/providers' @{kind=$x.kind;name=$x.name;base_url=$x.base_url;api_key=''} 40;$added+=('{0}: {1} моделей · {2}'-f$x.name,[int]$x.models,$x.base_url)}catch{}}
        Refresh-Providers;Refresh-Models;Refresh-Dashboard
        if($added.Count){Show-Toast ('Добавлен локальный AI — '+($added-join'; ')) 'ok'}else{Show-Toast 'Работающие локальные AI с загруженными моделями не обнаружены. Проверены Ollama, LM Studio, vLLM и LocalAI.' 'warn'}
    }catch{Show-Toast $_.Exception.Message 'error'}
})
$BtnProviderRefresh.Add_Click({
    $r=$ProvidersList.SelectedItem
    if($r){try{$null=Invoke-Admin POST ("/bridge/providers/$($r.raw.id)/refresh") @{} 90;Refresh-Providers;Refresh-Models;Show-Toast 'Модели обновлены.' 'ok'}catch{Show-Toast $_.Exception.Message 'error'}}
})
$BtnProviderDelete.Add_Click({
    $r=$ProvidersList.SelectedItem
    if($r -and [Windows.MessageBox]::Show("Удалить подключение '$($r.name)'?",'OmniBridge','YesNo','Question') -eq 'Yes'){
        try{$null=Invoke-Admin DELETE ("/bridge/providers/$($r.raw.id)");Refresh-Providers;Refresh-Models;Refresh-Dashboard;Show-Toast 'Подключение удалено.' 'ok'}catch{Show-Toast $_.Exception.Message 'error'}
    }
})

# Account events.
$BtnBrowseCodex.Add_Click({$d=New-Object Windows.Forms.OpenFileDialog;$d.Filter='Codex executable (*.exe;*.cmd;*.bat)|*.exe;*.cmd;*.bat|Все файлы (*.*)|*.*';if($d.ShowDialog()-eq'OK'){$CodexPath.Text=$d.FileName}});$BtnBrowseGemini.Add_Click({$d=New-Object Windows.Forms.OpenFileDialog;$d.Filter='Google OAuth JSON (*.json)|*.json|Все файлы (*.*)|*.*';if($d.ShowDialog()-eq'OK'){$GeminiClientPath.Text=$d.FileName}})
$BtnChatLogin.Add_Click({Start-AccountLogin 'chatgpt-codex' $false});$BtnChatDevice.Add_Click({Start-AccountLogin 'chatgpt-codex' $true});$BtnGeminiLogin.Add_Click({Start-AccountLogin 'gemini-developer-oauth' $false});$BtnAccountsRefresh.Add_Click({Refresh-Accounts})
$BtnAccountRefresh.Add_Click({$a=Selected-Account;if($a){try{$null=Invoke-Admin POST ("/bridge/accounts/$($a.id)/refresh") @{} 90;Refresh-Accounts;Refresh-Models;Refresh-Dashboard;Show-Toast 'Аккаунт обновлён.' 'ok'}catch{Show-Toast $_.Exception.Message 'error'}}})
$BtnAccountInfo.Add_Click({$a=Selected-Account;if($a){try{$i=Invoke-Admin GET ("/bridge/accounts/$($a.id)/info");$u=Invoke-Admin GET ("/bridge/accounts/$($a.id)/usage");[Windows.MessageBox]::Show(("Аккаунт: {0}`nСтатус: {1}`nПлан: {2}`nЛимиты: {3}" -f $i.email,$i.status,$i.plan,$u.status),'OmniBridge')|Out-Null}catch{Show-Toast $_.Exception.Message 'error'}}})
$BtnAccountLogout.Add_Click({$a=Selected-Account;if($a){try{$null=Invoke-Admin POST ("/bridge/accounts/$($a.id)/logout") @{};Refresh-Accounts;Refresh-Dashboard;Show-Toast 'Выполнен выход из аккаунта.' 'ok'}catch{Show-Toast $_.Exception.Message 'error'}}})

# Models, Doctor, Integration, Logs, Settings.
$ModelsSearch.Add_TextChanged({$ModelsSearchPlaceholder.Visibility=if($ModelsSearch.Text){'Collapsed'}else{'Visible'};Apply-ModelFilter});$ModelsProviderFilter.Add_SelectionChanged({Apply-ModelFilter});$ModelsSourceFilter.Add_SelectionChanged({Apply-ModelFilter});$ModelsSort.Add_SelectionChanged({Apply-ModelFilter});$BtnModelsRefresh.Add_Click({Refresh-Models});$ModelsList.Add_SelectionChanged({Update-ModelDetail});$BtnProbeSelectedModel.Add_Click({Probe-SelectedModel})
$BtnDoctorStart.Add_Click({Start-Doctor});$BtnDoctorReportRefresh.Add_Click({Refresh-DoctorReport})
$BtnRouterRefresh.Add_Click({Refresh-RouterUI});$RouterProfile.Add_SelectionChanged({if($script:CurrentPage-eq'router'){Refresh-RouterUI}});$BtnRouterEvaluate.Add_Click({Invoke-RouterDryRun})
$BtnVSSync.Add_Click({try{Sync-VSCode;$VSStatus.Text='Синхронизировано · перезапустите VS Code';$VSLastSync.Text=('Последняя синхронизация: '+(Get-Date -Format 'HH:mm:ss'));Show-Toast 'Синхронизировано. Перезапустите VS Code; при первом добавлении включите 👁 в Manage Models.' 'ok'}catch{Show-Toast $_.Exception.Message 'error'}})
$BtnVSOpen.Add_Click({Open-Folder (Join-Path $env:APPDATA 'Code\User')});$BtnVSRefresh.Add_Click({Refresh-Integration});$BtnIntegrationHubRefresh.Add_Click({Refresh-Integration})
$BtnCopyEndpoint.Add_Click({try{$c=Get-Config;$value=('http://'+$c.listen+'/v1');[Windows.Clipboard]::SetText($value);Show-Toast 'Endpoint скопирован в буфер обмена.' 'ok'}catch{Show-Toast $_.Exception.Message 'error'}})
$BtnCopyBridgeKey.Add_Click({try{$c=Get-Config;[Windows.Clipboard]::SetText([string]$c.data_api_key);Show-Toast 'Local Bridge Key скопирован. Буфер обмена содержит секрет.' 'warn'}catch{Show-Toast $_.Exception.Message 'error'}})
$BtnClineTest.Add_Click({
    try{
        $catalog=Invoke-Data GET '/v1/models' $null 8;$items=@(ConvertTo-FlatArray $catalog.data)
        $ClineStatus.Text=('Cline API: {0} моделей доступно' -f $items.Count);$ClineStatus.Foreground=Brush $(if($items.Count-gt0){'#64EA88'}else{'#F0D259'})
        if($items.Count-gt0){Show-Toast ('Cline совместимый /v1/models отвечает. Моделей: '+$items.Count) 'ok'}else{Show-Toast 'Gateway отвечает, но Model Registry пока пуст.' 'warn'}
    }catch{$ClineStatus.Text='Cline API: ошибка';$ClineStatus.Foreground=Brush '#FF8290';Show-Toast ('Cline API: '+$_.Exception.Message) 'error'}
})
$BtnServicesRefresh.Add_Click({Refresh-Services})
$BtnQuotaRefresh.Add_Click({try{$null=Invoke-Admin POST '/bridge/quota-cost/refresh' @{} 90;Refresh-Services;Show-Toast 'Квоты обновлены из доступных официальных источников.' 'ok'}catch{Show-Toast $_.Exception.Message 'error'}})
$BtnContextToggle.Add_Click({try{$g=Invoke-Admin GET '/bridge/context-guard';$c=$g.config;$mode=if($c.mode){[string]$c.mode}else{'compact'};if($mode-eq'off'){$mode='compact'};$body=@{enabled=(-not[bool]$c.enabled);mode=$mode;max_context_percent=[double]$c.max_context_percent;reserve_output_tokens=[int]$c.reserve_output_tokens;min_recent_messages=[int]$c.min_recent_messages;max_request_bytes=[int]$c.max_request_bytes};$null=Invoke-Admin PUT '/bridge/context-guard' $body;Refresh-Services;Show-Toast 'Context Guard обновлён.' 'ok'}catch{Show-Toast $_.Exception.Message 'error'}})
$BtnMCPProbeAll.Add_Click({try{$null=Invoke-Admin POST '/bridge/mcp/probe-all' @{} 30;Refresh-Services;Show-Toast 'Проверка MCP завершена.' 'ok'}catch{Show-Toast $_.Exception.Message 'error'}})
$BtnOpenLogsFromServices.Add_Click({Show-Page 'logs'});$BtnOpenIntegrationFromServices.Add_Click({Show-Page 'integration'})
$BtnLogsRefresh.Add_Click({Refresh-Logs});$BtnLogsFolder.Add_Click({Open-Folder (Join-Path $script:Root 'logs')});$LogsSearch.Add_TextChanged({$LogsSearchPlaceholder.Visibility=if($LogsSearch.Text){'Collapsed'}else{'Visible'};Apply-LogFilter});$LogsLevel.Add_SelectionChanged({Apply-LogFilter})
$BtnRestartServer.Add_Click({Restart-Server});$BtnOpenData.Add_Click({Open-Folder (Join-Path $script:Root 'data')});$BtnOpenRoot.Add_Click({Open-Folder $script:Root});$BtnBackup.Add_Click({Create-Backup});$BtnRouteSave.Add_Click({Save-Route})
$BtnRouteDelete.Add_Click({
    $r=$RoutesList.SelectedItem
    if($r){try{$aliases=@{};foreach($p in $script:RouteConfig.aliases.psobject.Properties){if($p.Name -ne $r.alias){$aliases[$p.Name]=[string]$p.Value}};$priorities=Copy-RouteArrayMap $script:RouteConfig.priorities;$fallbacks=Copy-RouteArrayMap $script:RouteConfig.fallbacks;$policies=Copy-PolicyMap $script:RouteConfig.policies;$priorities.Remove($r.alias);$fallbacks.Remove($r.alias);$policies.Remove($r.alias);$maxRetries=if($null-ne$script:RouteConfig.max_retries){[int]$script:RouteConfig.max_retries}else{2};$script:RouteConfig=Invoke-Admin PUT '/bridge/router' @{aliases=$aliases;priorities=$priorities;fallbacks=$fallbacks;policies=$policies;max_retries=$maxRetries};Refresh-Routes}catch{Show-Toast $_.Exception.Message 'error'}}
})

try{Ensure-Bridge|Out-Null;Refresh-Catalog;Refresh-All}catch{Show-Toast $_.Exception.Message 'error'}
$start=$Page.ToLower();if($start-eq'vscode'){$start='integration'};Show-Page $start
Initialize-IntegratedTray
$window.Show()
[void]$app.Run()
