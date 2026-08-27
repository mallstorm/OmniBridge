package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"omnibridge/internal/config"
	"omnibridge/internal/secretstore"
	"path/filepath"
	"testing"
)

func TestVerifyAgentRoundTripRequiresSemanticCompletion(t *testing.T) {
	finalText := agentProbeCompletionMarker
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v map[string]any
		_ = json.NewDecoder(r.Body).Decode(&v)
		messages, _ := v["messages"].([]any)
		for _, raw := range messages {
			msg, _ := raw.(map[string]any)
			if msg["role"] == "tool" {
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": finalText}, "finish_reason": "stop"}}})
				return
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{"id": "call-1", "type": "function", "function": map[string]any{"name": "test_probe", "arguments": `{"value":7}`}}}}, "finish_reason": "tool_calls"}}})
	}))
	defer up.Close()

	dir := t.TempDir()
	m := NewManager(secretstore.New(filepath.Join(dir, "secrets.json")))
	p := config.NormalizeProvider(config.Provider{ID: "mock", Name: "Mock", Kind: "custom", BaseURL: up.URL, AuthType: "none", Enabled: true, Protocols: []string{"chat-completions"}, PreferredProtocol: "chat-completions"})
	ok, reason := m.verifyAgentRoundTrip(context.Background(), p, "mock/model", "chat-completions")
	if !ok || reason != "" {
		t.Fatalf("expected strict round-trip success, ok=%v reason=%q", ok, reason)
	}
}

func TestVerifyAgentRoundTripRejectsMereSecondRound2xx(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v map[string]any
		_ = json.NewDecoder(r.Body).Decode(&v)
		messages, _ := v["messages"].([]any)
		for _, raw := range messages {
			msg, _ := raw.(map[string]any)
			if msg["role"] == "tool" {
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "tool-done"}, "finish_reason": "stop"}}})
				return
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{"id": "call-1", "type": "function", "function": map[string]any{"name": "test_probe", "arguments": `{"value":7}`}}}}, "finish_reason": "tool_calls"}}})
	}))
	defer up.Close()

	dir := t.TempDir()
	m := NewManager(secretstore.New(filepath.Join(dir, "secrets.json")))
	p := config.NormalizeProvider(config.Provider{ID: "mock", Name: "Mock", Kind: "custom", BaseURL: up.URL, AuthType: "none", Enabled: true, Protocols: []string{"chat-completions"}, PreferredProtocol: "chat-completions"})
	ok, reason := m.verifyAgentRoundTrip(context.Background(), p, "mock/model", "chat-completions")
	if ok || reason == "" {
		t.Fatalf("mere 2xx must fail strict Agent Doctor, ok=%v reason=%q", ok, reason)
	}
}
