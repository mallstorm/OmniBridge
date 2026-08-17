package contextguard

import (
	"encoding/json"
	"omnibridge/internal/config"
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

func TestObserveDoesNotModify(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"model": "m", "messages": []any{map[string]any{"role": "user", "content": string(make([]byte, 6000))}}})
	out, rep, err := Apply(raw, "chat-completions", 1, config.ContextGuardConfig{Enabled: true, Mode: "observe", MaxContextPercent: 90})
	if err != nil || string(out) != string(raw) || rep.Action != "would_compact" {
		t.Fatalf("%v %+v", err, rep)
	}
}
