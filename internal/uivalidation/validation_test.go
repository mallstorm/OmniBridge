package uivalidation

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func projectRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestNewPresentationLayerIsWellFormed(t *testing.T) {
	root := projectRoot(t)
	files := []string{filepath.Join(root, "ui", "Theme.xaml"), filepath.Join(root, "ui", "Shell.xaml"), filepath.Join(root, "ui", "TrayDashboard.xaml")}
	pages, err := filepath.Glob(filepath.Join(root, "ui", "pages", "*.xaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 11 {
		t.Fatalf("presentation layer pages=%d want 11", len(pages))
	}
	files = append(files, pages...)
	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		err = xml.NewDecoder(f).Decode(new(any))
		_ = f.Close()
		if err != nil {
			t.Fatalf("%s is not well-formed XML: %v", filepath.Base(path), err)
		}
	}
}

func TestStaticResourcesAndFixedShell(t *testing.T) {
	root := projectRoot(t)
	keyRE := regexp.MustCompile(`x:Key="([A-Za-z0-9_]+)"`)
	keys := map[string]bool{}
	refRE := regexp.MustCompile(`\{StaticResource\s+([A-Za-z0-9_]+)\}`)
	files, _ := filepath.Glob(filepath.Join(root, "ui", "*.xaml"))
	pages, _ := filepath.Glob(filepath.Join(root, "ui", "pages", "*.xaml"))
	files = append(files, pages...)
	for _, path := range files {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range keyRE.FindAllStringSubmatch(string(b), -1) {
			keys[m[1]] = true
		}
	}
	for _, path := range files {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range refRE.FindAllStringSubmatch(string(b), -1) {
			if !keys[m[1]] {
				t.Errorf("%s references missing resource %q", filepath.Base(path), m[1])
			}
		}
	}
	shell, _ := os.ReadFile(filepath.Join(root, "ui", "Shell.xaml"))
	text := string(shell)
	for _, required := range []string{`Width="1260"`, `Height="740"`, `ResizeMode="NoResize"`, `x:Name="PageHost"`, `x:Name="NavRouter"`, `x:Name="NavServices"`} {
		if !strings.Contains(text, required) {
			t.Errorf("Shell.xaml missing %s", required)
		}
	}
}

func TestLegacyEmbeddedXAMLWasRemoved(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(projectRoot(t), "scripts", "desktop.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if strings.Contains(text, "[xml]$xaml") || strings.Contains(text, "<Window xmlns=") {
		t.Fatal("desktop controller still embeds the legacy monolithic XAML")
	}
	for _, required := range []string{"ui\\Theme.xaml", "ui\\Shell.xaml", "ui\\pages\\"} {
		if !strings.Contains(text, required) {
			t.Errorf("desktop controller does not load %s", required)
		}
	}
}

func TestReferencePolishRegressions(t *testing.T) {
	root := projectRoot(t)
	themeBytes, err := os.ReadFile(filepath.Join(root, "ui", "Theme.xaml"))
	if err != nil {
		t.Fatal(err)
	}
	theme := string(themeBytes)
	for _, required := range []string{
		`<Color x:Key="ColorSidebar">#07172A</Color>`,
		`x:Key="HeadingBrush" Color="#FFFFFF"`,
		`x:Key="PageTitle" TargetType="TextBlock" BasedOn="{StaticResource {x:Type TextBlock}}"`,
		`<Setter Property="FontSize" Value="22"/>`,
		`x:Key="SectionTitle" TargetType="TextBlock" BasedOn="{StaticResource {x:Type TextBlock}}"`,
		`<Setter Property="FontSize" Value="15"/>`,
		`<ControlTemplate TargetType="ComboBox">`,
		`<ControlTemplate TargetType="TextBox">`,
		`<ControlTemplate TargetType="ScrollBar">`,
		`<Setter Property="Padding" Value="10,0"/>`,
		`<Setter Property="VerticalContentAlignment" Value="Center"/>`,
		`<Setter Property="ItemsPanel">`,
		`<VirtualizingStackPanel Orientation="Vertical"/>`,
	} {
		if !strings.Contains(theme, required) {
			t.Errorf("Theme.xaml missing reference-polish contract %s", required)
		}
	}

	emptyStates := map[string][]string{
		"ApiKeys.xaml":  {`x:Name="ProvidersEmpty"`},
		"Accounts.xaml": {`x:Name="AccountsEmpty"`},
		"Models.xaml":   {`x:Name="ModelsEmpty"`, `x:Name="ModelsSearchPlaceholder"`},
		"Doctor.xaml":   {`x:Name="DoctorEmpty"`},
		"Logs.xaml":     {`x:Name="LogsEmpty"`, `x:Name="LogsSearchPlaceholder"`},
		"Settings.xaml": {`x:Name="RoutesEmpty"`, `TextTrimming="CharacterEllipsis"`},
		"Router.xaml":   {`x:Name="RouterCandidates"`, `x:Name="RouterPoolsList"`, `x:Name="BtnRouterEvaluate"`, `x:Name="RouterTestState"`},
		"Services.xaml": {`x:Name="BtnQuotaRefresh"`, `x:Name="BtnContextToggle"`, `x:Name="BtnMCPProbeAll"`, `x:Name="SvcObsRequests"`},
	}
	for file, required := range emptyStates {
		b, err := os.ReadFile(filepath.Join(root, "ui", "pages", file))
		if err != nil {
			t.Fatal(err)
		}
		for _, marker := range required {
			if !strings.Contains(string(b), marker) {
				t.Errorf("%s missing %s", file, marker)
			}
		}
	}
	pageContracts := map[string][]string{
		"Dashboard.xaml":   {`<ColumnDefinition Width="43*"/><ColumnDefinition Width="10"/><ColumnDefinition Width="32*"/><ColumnDefinition Width="10"/><ColumnDefinition Width="25*"/>`, `<RowDefinition Height="256"/>`, `x:Name="DashChatReady"`, `x:Name="DashGeminiReady"`, `x:Name="SystemPort"`, `x:Name="SystemVersion"`, `x:Name="DashAccountsConnected"`},
		"Logs.xaml":        {`x:Name="LogsSearch"`, `FontSize="14"`, `FontSize="11.8"`, `x:Name="LogsSearchPlaceholder" Text="" Visibility="Collapsed"`},
		"Models.xaml":      {`x:Name="ModelsSearch"`, `FontSize="14"`, `x:Name="ModelDoctorScore"`, `x:Name="ModelAgentScore"`, `x:Name="ModelCodingScore"`, `x:Name="ModelReliabilityScore"`},
		"Settings.xaml":    {`x:Name="BtnOpenRoot"`, `Width="112" HorizontalAlignment="Right" VerticalAlignment="Bottom"`},
		"Integration.xaml": {`x:Name="IntegrationHubState"`, `x:Name="IntegrationHubDetected"`, `x:Name="BtnIntegrationHubRefresh"`},
	}
	for file, required := range pageContracts {
		b, err := os.ReadFile(filepath.Join(root, "ui", "pages", file))
		if err != nil {
			t.Fatal(err)
		}
		for _, marker := range required {
			if !strings.Contains(string(b), marker) {
				t.Errorf("%s missing field-alignment contract %s", file, marker)
			}
		}
	}
	dashboardBytes, err := os.ReadFile(filepath.Join(root, "ui", "pages", "Dashboard.xaml"))
	if err != nil {
		t.Fatal(err)
	}
	dashboardGuide := `<ColumnDefinition Width="43*"/><ColumnDefinition Width="10"/><ColumnDefinition Width="32*"/><ColumnDefinition Width="10"/><ColumnDefinition Width="25*"/>`
	if strings.Count(string(dashboardBytes), dashboardGuide) < 2 {
		t.Error("Dashboard.xaml upper and lower sections must use the same vertical guides")
	}
	if strings.Count(string(dashboardBytes), `Foreground="#46AEFF"`) < 2 {
		t.Error("Dashboard.xaml account and model links must keep the explicit reference blue")
	}

	logsBytes, err := os.ReadFile(filepath.Join(root, "ui", "pages", "Logs.xaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logsBytes), "Поиск по журналу") {
		t.Error("Logs.xaml still contains the removed search placeholder copy")
	}
	for _, marker := range []string{`<Setter Property="Foreground" Value="#D8C7A6"/>`, `<DataTrigger Binding="{Binding level}" Value="WARN">`, `<DataTrigger Binding="{Binding level}" Value="ERROR">`, `<DataTrigger Binding="{Binding level}" Value="DEBUG">`} {
		if !strings.Contains(string(logsBytes), marker) {
			t.Errorf("Logs.xaml missing deterministic level color %s", marker)
		}
	}

	shellBytes, err := os.ReadFile(filepath.Join(root, "ui", "Shell.xaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(shellBytes), `Effect="{StaticResource GlowBlue}" VerticalAlignment="Top" Margin="0,7,0,0"`) {
		t.Error("Shell.xaml sidebar logo is not aligned with the brand title")
	}
	for _, marker := range []string{`CornerRadius="0,12,12,12"`, `x:Name="NavIntegration"`, `Stroke="#28A9FF"`, `ToolTip="Интеграция"`, `Text="Интеграция" Margin="11,0,0,0"`} {
		if !strings.Contains(string(shellBytes), marker) {
			t.Errorf("Shell.xaml missing cold rounded sidebar contract %s", marker)
		}
	}
	if strings.Contains(string(shellBytes), `Text="VS Code Интеграция"`) {
		t.Error("Shell.xaml still renders the removed VS Code navigation label")
	}
	if strings.Contains(string(shellBytes), `<TextBlock Text="OmniBridge" Margin="14,0,0,0"`) {
		t.Error("Shell.xaml still renders the removed miniature title-bar caption")
	}
	integrationBytes, err := os.ReadFile(filepath.Join(root, "ui", "pages", "Integration.xaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(integrationBytes), `<TextBlock Text="Интеграция" Style="{StaticResource PageTitle}"`) || strings.Contains(string(integrationBytes), `Text="VS Code Интеграция"`) {
		t.Error("Integration page must use the concise visible name Интеграция")
	}
	if !strings.Contains(string(dashboardBytes), `<TextBlock Text="Интеграция" FontSize="12"`) || strings.Contains(string(dashboardBytes), `Text="VS Code Интеграция"`) {
		t.Error("Dashboard integration tile must use the concise visible name Интеграция")
	}

	controllerBytes, err := os.ReadFile(filepath.Join(root, "scripts", "desktop.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	controller := string(controllerBytes)
	for _, forbidden := range []string{`$ProviderPreset.ItemsSource=$script:Catalog`, `$ModelsProviderFilter.ItemsSource=$providers`, `Start-Process -FilePath $script:BridgeExe`, `$window.ShowDialog()`, `Закрытие Desktop не останавливает`, `public sealed class OmniTrayColorTable`, `using System.Windows.Forms;`, `System.Collections.Generic.List[System.Drawing.Image]`} {
		if strings.Contains(controller, forbidden) {
			t.Errorf("desktop controller still assigns an object array directly: %s", forbidden)
		}
	}
	for _, required := range []string{`function Set-ControlItems`, `function ConvertTo-FlatArray`, `function Model-Icon`, `(?:\.\d+)?`, `$SystemVersion.Text=[string]$st.version`, `$readyCounts`, `Set-ControlItems $AccountsList $rows $AccountsEmpty`, `Работающие локальные AI с загруженными моделями не обнаружены`, `SetCurrentProcessExplicitAppUserModelID`, `$window.Icon=`, `function Initialize-IntegratedTray`, `ui\TrayDashboard.xaml`, `function Show-TrayDashboard`, `function Refresh-TrayDashboard`, `function Update-TrayCountdown`, `function Set-TrayGateway`, `function Invoke-TrayRefresh`, `/bridge/tray`, `/bridge/tray/start`, `/bridge/tray/stop`, `/bridge/tray/refresh`, `$script:TrayReady=$true`, `[Drawing.SystemIcons]::Application`, `ShowBalloonTip(1800)`, `function Hide-MainWindow`, `function Show-MainWindow`, `function Request-ApplicationExit`, `function Refresh-RouterUI`, `function Invoke-RouterDryRun`, `function Refresh-Services`, `/bridge/quota-cost`, `/bridge/context-guard`, `/bridge/mcp`, `/bridge/observability`, `/bridge/integrations`, `/bridge/router/diagnostics`, `/bridge/router/evaluate`, `/bridge/resilience`, `New-Object Windows.Forms.NotifyIcon`, `$BtnMinimize.Add_Click({Hide-MainWindow})`, `$BtnClose.Add_Click({Request-ApplicationExit})`, `$window.Show()`, `[void]$app.Run()`, `/control/shutdown`, `restart.request`} {
		if !strings.Contains(controller, required) {
			t.Errorf("desktop controller missing %s", required)
		}
	}
	trayStart := strings.Index(controller, "function Initialize-IntegratedTray")
	tray := controller[trayStart:]
	if strings.Index(tray, `$notify.Visible=$true`) < 0 {
		t.Error("integrated Tray icon must become visible")
	}
	if strings.Contains(tray, "ContextMenuStrip") || strings.Contains(tray, "ToolStripControlHost") {
		t.Error("custom Tray Dashboard must replace the legacy Windows context menu")
	}
	trayXAML, err := os.ReadFile(filepath.Join(root, "ui", "TrayDashboard.xaml"))
	if err != nil {
		t.Fatal(err)
	}
	trayText := string(trayXAML)
	for _, required := range []string{`Width="1000"`, `Height="672"`, `x:Name="TrayGigaCard"`, `x:Name="TrayStartButton"`, `x:Name="TrayStopButton"`, `x:Name="TrayRefreshSessions"`, `x:Name="TrayApiCount"`, `x:Name="TrayAgentCount"`, `x:Name="TrayStatisticsButton"`, `x:Name="TrayExitButton"`} {
		if !strings.Contains(trayText, required) {
			t.Errorf("TrayDashboard.xaml missing %s", required)
		}
	}

	themeBytes, err = os.ReadFile(filepath.Join(root, "ui", "Theme.xaml"))
	if err != nil {
		t.Fatal(err)
	}
	themeText := string(themeBytes)
	if strings.Contains(themeText, `<Trigger Property="IsMouseOver" Value="True"><Setter TargetName="ButtonBorder" Property="BorderBrush" Value="{StaticResource BlueBrightBrush}"`) {
		t.Error("button hover still adds the removed bright blue border")
	}
	for _, marker := range []string{`x:Key="TransparentButton"`, `Style="{StaticResource TransparentButton}"`} {
		if marker == `x:Key="TransparentButton"` && !strings.Contains(themeText, marker) {
			t.Error("Theme.xaml missing non-glowing transparent button style")
		}
		if marker != `x:Key="TransparentButton"` && !strings.Contains(string(shellBytes), marker) {
			t.Error("Shell.xaml title-bar controls do not use the non-glowing style")
		}
	}

	launcherBytes, err := os.ReadFile(filepath.Join(root, "cmd", "bridge", "integrated_windows.go"))
	if err != nil {
		t.Fatal(err)
	}
	launcher := string(launcherBytes)
	for _, required := range []string{`logPath := filepath.Join(root, "logs", "ui.log")`, `desktopErrorDetails(logPath)`, `strings.TrimSpace(string(b))`} {
		if !strings.Contains(launcher, required) {
			t.Errorf("integrated Windows launcher missing startup diagnostics %s", required)
		}
	}

	buildBytes, err := os.ReadFile(filepath.Join(root, "scripts", "build-release.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	build := string(buildBytes)
	if !strings.Contains(build, `OmniBridge.exe') ./cmd/bridge`) {
		t.Error("release build must produce the integrated OmniBridge.exe runtime")
	}
	for _, legacy := range []string{"OmniBridgeDesktop.exe", "OmniBridgeTray.exe", "OmniBridgeAccounts.exe"} {
		if strings.Contains(build, legacy) {
			t.Errorf("release build still emits legacy runtime executable %s", legacy)
		}
	}
	if !strings.Contains(build, `OmniBridgeSetup.exe') ./cmd/setup`) {
		t.Error("release build must include the separate maintenance/setup utility")
	}
}

func TestReleaseEngineeringContracts(t *testing.T) {
	root := projectRoot(t)
	maintenance, err := os.ReadFile(filepath.Join(root, "scripts", "maintenance.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(maintenance)
	for _, required := range []string{
		"SHA256SUMS.txt", "RELEASE.json", "New-AppSnapshot", "New-DataBackup", "Resolve-DataBackup", "Test-NewCore",
		"Verify-Sha256Manifest", "Assert-SafeZip", "Copy-DataBackupPayload", "Write-Sha256Manifest $stage",
		"Try-NewDataBackup", "Restore-DataRollback", "$dataRollback",
		"connection-health.json", "omnibridge.db", "omnibridge.db-wal", "secrets.json intentionally excluded", "preupdate", "health-check", "OmniBridgeBackups",
		"Восстановленные данные не прошли health-check", "A legacy JSON-only backup must force a clean JSON -> SQLite import",
		"/readyz", "[string]$h.version -eq $expected", "backup.cmd", "channel -ne 'rc'", "secret hygiene",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("maintenance.ps1 missing %q", required)
		}
	}
	build, err := os.ReadFile(filepath.Join(root, "scripts", "build-release.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	bt := string(build)
	for _, required := range []string{"OmniBridgeSetup.exe", "./cmd/setup", "IMPLEMENTATION_MATRIX.md", "RELEASE.json", "SHA256SUMS.txt", "minimum_supported_upgrade", "Secret-like files found"} {
		if !strings.Contains(bt, required) {
			t.Errorf("build-release.ps1 missing %q", required)
		}
	}
	serverBytes, err := os.ReadFile(filepath.Join(root, "internal", "server", "server.go"))
	if err != nil {
		t.Fatal(err)
	}
	serverText := string(serverBytes)
	for _, required := range []string{`GET /readyz`, `GET /bridge/release-readiness`} {
		if !strings.Contains(serverText, required) {
			t.Errorf("release readiness route missing %q", required)
		}
	}
	for _, file := range []string{"RELEASE.json", "install.cmd", "repair.cmd", "backup.cmd", "restore.cmd", "update.cmd", "uninstall.cmd", filepath.Join("docs", "MAINTENANCE_RU.md")} {
		if _, err := os.Stat(filepath.Join(root, file)); err != nil {
			t.Errorf("release maintenance asset missing: %s", file)
		}
	}
}

func TestAccountLoginControllerUsesServerAuthFields(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(projectRoot(t), "scripts", "desktop.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, required := range []string{
		`$r.auth_url`,
		`$r.verification_url`,
		`Start-Process $authUrl`,
		`Start-Process $verificationUrl`,
		`$settings.command=$CodexPath.Text.Trim()`,
		`$settings.client_json=$GeminiClientPath.Text.Trim()`,
		`/preflight`,
		`$r.browser_managed`,
		`Официальный Codex CLI управляет входом`,
		`@{id=$a.id;name=$a.name;kind=$Kind;settings=$settings}`,
	} {
		if !strings.Contains(text, required) {
			t.Errorf("desktop account-login controller missing %s", required)
		}
	}
}

func TestProviderIconCoverage(t *testing.T) {
	root := projectRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "internal", "providerdefs", "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	var defs []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &defs); err != nil {
		t.Fatal(err)
	}
	if len(defs) < 80 {
		t.Fatalf("unexpected provider catalog size: %d", len(defs))
	}
	for _, d := range defs {
		if d.ID == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, "assets", "providers", d.ID+".png")); err != nil {
			t.Errorf("missing provider icon for %s", d.ID)
		}
	}
}

func TestModelIconsAndDoctorUIContract(t *testing.T) {
	root := projectRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "ui", "pages", "Models.xaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, marker := range []string{`Source="{Binding icon}"`, `x:Name="ModelDoctorScore"`, `x:Name="ModelAgentScore"`, `x:Name="ModelCodingScore"`, `x:Name="ModelReliabilityScore"`} {
		if !strings.Contains(text, marker) {
			t.Errorf("Models UI missing %s", marker)
		}
	}
	desktop, err := os.ReadFile(filepath.Join(root, "scripts", "desktop.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	d := string(desktop)
	for _, marker := range []string{`Model-Icon`, `providers\`, `model-families\`, `Format-DoctorScore`, `$selectedID`, `Update-ModelDetail`} {
		if !strings.Contains(d, marker) {
			t.Errorf("desktop model contract missing %s", marker)
		}
	}
}

func TestVSCodeSyncUsesPickerExportV22(t *testing.T) {
	root := projectRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "scripts", "sync-vscode.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, required := range []string{
		`/bridge/vscode/export`,
		`vendor='customendpoint'`,
		`apiType='chat-completions'`,
		`toolCalling=[bool]$m.toolCalling`,
		`requestHeaders=[ordered]@{Authorization=('Bearer '+$dataKey);'x-bridge-key'=$dataKey}`,
		`if([string]$vm.requestHeaders.Authorization -ne ('Bearer '+$dataKey))`,
		`bridge_key_fingerprint`,
		`/v1/models`,
		`chat.agentHost.byokModels.enabled`,
		`VS Code нужно полностью перезапустить`,
	} {
		if !strings.Contains(text, required) {
			t.Errorf("VS Code sync v2.2 contract missing %q", required)
		}
	}
	if strings.Contains(text, `Invoke-RestMethod -Uri 'http://127.0.0.1:4142/bridge/models'`) {
		t.Error("VS Code sync still consumes raw Model Registry instead of the picker export contract")
	}
}
