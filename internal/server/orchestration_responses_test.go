package server

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"omnibridge/internal/accounts"
	"omnibridge/internal/config"
	"omnibridge/internal/providers"
	"omnibridge/internal/registry"
	"omnibridge/internal/secretstore"
	"omnibridge/internal/stats"
	"omnibridge/internal/storage"
)

func TestOrchestrationResponsesAPIEndToEnd(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	outputs := []string{"draft", "issue", "fixed via responses", "PASS\nNo blocking issues."}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []any{map[string]any{"id": "model-a", "object": "model"}})
		case "/v1/chat/completions":
			mu.Lock()
			i := calls
			calls++
			mu.Unlock()
			if i >= len(outputs) {
				http.Error(w, "unexpected call", http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "upstream", "object": "chat.completion", "model": "model-a",
				"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": outputs[i]}, "finish_reason": "stop"}},
				"usage": map[string]any{"prompt_tokens": 8, "completion_tokens": 4, "total_tokens": 12},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	dir := t.TempDir()
	cfg, err := config.New(filepath.Join(dir, "config.json"))
	if err != nil { t.Fatal(err) }
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	if err := sec.Put("provider:mock", "secret"); err != nil { t.Fatal(err) }
	p := config.NormalizeProvider(config.Provider{ID: "mock", Name: "Mock", Kind: "openai-compatible", BaseURL: upstream.URL + "/v1", APIType: "chat-completions", AuthType: "bearer", CredentialRef: "provider:mock", Enabled: true})
	if err := cfg.UpsertProvider(p); err != nil { t.Fatal(err) }

	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	defer am.Close()
	reg := registry.New(filepath.Join(dir, "models.json"), cfg, pm, am)
	reg.RefreshAll(context.Background())
	reg.UpdateProbe("mock/model-a", providers.ProbeResult{Deep: true, Basic: true, Streaming: true, SystemPrompt: true, Reasoning: true, DoctorStatus: "chat_ready", SelectedProtocol: "chat-completions"})

	srv := New(cfg, sec, pm, am, reg, stats.New(filepath.Join(dir, "stats.json")), log.New(io.Discard, "", 0))
	db, err := storage.Open(filepath.Join(dir, "omnibridge.db"))
	if err != nil { t.Fatal(err) }
	defer db.Close()
	srv.SetStorage(db)

	ts := httptest.NewServer(srv.OrchestrationHandler())
	defer ts.Close()
	keys := cfg.Get()

	body := `{"model":"workflow/coding-review","input":"implement the feature"}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/responses", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+keys.DataAPIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil { t.Fatal(err) }
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("responses workflow status=%d body=%s", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "fixed via responses") {
		t.Fatalf("responses workflow body=%s", raw)
	}
	if resp.Header.Get("X-OmniBridge-Workflow-Execution") == "" {
		t.Fatal("missing workflow execution header")
	}
	mu.Lock()
	gotCalls := calls
	mu.Unlock()
	if gotCalls != 4 {
		t.Fatalf("upstream calls=%d want=4", gotCalls)
	}
}
