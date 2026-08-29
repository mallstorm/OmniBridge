package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"omnibridge/internal/config"
	"omnibridge/internal/secretstore"
	"path/filepath"
	"strings"
	"testing"
)

func TestModelDoctorV2ChatCapabilities(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v map[string]any
		_ = json.NewDecoder(r.Body).Decode(&v)
		if stream, _ := v["stream"].(bool); stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\ndata: [DONE]\n\n"))
			return
		}
		msgs, _ := v["messages"].([]any)
		for _, raw := range msgs {
			m, _ := raw.(map[string]any)
			if m["role"] == "tool" {
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "tool-done"}, "finish_reason": "stop"}}})
				return
			}
		}
		if _, ok := v["response_format"]; ok {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": `{"ok":true}`}, "finish_reason": "stop"}}})
			return
		}
		if _, ok := v["reasoning_effort"]; ok {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "4"}, "finish_reason": "stop"}}})
			return
		}
		if len(msgs) > 0 {
			last, _ := msgs[len(msgs)-1].(map[string]any)
			if prompt, _ := last["content"].(string); strings.Contains(prompt, "IDENTITY_OK") {
				_ = json.NewEncoder(w).Encode(map[string]any{"model": "model", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "IDENTITY_OK: Mock Model"}, "finish_reason": "stop"}}})
				return
			}
			first, _ := msgs[0].(map[string]any)
			if first["role"] == "system" {
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "SYS_OK"}, "finish_reason": "stop"}}})
				return
			}
			if parts, ok := last["content"].([]any); ok {
				for _, pr := range parts {
					pm, _ := pr.(map[string]any)
					if pm["type"] == "image_url" {
						_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "VIS_Q7Z"}, "finish_reason": "stop"}}})
						return
					}
				}
			}
		}
		if tools, ok := v["tools"].([]any); ok && len(tools) > 0 {
			calls := []any{}
			for i, raw := range tools {
				t, _ := raw.(map[string]any)
				fn, _ := t["function"].(map[string]any)
				name, _ := fn["name"].(string)
				calls = append(calls, map[string]any{"id": "call-" + string(rune('a'+i)), "type": "function", "function": map[string]any{"name": name, "arguments": `{"value":7}`}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": nil, "tool_calls": calls}, "finish_reason": "tool_calls"}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "OK"}, "finish_reason": "stop"}}})
	}))
	defer up.Close()
	dir := t.TempDir()
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	m := NewManager(sec)
	p := config.NormalizeProvider(config.Provider{ID: "mock", Name: "Mock", Kind: "custom", BaseURL: strings.TrimRight(up.URL, "/"), AuthType: "none", Enabled: true, Protocols: []string{"chat-completions"}, PreferredProtocol: "chat-completions"})
	model := Model{ID: "mock/model", NativeID: "model", ProviderID: "mock", Name: "Mock Model", APIType: "chat-completions", PreferredProtocol: "chat-completions", Protocols: []string{"chat-completions"}}
	res := m.Probe(context.Background(), p, model, true)
	if !res.Basic || !res.Streaming || !res.SystemPrompt || !res.ToolCalling || !res.ToolRoundTrip || !res.ParallelTools || !res.StructuredOutput || !res.Vision || !res.Reasoning || !res.IdentityVerified || res.UpstreamModelID != "model" || !res.AgentCompatible || res.DoctorStatus != "agent_ready" {
		t.Fatalf("doctor v2 result=%+v", res)
	}
}
