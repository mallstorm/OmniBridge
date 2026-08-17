param([string]$Root)
$ErrorActionPreference='Stop'
Add-Type -AssemblyName PresentationFramework
if(-not $Root){$Root=Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)}

$tokens=$null;$errors=$null
[void][Management.Automation.Language.Parser]::ParseFile((Join-Path $Root 'scripts\desktop.ps1'),[ref]$tokens,[ref]$errors)
if($errors.Count){throw (($errors|ForEach-Object{"$($_.Extent.StartLineNumber): $($_.Message)"}) -join [Environment]::NewLine)}

$app=[Windows.Application]::Current
if($null-eq$app){$app=New-Object Windows.Application}
$utf8=[Text.Encoding]::UTF8
$theme=[Windows.Markup.XamlReader]::Parse([IO.File]::ReadAllText((Join-Path $Root 'ui\Theme.xaml'),$utf8))
$app.Resources.MergedDictionaries.Add($theme)
$files=@((Join-Path $Root 'ui\Shell.xaml'))+@(Get-ChildItem (Join-Path $Root 'ui\pages') -Filter '*.xaml'|ForEach-Object{$_.FullName})
foreach($file in $files){[void][Windows.Markup.XamlReader]::Parse([IO.File]::ReadAllText($file,$utf8))}
Write-Host ('UI validation passed: PowerShell syntax + {0} XAML views.' -f $files.Count) -ForegroundColor Green
