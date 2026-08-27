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
	"omnibridge/internal/orchestration"
	"omnibridge/internal/providers"
	"omnibridge/internal/registry"
	"omnibridge/internal/secretstore"
	"omnibridge/internal/stats"
	"omnibridge/internal/storage"
)

func TestMultiProviderTeamExecutionAcceptance(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []any{map[string]any{"id": "model-a", "object": "model"}}})
		case "/v1/chat/completions":
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			msgs, _ := req["messages"].([]any)
			system := ""
			if len(msgs) > 0 {
				first, _ := msgs[0].(map[string]any)
				system, _ = first["content"].(string)
			}
			output := "PASS"
			switch {
			case strings.Contains(system, "Developer"):
				output = "draft answer"
			case strings.Contains(system, "Critic"):
				output = "one concrete issue"
			case strings.Contains(system, "Fixer"):
				output = "fixed answer"
			case strings.Contains(system, "quality gate"):
				output = "PASS\nNo blocking issues."
			}
			mu.Lock()
			calls++
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "upstream", "object": "chat.completion", "model": "model-a",
				"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": output}, "finish_reason": "stop"}},
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
	for i := 1; i <= 4; i++ {
		id := "p" + string(rune('0'+i))
		ref := "provider:" + id
		if err := sec.Put(ref, "secret"); err != nil {
			t.Fatal(err)
		}
		p := config.NormalizeProvider(config.Provider{ID: id, Name: id, Kind: "openai-compatible", BaseURL: upstream.URL + "/v1", APIType: "chat-completions", AuthType: "bearer", CredentialRef: ref, Enabled: true})
		if err := cfg.UpsertProvider(p); err != nil {
			t.Fatal(err)
		}
	}

	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	defer am.Close()
	reg := registry.New(filepath.Join(dir, "models.json"), cfg, pm, am)
	reg.RefreshAll(context.Background())
	for i := 1; i <= 4; i++ {
		id := "p" + string(rune('0'+i)) + "/model-a"
		reg.UpdateProbe(id, providers.ProbeResult{
			Deep: true, Basic: true, Streaming: true, SystemPrompt: true, Reasoning: true,
			ToolCalling: true, ToolRoundTrip: true, AgentCompatible: true,
			DoctorStatus: "agent_ready", SelectedProtocol: "chat-completions",
		})
	}

	st := stats.New(filepath.Join(dir, "stats.json"))
	srv := New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0))
	db, err := storage.Open(filepath.Join(dir, "omnibridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	srv.SetStorage(db)

	ex, err := newOrchestrationEngine(srv).Run(context.Background(), orchestration.CodingReviewModel, orchestration.Request{ID: "multi-provider-team", Input: "implement feature"})
	if err != nil {
		t.Fatal(err)
	}
	if ex.FinalResult != "fixed answer" || ex.Review.Status != "PASS" || len(ex.Steps) != 4 {
		t.Fatalf("execution=%+v", ex)
	}
	providersUsed := map[string]bool{}
	for _, step := range ex.Steps {
		providersUsed[step.Provider] = true
		if step.Routing.Profile == "" || step.Routing.Family == "" || len(step.Routing.Rationale) == 0 {
			t.Fatalf("missing routing diagnostics in step: %+v", step)
		}
	}
	if len(providersUsed) != 4 {
		t.Fatalf("expected four-provider team, providers=%v steps=%+v", providersUsed, ex.Steps)
	}
	mu.Lock()
	gotCalls := calls
	mu.Unlock()
	if gotCalls != 4 {
		t.Fatalf("upstream calls=%d want=4", gotCalls)
	}
}
