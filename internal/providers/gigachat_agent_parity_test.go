package providers

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGigaAgentForcedToolChoiceAndMultiRoundHistory(t *testing.T) {
	originalName := "workbench.action.read_file"
	state := "state-123"
	first := map[string]any{
		"model": "giga/GigaChat-3-Ultra",
		"messages": []any{map[string]any{"role": "user", "content": "read file"}},
		"tools": []any{map[string]any{"type": "function", "function": map[string]any{
			"name": originalName,
			"parameters": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []any{"path"}},
		}}},
		"tool_choice": map[string]any{"type": "function", "function": map[string]any{"name": originalName}},
	}
	raw, _ := json.Marshal(first)
	translated, reverse, err := translateOpenAIToGiga(raw)
	if err != nil {
		t.Fatal(err)
	}
	var req map[string]any
	if err := json.Unmarshal(translated, &req); err != nil {
		t.Fatal(err)
	}
	forced, ok := req["function_call"].(map[string]any)
	if !ok {
		t.Fatalf("function_call=%#v", req["function_call"])
	}
	alias, _ := forced["name"].(string)
	if alias == "" || reverse[alias] != originalName {
		t.Fatalf("forced alias=%q reverse=%v", alias, reverse)
	}

	callID := encodeGigaToolID(state, alias)
	second := map[string]any{
		"model": "giga/GigaChat-3-Ultra",
		"messages": []any{
			map[string]any{"role": "user", "content": "read file"},
			map[string]any{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{"id": callID, "type": "function", "function": map[string]any{"name": originalName, "arguments": `{"path":"README.md"}`}}}},
			map[string]any{"role": "tool", "tool_call_id": callID, "name": originalName, "content": "file contents"},
		},
		"tools": first["tools"],
	}
	raw2, _ := json.Marshal(second)
	translated2, _, err := translateOpenAIToGiga(raw2)
	if err != nil {
		t.Fatal(err)
	}
	var req2 map[string]any
	if err := json.Unmarshal(translated2, &req2); err != nil {
		t.Fatal(err)
	}
	msgs, _ := req2["messages"].([]any)
	var sawAssistantState, sawFunctionResult bool
	for _, item := range msgs {
		msg, _ := item.(map[string]any)
		switch msg["role"] {
		case "assistant":
			if msg["functions_state_id"] == state {
				sawAssistantState = true
			}
		case "function":
			if msg["function_name"] == alias {
				sawFunctionResult = true
				content, _ := msg["content"].(string)
				if !strings.Contains(content, "file contents") || !json.Valid([]byte(content)) {
					t.Fatalf("function result must be JSON-safe string, got %#v", msg["content"])
				}
			}
		}
	}
	if !sawAssistantState || !sawFunctionResult {
		t.Fatalf("multi-round translation lost state/result: %s", translated2)
	}
}

func TestGigaAgentStreamingPayloadRestoresToolNameAndState(t *testing.T) {
	orig := "read_file"
	alias := safeGigaAlias(orig)
	state := "stream-state"
	payload := map[string]any{
		"id": "chunk-1",
		"choices": []any{map[string]any{
			"finish_reason": "function_call",
			"delta": map[string]any{
				"role": "assistant",
				"functions_state_id": state,
				"function_call": map[string]any{"name": alias, "arguments": `{"path":"x"}`},
			},
		}},
	}
	raw, _ := json.Marshal(payload)
	out, err := translateGigaJSON(raw, map[string]string{alias: orig})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	choice := got["choices"].([]any)[0].(map[string]any)
	if choice["finish_reason"] != "tool_calls" {
		t.Fatalf("finish_reason=%v", choice["finish_reason"])
	}
	delta := choice["delta"].(map[string]any)
	calls := delta["tool_calls"].([]any)
	fn := calls[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != orig {
		t.Fatalf("tool name=%v", fn["name"])
	}
	id := calls[0].(map[string]any)["id"].(string)
	decodedState, decodedAlias, ok := decodeGigaToolID(id)
	if !ok || decodedState != state || decodedAlias != alias {
		t.Fatalf("stream tool id round-trip failed: state=%q alias=%q ok=%v", decodedState, decodedAlias, ok)
	}
}
