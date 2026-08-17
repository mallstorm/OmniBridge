package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDetectKind(t *testing.T) {
	cases := map[string]string{"sk-ant-test": "anthropic", "sk-or-test": "openrouter", "xai-test": "xai", "AIza-test": "gemini"}
	for key, want := range cases {
		if got := DetectKind("", key); got != want {
			t.Fatalf("%s => %s want %s", key, got, want)
		}
	}
}
func TestNormalizeGemini(t *testing.T) {
	p := NormalizeProvider(Provider{Kind: "gemini"})
	if p.APIType != "chat-completions" || p.BaseURL == "" || p.ModelsURL == "" {
		t.Fatalf("bad preset %+v", p)
	}
}

func TestProviderCatalogCoverage(t *testing.T) {
	catalog := ProviderCatalog()
	if len(catalog) < 30 {
		t.Fatalf("provider catalog too small: %d", len(catalog))
	}
	for _, id := range []string{"yandex", "nanogpt", "openrouter", "deepseek", "alibaba", "ollama", "lmstudio", "localai", "custom"} {
		if _, ok := ProviderPresetByID(id); !ok {
			t.Fatalf("missing provider preset %q", id)
		}
	}
	for _, id := range []string{"ollama", "lmstudio", "vllm", "localai"} {
		p, _ := ProviderPresetByID(id)
		if !p.APIKeyOptional || p.AuthType != "none" {
			t.Fatalf("local preset %q must allow keyless auth: %+v", id, p)
		}
	}
}

func TestNormalizeYandex(t *testing.T) {
	p := NormalizeProvider(Provider{Kind: "yandex", ProjectID: "b1g-folder"})
	if p.AuthType != "yandex-api-key" || p.PreferredProtocol != "responses" || p.Headers["OpenAI-Project"] != "b1g-folder" {
		t.Fatalf("bad Yandex preset: %+v", p)
	}
}

func TestNormalizeNanoGPTAndAlibabaProtocols(t *testing.T) {
	n := NormalizeProvider(Provider{Kind: "nanogpt"})
	if n.PreferredProtocol != "responses" || !SupportsProtocol(n, "messages") || n.ModelsURL == "" {
		t.Fatalf("bad NanoGPT preset: %+v", n)
	}
	a := NormalizeProvider(Provider{Kind: "alibaba"})
	if got := ProtocolBaseURL(a, "messages"); got == a.BaseURL || got == "" {
		t.Fatalf("Alibaba Messages must use its Anthropic-compatible base, got %q", got)
	}
	if got := ProtocolAuthType(a, "messages"); got != "x-api-key" {
		t.Fatalf("Alibaba Messages auth = %q, want x-api-key", got)
	}
}

func TestAPIAndAccountIDsCannotCollide(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir + "/config.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertProvider(Provider{ID: "same", Name: "API", Kind: "custom", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAccount(AccountConnection{ID: "same", Name: "Account", Kind: "chatgpt-codex", Enabled: true}); err == nil {
		t.Fatal("expected account/provider id collision to be rejected")
	}
}

func TestCoreAPIProvidersRemainAfterAccountCatalogCleanup(t *testing.T) {
	cat := ProviderCatalog()
	seen := map[string]bool{}
	for _, p := range cat {
		seen[p.ID] = true
	}
	for _, id := range []string{"anthropic", "deepseek", "alibaba"} {
		if !seen[id] {
			t.Fatalf("API provider %q must remain available", id)
		}
	}
}

func TestRoutingPersistsAndSanitizesAliases(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir + "/config.json")
	if err != nil {
		t.Fatal(err)
	}
	err = s.SetRouting(RoutingConfig{
		Aliases:    map[string]string{"Coding Fast": "mock/model-a"},
		Priorities: map[string][]string{"reasoning": {"mock/model-a", "backup/model-a"}},
		Fallbacks:  map[string][]string{"mock/model-a": {"backup/model-a"}},
		MaxRetries: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Get().Routing.Aliases["coding-fast"]; got != "mock/model-a" {
		t.Fatalf("sanitized alias target=%q", got)
	}
	reloaded, err := New(dir + "/config.json")
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Get().Routing.MaxRetries != 2 || len(reloaded.Get().Routing.Fallbacks["mock/model-a"]) != 1 {
		t.Fatalf("routing did not persist: %+v", reloaded.Get().Routing)
	}
}

func TestBridgeKeysAreRemovedFromConfigAfterProtection(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.json"
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	store := &memorySecretStore{items: map[string]string{}}
	before := s.Get()
	if before.DataAPIKey == "" || before.AdminAPIKey == "" {
		t.Fatal("runtime keys missing")
	}
	if err := s.EnsureProtectedBridgeKeys(store); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if strings.Contains(text, before.DataAPIKey) || strings.Contains(text, before.AdminAPIKey) || strings.Contains(text, "data_api_key") || strings.Contains(text, "admin_api_key") {
		t.Fatalf("bridge keys leaked to config.json: %s", text)
	}
	reloaded, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := reloaded.EnsureProtectedBridgeKeys(store); err != nil {
		t.Fatal(err)
	}
	if reloaded.Get().DataAPIKey != before.DataAPIKey || reloaded.Get().AdminAPIKey != before.AdminAPIKey {
		t.Fatal("protected bridge keys did not survive reload")
	}
}

type memorySecretStore struct{ items map[string]string }

func (m *memorySecretStore) Get(k string) (string, error) {
	v, ok := m.items[k]
	if !ok {
		return "", os.ErrNotExist
	}
	return v, nil
}
func (m *memorySecretStore) Put(k, v string) error { m.items[k] = v; return nil }

func TestRoutingPolicyAndCircuitDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	c := s.Get()
	if c.Routing.CircuitBreakerThreshold != 3 || c.Routing.CircuitBreakerCooldownSeconds != 30 {
		t.Fatalf("bad defaults: %+v", c.Routing)
	}
	err = s.SetRouting(RoutingConfig{Aliases: map[string]string{}, Priorities: map[string][]string{}, Fallbacks: map[string][]string{}, Policies: map[string]RoutingPolicy{"fast route": {Mode: "fastest", Candidates: []string{"a/m", "b/m"}}}, MaxRetries: 2, CircuitBreakerThreshold: 4, CircuitBreakerCooldownSeconds: 45})
	if err != nil {
		t.Fatal(err)
	}
	got := s.Get().Routing
	if _, ok := got.Policies["fast-route"]; !ok || got.CircuitBreakerThreshold != 4 {
		t.Fatalf("routing=%+v", got)
	}
}

func TestAccountTelemetryPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s, _ := New(path)
	now := time.Now().UTC().Truncate(time.Second)
	a := AccountConnection{ID: "acct", Name: "Account", Kind: "chatgpt-codex", Enabled: true, Status: "reauth_required", LastError: "login", LastErrorKind: "reauth_required", LastChecked: now, LastConnectedAt: now.Add(-time.Hour), RefreshFailures: 2}
	if err := s.UpsertAccount(a); err != nil {
		t.Fatal(err)
	}
	s2, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	got := s2.Get().Accounts[0]
	if got.LastErrorKind != "reauth_required" || got.RefreshFailures != 2 || got.LastChecked.IsZero() {
		t.Fatalf("telemetry lost: %+v", got)
	}
}

func TestRouter3PoliciesAndHints(t *testing.T) {
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	modes := []string{"weighted", "round_robin", "least_used", "p2c", "quota_optimized", "cost_optimized", "auto"}
	policies := map[string]RoutingPolicy{}
	for _, mode := range modes {
		policies[mode] = RoutingPolicy{Mode: mode, Candidates: []string{"a", "b"}, Weights: map[string]float64{"a": 3, "b": 1}, QuotaHints: map[string]float64{"a": 80}, CostHints: map[string]float64{"a": 1.5}}
	}
	if err := s.SetRouting(RoutingConfig{Aliases: map[string]string{}, Priorities: map[string][]string{}, Fallbacks: map[string][]string{}, Policies: policies, MaxRetries: 2, CircuitBreakerThreshold: 3, CircuitBreakerCooldownSeconds: 30, SessionStickiness: true, SessionTTLSeconds: 1800}); err != nil {
		t.Fatal(err)
	}
	got := s.Get().Routing
	if !got.SessionStickiness || got.SessionTTLSeconds != 1800 || got.Policies["weighted"].Weights["a"] != 3 {
		t.Fatalf("router3 config not persisted: %+v", got)
	}
	bad := got
	bad.Policies["weighted"] = RoutingPolicy{Mode: "weighted", Weights: map[string]float64{"a": 0}}
	if err := s.SetRouting(bad); err == nil {
		t.Fatal("zero weight must be rejected")
	}
}
