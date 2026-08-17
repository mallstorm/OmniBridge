package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"omnibridge/internal/accounts"
	"omnibridge/internal/config"
	"omnibridge/internal/providers"
	"omnibridge/internal/secretstore"
)

func TestRefreshPreservesVerifiedDoctorState(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "m"}}})
	}))
	defer up.Close()
	dir := t.TempDir()
	cfg, _ := config.New(filepath.Join(dir, "config.json"))
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	if err := cfg.UpsertProvider(config.Provider{ID: "p", Name: "P", Kind: "custom", BaseURL: up.URL + "/v1", ModelsURL: up.URL + "/v1/models", AuthType: "none", Enabled: true, Protocols: []string{"chat-completions"}}); err != nil {
		t.Fatal(err)
	}
	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	r := New(filepath.Join(dir, "models.json"), cfg, pm, am)
	r.RefreshAll(context.Background())
	r.UpdateProbe("p/m", providers.ProbeResult{Deep: true, Basic: true, Streaming: true, SystemPrompt: true, ToolCalling: true, ToolRoundTrip: true, AgentCompatible: true, DoctorStatus: "agent_ready", Protocols: map[string]providers.ProtocolProbe{"chat-completions": {Basic: true, Streaming: true, SystemPrompt: true, ToolCalling: true, ToolRoundTrip: true, AgentCompatible: true}}})
	before, err := r.Find("p/m")
	if err != nil || !before.DoctorVerified || before.DoctorScore <= 0 {
		t.Fatalf("before=%+v err=%v", before, err)
	}
	r.RefreshAll(context.Background())
	after, err := r.Find("p/m")
	if err != nil {
		t.Fatal(err)
	}
	if !after.DoctorVerified || after.DoctorScore != before.DoctorScore || after.AgentScore != before.AgentScore || !after.AgentReady || after.DoctorCheckedAt.IsZero() {
		t.Fatalf("doctor state lost after refresh: before=%+v after=%+v", before, after)
	}
}
