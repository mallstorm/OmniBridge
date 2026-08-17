package providers

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGigaToolTranslationRoundTrip(t *testing.T) {
	in := map[string]any{
		"model":    "giga/GigaChat-3-Ultra",
		"messages": []any{map[string]any{"role": "user", "content": "read x"}},
		"tools": []any{map[string]any{"type": "function", "function": map[string]any{
			"name": "read_file", "description": "read", "parameters": map[string]any{
				"type": "object", "properties": map[string]any{"newCode": map[string]any{"anyOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}}},
			},
		}}},
		"tool_choice": "auto",
	}
	b, _ := json.Marshal(in)
	out, reverse, err := translateOpenAIToGiga(b)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["model"] != "GigaChat-3-Ultra" {
		t.Fatalf("model=%v", got["model"])
	}
	funcs := got["functions"].([]any)
	fn := funcs[0].(map[string]any)
	alias := fn["name"].(string)
	if strings.ContainsAny(alias, "_0123456789-") {
		t.Fatalf("unsafe alias %q", alias)
	}
	if reverse[alias] != "read_file" {
		t.Fatalf("reverse alias failed")
	}
	params := fn["parameters"].(map[string]any)
	props := params["properties"].(map[string]any)
	nc := props["newCode"].(map[string]any)
	if _, ok := nc["anyOf"]; ok {
		t.Fatal("anyOf not sanitized")
	}
	if nc["type"] != "string" {
		t.Fatalf("sanitized type=%v", nc["type"])
	}

	state := "123e4567-e89b-12d3-a456-426614174000"
	upstream := map[string]any{"choices": []any{map[string]any{"finish_reason": "function_call", "message": map[string]any{"role": "assistant", "content": "", "functions_state_id": state, "function_call": map[string]any{"name": alias, "arguments": map[string]any{"path": "x"}}}}}}
	ub, _ := json.Marshal(upstream)
	rb, err := translateGigaJSON(ub, reverse)
	if err != nil {
		t.Fatal(err)
	}
	var rr map[string]any
	_ = json.Unmarshal(rb, &rr)
	choice := rr["choices"].([]any)[0].(map[string]any)
	if choice["finish_reason"] != "tool_calls" {
		t.Fatalf("finish=%v", choice["finish_reason"])
	}
	msg := choice["message"].(map[string]any)
	tc := msg["tool_calls"].([]any)[0].(map[string]any)
	f := tc["function"].(map[string]any)
	if f["name"] != "read_file" {
		t.Fatalf("name=%v", f["name"])
	}
	id := tc["id"].(string)
	st, al, ok := decodeGigaToolID(id)
	if !ok || st != state || al != alias {
		t.Fatalf("id round trip failed %q %q %v", st, al, ok)
	}
}

func TestMergeDeveloperAndSystem(t *testing.T) {
	b := []byte(`{"model":"giga/GigaChat","messages":[{"role":"system","content":"A"},{"role":"developer","content":"B"},{"role":"user","content":"C"}]}`)
	out, _, err := translateOpenAIToGiga(b)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	_ = json.Unmarshal(out, &v)
	msgs := v["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages=%d", len(msgs))
	}
	first := msgs[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "A\n\nB" {
		t.Fatalf("first=%v", first)
	}
}
