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

func TestOrchestrationHTTPWorkflowEndToEnd(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	outputs := []string{"draft answer", "one concrete issue", "fixed answer", "PASS\nNo blocking issues."}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "bad auth", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data": []any{map[string]any{"id": "model-a", "object": "model", "owned_by": "mock"}},
			})
		case "/v1/chat/completions":
			mu.Lock()
			index := calls
			calls++
			mu.Unlock()
			if index >= len(outputs) {
				http.Error(w, "unexpected workflow call", http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":     "upstream",
				"object": "chat.completion",
				"model":  "model-a",
				"choices": []any{map[string]any{
					"index":         0,
					"message":       map[string]any{"role": "assistant", "content": outputs[index]},
					"finish_reason": "stop",
				}},
				"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	dir := t.TempDir()
	cfg, err := config.New(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	if err := sec.Put("provider:mock", "secret"); err != nil {
		t.Fatal(err)
	}
	provider := config.NormalizeProvider(config.Provider{
		ID:            "mock",
		Name:          "Mock",
		Kind:          "openai-compatible",
		BaseURL:       upstream.URL + "/v1",
		APIType:       "chat-completions",
		AuthType:      "bearer",
		CredentialRef: "provider:mock",
		Enabled:       true,
	})
	if err := cfg.UpsertProvider(provider); err != nil {
		t.Fatal(err)
	}

	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	defer am.Close()
	reg := registry.New(filepath.Join(dir, "models.json"), cfg, pm, am)
	reg.RefreshAll(context.Background())
	reg.UpdateProbe("mock/model-a", providers.ProbeResult{
		Deep:          true,
		Basic:         true,
		Streaming:     true,
		SystemPrompt:  true,
		Reasoning:     true,
		DoctorStatus:  "chat_ready",
		SelectedProtocol: "chat-completions",
	})

	st := stats.New(filepath.Join(dir, "stats.json"))
	srv := New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0))
	db, err := storage.Open(filepath.Join(dir, "omnibridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	srv.SetStorage(db)

	ts := httptest.NewServer(srv.OrchestrationHandler())
	defer ts.Close()
	keys := cfg.Get()

	modelsReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/models", nil)
	modelsReq.Header.Set("Authorization", "Bearer "+keys.DataAPIKey)
	modelsResp, err := http.DefaultClient.Do(modelsReq)
	if err != nil {
		t.Fatal(err)
	}
	modelsBody, _ := io.ReadAll(modelsResp.Body)
	modelsResp.Body.Close()
	if modelsResp.StatusCode != http.StatusOK || !strings.Contains(string(modelsBody), "workflow/coding-review") {
		t.Fatalf("models status=%d body=%s", modelsResp.StatusCode, modelsBody)
	}

	requestBody := `{"model":"workflow/coding-review","messages":[{"role":"user","content":"implement the feature"}]}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(requestBody))
	req.Header.Set("Authorization", "Bearer "+keys.DataAPIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("workflow status=%d body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "fixed answer") {
		t.Fatalf("workflow body=%s", body)
	}
	mu.Lock()
	gotCalls := calls
	mu.Unlock()
	if gotCalls != 4 {
		t.Fatalf("upstream calls=%d want=4", gotCalls)
	}

	executionsReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/bridge/orchestration/executions", nil)
	executionsReq.Header.Set("Authorization", "Bearer "+keys.AdminAPIKey)
	executionsResp, err := http.DefaultClient.Do(executionsReq)
	if err != nil {
		t.Fatal(err)
	}
	executionsBody, _ := io.ReadAll(executionsResp.Body)
	executionsResp.Body.Close()
	if executionsResp.StatusCode != http.StatusOK || !strings.Contains(string(executionsBody), "workflow/coding-review") {
		t.Fatalf("executions status=%d body=%s", executionsResp.StatusCode, executionsBody)
	}
}
