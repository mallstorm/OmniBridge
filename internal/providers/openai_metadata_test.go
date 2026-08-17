package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"omnibridge/internal/config"
	"omnibridge/internal/secretstore"
)

func TestDetailedModelCapabilities(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []any{map[string]any{
				"id": "cap-model", "name": "Capability Model", "context_length": 131072, "max_output_tokens": 8192,
				"capabilities": map[string]any{"vision": true, "reasoning": true, "tool_calling": true, "parallel_tool_calls": true, "structured_output": true, "pdf_upload": true},
			}},
		})
	}))
	defer ts.Close()
	m := NewManager(secretstore.New(t.TempDir() + "/s.json"))
	p := config.NormalizeProvider(config.Provider{ID: "nano", Name: "Nano", Kind: "custom", BaseURL: ts.URL, ModelsURL: ts.URL + "/models", AuthType: "none", Protocols: []string{"responses", "messages", "chat-completions"}, PreferredProtocol: "responses", Enabled: true})
	models, err := m.ListModels(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 {
		t.Fatalf("models=%d", len(models))
	}
	got := models[0]
	if !got.ToolCalling || !got.ParallelTools || !got.StructuredOutput || !got.Reasoning || !got.Vision || !got.PDF {
		t.Fatalf("capabilities not propagated: %+v", got)
	}
	if got.ContextWindow != 131072 || got.MaxOutputTokens != 8192 || got.PreferredProtocol != "responses" {
		t.Fatalf("metadata not propagated: %+v", got)
	}
}
