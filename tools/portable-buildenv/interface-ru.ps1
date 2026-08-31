#requires -version 5.1
param()

Set-StrictMode -Version 2
$ErrorActionPreference = 'Stop'
$Root = $PSScriptRoot
if (Test-Path (Join-Path $Root 'lib\common.ps1')) { . (Join-Path $Root 'lib\common.ps1') }

Add-Type -AssemblyName PresentationFramework
Add-Type -AssemblyName PresentationCore
Add-Type -AssemblyName WindowsBase
Add-Type -AssemblyName System.Windows.Forms

[xml]$xaml = @"
<Window xmlns="http://schemas.microsoft.com/winfx/2006/xaml/presentation"
        xmlns:x="http://schemas.microsoft.com/winfx/2006/xaml"
        Title="OmniBridge — переносная среда сборки"
        Width="1160" Height="790" MinWidth="1000" MinHeight="700"
        WindowStartupLocation="CenterScreen" Background="#0F1115" Foreground="#F4F6F8"
        FontFamily="Segoe UI">
  <Window.Resources>
    <Style TargetType="Button">
      <Setter Property="Background" Value="#252A32"/><Setter Property="Foreground" Value="#F7F8FA"/>
      <Setter Property="BorderBrush" Value="#39414D"/><Setter Property="BorderThickness" Value="1"/>
      <Setter Property="Padding" Value="14,9"/><Setter Property="Margin" Value="0,4,8,4"/>
      <Setter Property="Cursor" Value="Hand"/>
    </Style>
    <Style x:Key="Primary" TargetType="Button">
      <Setter Property="Background" Value="#2C67D7"/><Setter Property="BorderBrush" Value="#4B7EE0"/>
      <Setter Property="Foreground" Value="White"/><Setter Property="FontWeight" Value="SemiBold"/>
      <Setter Property="Padding" Value="14,9"/><Setter Property="Margin" Value="0,4,8,4"/>
    </Style>
    <Style x:Key="Card" TargetType="Border">
      <Setter Property="Background" Value="#171A20"/><Setter Property="BorderBrush" Value="#2A303A"/>
      <Setter Property="BorderThickness" Value="1"/><Setter Property="CornerRadius" Value="10"/>
      <Setter Property="Padding" Value="16"/><Setter Property="Margin" Value="0,0,0,12"/>
    </Style>
  </Window.Resources>

  <Grid Margin="18">
    <Grid.RowDefinitions><RowDefinition Height="Auto"/><RowDefinition Height="Auto"/><RowDefinition Height="*"/><RowDefinition Height="Auto"/></Grid.RowDefinitions>

    <Grid Grid.Row="0" Margin="0,0,0,14">
      <Grid.ColumnDefinitions><ColumnDefinition Width="*"/><ColumnDefinition Width="Auto"/></Grid.ColumnDefinitions>
      <StackPanel>
        <TextBlock Text="OmniBridge" FontSize="28" FontWeight="SemiBold"/>
        <TextBlock Text="Переносная локальная среда сборки • без зависимости от GitHub Actions" Foreground="#AEB6C2" FontSize="14" Margin="0,4,0,0"/>
      </StackPanel>
      <StackPanel Grid.Column="1" Orientation="Horizontal" VerticalAlignment="Center">
        <Button x:Name="BtnDocs" Content="Подробная документация" ToolTip="Открыть локальное русское руководство."/>
        <Button x:Name="BtnRefresh" Content="Обновить статусы" ToolTip="Повторно проверить инструменты, исходники, отчёты и релизы."/>
      </StackPanel>
    </Grid>

    <Border Grid.Row="1" Style="{StaticResource Card}">
      <Grid>
        <Grid.ColumnDefinitions><ColumnDefinition Width="*"/><ColumnDefinition Width="*"/><ColumnDefinition Width="*"/><ColumnDefinition Width="*"/></Grid.ColumnDefinitions>
        <StackPanel Grid.Column="0"><TextBlock Text="Инструменты" Foreground="#AEB6C2"/><TextBlock x:Name="TxtTools" Text="Проверка..." FontWeight="SemiBold" Margin="0,5,0,0"/></StackPanel>
        <StackPanel Grid.Column="1"><TextBlock Text="Исходники" Foreground="#AEB6C2"/><TextBlock x:Name="TxtSource" Text="Проверка..." FontWeight="SemiBold" Margin="0,5,0,0"/></StackPanel>
        <StackPanel Grid.Column="2"><TextBlock Text="Последняя проверка" Foreground="#AEB6C2"/><TextBlock x:Name="TxtValidation" Text="Нет данных" FontWeight="SemiBold" Margin="0,5,0,0"/></StackPanel>
        <StackPanel Grid.Column="3"><TextBlock Text="Последний релиз" Foreground="#AEB6C2"/><TextBlock x:Name="TxtRelease" Text="Не собран" FontWeight="SemiBold" Margin="0,5,0,0"/></StackPanel>
      </Grid>
    </Border>

    <Grid Grid.Row="2">
      <Grid.ColumnDefinitions><ColumnDefinition Width="1.2*"/><ColumnDefinition Width="0.9*"/></Grid.ColumnDefinitions>
      <ScrollViewer Grid.Column="0" VerticalScrollBarVisibility="Auto" Margin="0,0,12,0">
        <StackPanel>
          <Border Style="{StaticResource Card}"><StackPanel>
            <TextBlock Text="1. Развернуть инструменты" FontSize="18" FontWeight="SemiBold"/>
            <TextBlock TextWrapping="Wrap" Foreground="#AEB6C2" Margin="0,6,0,8" Text="Скачивает закреплённые Go, PowerShell, LLVM-MinGW, rsrc и govulncheck, проверяет SHA256 и хранит всё внутри переносной папки."/>
            <WrapPanel><Button x:Name="BtnBootstrap" Style="{StaticResource Primary}" Content="Скачать и развернуть" ToolTip="Нужен интернет только для первого bootstrap."/><Button x:Name="BtnDoctor" Content="Проверить среду" ToolTip="Проверить Windows x64, toolchain, WPF, PowerShell 5.1, race compiler и исходники."/></WrapPanel>
          </StackPanel></Border>

          <Border Style="{StaticResource Card}"><StackPanel>
            <TextBlock Text="2. Получить исходники OmniBridge" FontSize="18" FontWeight="SemiBold"/>
            <TextBlock TextWrapping="Wrap" Foreground="#AEB6C2" Margin="0,6,0,8" Text="Git не нужен. Текущий snapshot private ветки скачивается через GitHub REST API. PAT используется только дочерним процессом и не сохраняется."/>
            <TextBlock Text="GitHub PAT (read-only):" Foreground="#CDD3DC"/>
            <PasswordBox x:Name="PatBox" Margin="0,5,0,7" Background="#11151B" Foreground="White" BorderBrush="#39414D" Padding="8" ToolTip="Fine-grained PAT: доступ только к OmniBridge, Contents: Read."/>
            <WrapPanel><Button x:Name="BtnSync" Style="{StaticResource Primary}" Content="Скачать текущую ветку"/><Button x:Name="BtnImportFolder" Content="Импортировать папку"/><Button x:Name="BtnImportZip" Content="Импортировать ZIP"/><Button x:Name="BtnFallback" Content="Восстановить RC7" ToolTip="Аварийный проверенный RC7 snapshot 436eec3."/></WrapPanel>
          </StackPanel></Border>

          <Border Style="{StaticResource Card}"><StackPanel>
            <TextBlock Text="3. Проверить код" FontSize="18" FontWeight="SemiBold"/>
            <TextBlock TextWrapping="Wrap" Foreground="#AEB6C2" Margin="0,6,0,8" Text="FAST — ежедневный цикл. FULL — локальный аналог CI: race detector, govulncheck, UI/XAML, PS5.1, DPAPI и Windows compile."/>
            <WrapPanel><Button x:Name="BtnFast" Style="{StaticResource Primary}" Content="FAST-проверка" ToolTip="gofmt + go test ./... + go vet ./..."/><Button x:Name="BtnFull" Content="FULL + govulncheck" ToolTip="Полный online acceptance."/><Button x:Name="BtnFullOffline" Content="FULL офлайн" ToolTip="Без свежего сетевого vulnerability scan."/></WrapPanel>
          </StackPanel></Border>

          <Border Style="{StaticResource Card}"><StackPanel>
            <TextBlock Text="4. Собрать приложение" FontSize="18" FontWeight="SemiBold"/>
            <TextBlock TextWrapping="Wrap" Foreground="#AEB6C2" Margin="0,6,0,8" Text="Быстрая сборка создаёт EXE для smoke. LOCAL RELEASE формирует Core, Setup, ZIP, SHA256, SBOM, provenance и snapshot исходников."/>
            <WrapPanel><Button x:Name="BtnQuickBuild" Content="Быстрая сборка"/><Button x:Name="BtnRelease" Style="{StaticResource Primary}" Content="LOCAL RELEASE"/><Button x:Name="BtnReleaseOffline" Content="LOCAL RELEASE офлайн"/></WrapPanel>
          </StackPanel></Border>

          <Border Style="{StaticResource Card}"><StackPanel>
            <TextBlock Text="5. Разработка и перенос" FontSize="18" FontWeight="SemiBold"/>
            <TextBlock TextWrapping="Wrap" Foreground="#AEB6C2" Margin="0,6,0,8" Text="DEV Shell использует только локальные GOROOT/GOPATH/GOCACHE/GOMODCACHE/CC/CXX. OFFLINE ZIP упаковывает toolchain и source snapshot для другого ПК."/>
            <WrapPanel><Button x:Name="BtnDevShell" Content="Открыть DEV Shell"/><Button x:Name="BtnPackOffline" Content="Создать OFFLINE ZIP"/><Button x:Name="BtnOpenSource" Content="Исходники"/><Button x:Name="BtnOpenDist" Content="Релизы"/><Button x:Name="BtnOpenReports" Content="Отчёты"/></WrapPanel>
          </StackPanel></Border>
        </StackPanel>
      </ScrollViewer>

      <Border Grid.Column="1" Style="{StaticResource Card}">
        <Grid><Grid.RowDefinitions><RowDefinition Height="Auto"/><RowDefinition Height="Auto"/><RowDefinition Height="*"/><RowDefinition Height="Auto"/></Grid.RowDefinitions>
          <TextBlock Grid.Row="0" Text="Помощник по разворачиванию" FontSize="18" FontWeight="SemiBold"/>
          <Border Grid.Row="1" Background="#12151A" CornerRadius="8" Padding="12" Margin="0,10,0,10"><StackPanel>
            <TextBlock Text="Рекомендуемый порядок" FontWeight="SemiBold"/>
            <TextBlock Foreground="#B9C1CC" Margin="0,6,0,0" Text="1 → Скачать инструменты&#x0a;2 → Получить исходники&#x0a;3 → Doctor&#x0a;4 → FAST&#x0a;5 → FULL&#x0a;6 → LOCAL RELEASE"/>
          </StackPanel></Border>
          <ScrollViewer Grid.Row="2" VerticalScrollBarVisibility="Auto"><TextBlock TextWrapping="Wrap" Foreground="#AEB6C2" Text="GitHub Actions для локальной сборки не нужен.&#x0a;&#x0a;Git также не обязателен.&#x0a;&#x0a;После bootstrap почти весь цикл работает без сети.&#x0a;&#x0a;Свежий govulncheck обычно требует интернет.&#x0a;&#x0a;PowerShell 5.1 и WPF берутся из Windows.&#x0a;&#x0a;PAT не сохраняется. Не помещайте секреты в source, reports или snapshots.&#x0a;&#x0a;FULL и RELEASE могут выполняться заметно дольше FAST.&#x0a;&#x0a;Долгие операции открываются в отдельном PowerShell-окне, поэтому GUI не зависает."/></ScrollViewer>
          <StackPanel Grid.Row="3" Margin="0,12,0,0"><TextBlock x:Name="TxtTask" Text="Нет активных задач." Foreground="#C8CFD8" TextWrapping="Wrap"/><ProgressBar x:Name="Progress" Height="5" Margin="0,7,0,0"/></StackPanel>
        </Grid>
      </Border>
    </Grid>

    <Grid Grid.Row="3" Margin="0,12,0,0"><Grid.ColumnDefinitions><ColumnDefinition Width="*"/><ColumnDefinition Width="Auto"/></Grid.ColumnDefinitions>
      <TextBlock x:Name="TxtFooter" Foreground="#7F8996" Text="Наведите курсор на кнопки — у основных действий есть подсказки."/>
      <Button Grid.Column="1" x:Name="BtnConsole" Content="Резервное консольное меню"/>
    </Grid>
  </Grid>
</Window>
"@

$reader = New-Object System.Xml.XmlNodeReader $xaml
$Window = [Windows.Markup.XamlReader]::Load($reader)
function C([string]$n) { $Window.FindName($n) }
$PatBox=C 'PatBox'; $TxtTools=C 'TxtTools'; $TxtSource=C 'TxtSource'; $TxtValidation=C 'TxtValidation'; $TxtRelease=C 'TxtRelease'; $TxtTask=C 'TxtTask'; $Progress=C 'Progress'; $TxtFooter=C 'TxtFooter'
$script:Running=@{}

function Refresh-Status {
    $ready=(Test-Path (Join-Path $Root 'tools\go\bin\go.exe')) -and (Test-Path (Join-Path $Root 'tools\pwsh\pwsh.exe')) -and (Test-Path (Join-Path $Root 'tools\bin\rsrc.exe'))
    $TxtTools.Text=if($ready){'Готовы'}else{'Не развёрнуты'}
    $source=Join-Path $Root 'source\OmniBridge'
    $TxtSource.Text=if(Test-Path (Join-Path $source 'go.mod')){'Готовы'}else{'Не загружены'}
    $r=if(Test-Path (Join-Path $Root 'reports')){Get-ChildItem (Join-Path $Root 'reports') -Filter 'validation-*.txt' -File|Sort-Object LastWriteTime -Descending|Select-Object -First 1}
    $TxtValidation.Text=if($r){$r.LastWriteTime.ToString('dd.MM HH:mm')}else{'Ещё не запускалась'}
    $z=if(Test-Path (Join-Path $Root 'dist')){Get-ChildItem (Join-Path $Root 'dist') -Filter 'OmniBridge_*_Windows_x64.zip' -File|Sort-Object LastWriteTime -Descending|Select-Object -First 1}
    $TxtRelease.Text=if($z){$z.LastWriteTime.ToString('dd.MM HH:mm')}else{'Не собран'}
}

function Q([string]$s){'"'+($s -replace '"','\"')+'"'}
function Run-Backend([string]$Title,[string]$Script,[string[]]$Args=@(),[hashtable]$Env=@{}){
    if(-not(Test-Path $Script)){[Windows.MessageBox]::Show("Не найден backend-скрипт:`n$Script",'OmniBridge')|Out-Null;return}
    $psi=New-Object Diagnostics.ProcessStartInfo
    $psi.FileName="$env:SystemRoot\System32\WindowsPowerShell\v1.0\powershell.exe"
    $a=@('-NoLogo','-NoProfile','-ExecutionPolicy','Bypass','-File',(Q $Script));foreach($x in $Args){$a+=(Q $x)}
    $psi.Arguments=$a -join ' ';$psi.WorkingDirectory=$Root;$psi.UseShellExecute=$false
    foreach($k in $Env.Keys){$psi.EnvironmentVariables[$k]=[string]$Env[$k]}
    $p=New-Object Diagnostics.Process;$p.StartInfo=$psi;if($p.Start()){$script:Running[$p.Id]=@{p=$p;t=$Title};$TxtTask.Text="Выполняется: $Title";$Progress.IsIndeterminate=$true}
}
function Open-Folder([string]$p){if(-not(Test-Path $p)){New-Item -ItemType Directory -Force -Path $p|Out-Null};Start-Process explorer.exe $p}

(C 'BtnBootstrap').Add_Click({Run-Backend 'Разворачивание инструментов' (Join-Path $Root 'bootstrap.ps1')})
(C 'BtnDoctor').Add_Click({Run-Backend 'Диагностика среды' (Join-Path $Root 'doctor.ps1')})
(C 'BtnSync').Add_Click({$e=@{};if($PatBox.Password){$e['OMNIBRIDGE_GITHUB_TOKEN']=$PatBox.Password};Run-Backend 'Загрузка исходников' (Join-Path $Root 'sync-source.ps1') @() $e;$PatBox.Clear()})
(C 'BtnImportFolder').Add_Click({$d=New-Object Windows.Forms.FolderBrowserDialog;$d.Description='Выберите папку OmniBridge';if($d.ShowDialog() -eq 'OK'){Run-Backend 'Импорт папки' (Join-Path $Root 'sync-source.ps1') @('-FromFolder',$d.SelectedPath)}})
(C 'BtnImportZip').Add_Click({$d=New-Object Microsoft.Win32.OpenFileDialog;$d.Filter='ZIP (*.zip)|*.zip';if($d.ShowDialog()){Run-Backend 'Импорт ZIP' (Join-Path $Root 'sync-source.ps1') @('-FromZip',$d.FileName)}})
(C 'BtnFallback').Add_Click({if([Windows.MessageBox]::Show('Восстановить встроенный RC7 snapshot 436eec3?','OmniBridge','YesNo','Warning') -eq 'Yes'){Run-Backend 'RC7 fallback' (Join-Path $Root 'restore-fallback.ps1')}})
(C 'BtnFast').Add_Click({Run-Backend 'FAST-проверка' (Join-Path $Root 'validate.ps1') @('-Mode','Fast')})
(C 'BtnFull').Add_Click({Run-Backend 'FULL + govulncheck' (Join-Path $Root 'validate.ps1') @('-Mode','Full','-OnlineVuln')})
(C 'BtnFullOffline').Add_Click({Run-Backend 'FULL офлайн' (Join-Path $Root 'validate.ps1') @('-Mode','Full')})
(C 'BtnQuickBuild').Add_Click({Run-Backend 'Быстрая Windows сборка' (Join-Path $Root 'build.ps1') @('-Quick')})
(C 'BtnRelease').Add_Click({Run-Backend 'LOCAL RELEASE' (Join-Path $Root 'release.ps1') @('-SnapshotSource')})
(C 'BtnReleaseOffline').Add_Click({Run-Backend 'LOCAL RELEASE офлайн' (Join-Path $Root 'release.ps1') @('-Offline','-SnapshotSource')})
(C 'BtnDevShell').Add_Click({Run-Backend 'DEV Shell' (Join-Path $Root 'dev-shell.ps1')})
(C 'BtnPackOffline').Add_Click({Run-Backend 'Упаковка OFFLINE ZIP' (Join-Path $Root 'pack-offline.ps1')})
(C 'BtnOpenSource').Add_Click({Open-Folder (Join-Path $Root 'source\OmniBridge')});(C 'BtnOpenDist').Add_Click({Open-Folder (Join-Path $Root 'dist')});(C 'BtnOpenReports').Add_Click({Open-Folder (Join-Path $Root 'reports')})
(C 'BtnDocs').Add_Click({$p=Join-Path $Root 'docs\РУКОВОДСТВО_ПО_СРЕДЕ.html';if(Test-Path $p){Start-Process $p}else{Start-Process (Join-Path $Root 'README_RU.md')}})
(C 'BtnRefresh').Add_Click({Refresh-Status});(C 'BtnConsole').Add_Click({Run-Backend 'Консольное меню' (Join-Path $Root 'menu.ps1')})

$timer=New-Object Windows.Threading.DispatcherTimer;$timer.Interval=[TimeSpan]::FromSeconds(1)
$timer.Add_Tick({$done=@();foreach($id in @($script:Running.Keys)){$x=$script:Running[$id];if($x.p.HasExited){$done+=$id;$TxtFooter.Text=if($x.p.ExitCode -eq 0){'Готово: '+$x.t}else{'Ошибка '+$x.p.ExitCode+': '+$x.t}}};foreach($id in $done){$script:Running.Remove($id)};if($script:Running.Count -eq 0){$Progress.IsIndeterminate=$false;$TxtTask.Text='Нет активных задач.';Refresh-Status}});$timer.Start()
$Window.Add_Closed({$timer.Stop()});Refresh-Status;[void]$Window.ShowDialog()
