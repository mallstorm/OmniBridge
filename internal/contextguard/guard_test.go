package contextguard

import (
	"encoding/json"
	"omnibridge/internal/config"
	"strings"
	"testing"
)

func TestCompactionPreservesSystemAndRecent(t *testing.T) {
	msgs := []any{map[string]any{"role": "system", "content": "rules"}}
	for i := 0; i < 30; i++ {
		msgs = append(msgs, map[string]any{"role": "user", "content": string(make([]byte, 400))})
	}
	raw, _ := json.Marshal(map[string]any{"model": "m", "messages": msgs})
	out, rep, err := Apply(raw, "chat-completions", 2000, config.ContextGuardConfig{Enabled: true, Mode: "compact", MaxContextPercent: 80, ReserveOutputTokens: 100, MinRecentMessages: 4})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Action != "compacted" || rep.RemovedMessages == 0 || len(out) >= len(raw) {
		t.Fatalf("unexpected report %+v", rep)
	}
	var root map[string]any
	_ = json.Unmarshal(out, &root)
	arr := root["messages"].([]any)
	first := arr[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "rules" {
		t.Fatal("system instruction lost")
	}
}

func TestSafeModeNeverRewritesCodeOrToolSchema(t *testing.T) {
	root := map[string]any{"model": "x", "tools": []any{map[string]any{"type": "function", "function": map[string]any{"name": "write_file", "parameters": map[string]any{"type": "object", "required": []any{"path"}, "properties": map[string]any{"path": map[string]any{"type": "string", "pattern": "^src/"}}}}}}, "messages": []any{map[string]any{"role": "system", "content": "rules"}, map[string]any{"role": "user", "content": "old"}, map[string]any{"role": "assistant", "content": "old"}, map[string]any{"role": "user", "content": "old"}, map[string]any{"role": "assistant", "content": "old"}, map[string]any{"role": "user", "content": "```go\nfunc main() {}\n```"}, map[string]any{"role": "user", "content": "latest"}}}
	raw, _ := json.Marshal(root)
	cfg := config.ContextGuardConfig{Enabled: true, Mode: "safe", MaxContextPercent: 11, ReserveOutputTokens: 0, MinRecentMessages: 2}
	out, _, err := Apply(raw, "chat-completions", 1024, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]any
	if json.Unmarshal(raw, &before) != nil || json.Unmarshal(out, &after) != nil {
		t.Fatal("invalid json")
	}
	b1, _ := json.Marshal(before["tools"])
	b2, _ := json.Marshal(after["tools"])
	if string(b1) != string(b2) {
		t.Fatalf("tool schema changed: %s != %s", b1, b2)
	}
	if !strings.Contains(string(out), "func main()") {
		t.Fatal("code block was removed or rewritten")
	}
}

func TestObserveDoesNotModify(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"model": "m", "messages": []any{map[string]any{"role": "user", "content": string(make([]byte, 6000))}}})
	out, rep, err := Apply(raw, "chat-completions", 1, config.ContextGuardConfig{Enabled: true, Mode: "observe", MaxContextPercent: 90})
	if err != nil || string(out) != string(raw) || rep.Action != "would_compact" {
		t.Fatalf("%v %+v", err, rep)
	}
}
