package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProtocolRegistry(t *testing.T) {
	got := RegisteredProtocols()
	if len(got) != 3 || got[0] != ChatCompletions || got[1] != Messages || got[2] != Responses {
		t.Fatalf("protocol registry=%v", got)
	}
	st := Status()
	if st.Version != "3.1" || !st.SameProtocolNormalization || !st.CanonicalStreamContract || !st.LiveCrossProtocolStreaming {
		t.Fatalf("status=%+v", st)
	}
}

func TestSameProtocolPreservesUnknownAndRepairsAgentSequence(t *testing.T) {
	in := []byte(`{
		"model":"p/m","stream":true,"logprobs":true,"custom_vendor_flag":{"x":1},
		"messages":[
			{"role":"user","content":"run"},
			{"role":"assistant","content":null,"tool_calls":[{"type":"function","function":{"name":"f","arguments":"{\"a\":1,}"}}]},
			{"role":"user","content":"continue"}
		],
		"tools":[{"type":"function","function":{"name":"f","parameters":{"properties":{"a":{"type":"integer"}}}}}]
	}`)
	out, stream, report, err := TranslateRequestDetailed(ChatCompletions, ChatCompletions, in)
	if err != nil {
		t.Fatal(err)
	}
	if !stream {
		t.Fatal("stream flag lost")
	}
	var v map[string]any
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatal(err)
	}
	if v["logprobs"] != true || v["custom_vendor_flag"] == nil {
		t.Fatalf("unknown fields lost: %s", out)
	}
	if report.ToolIDsGenerated != 1 || report.ArgumentsRepaired != 1 || report.MissingToolResults != 1 || !report.UnknownFieldsPreserved {
		t.Fatalf("report=%+v out=%s", report, out)
	}
	messages := v["messages"].([]any)
	if len(messages) != 4 {
		t.Fatalf("expected inserted tool result, got %d: %s", len(messages), out)
	}
	tool := messages[2].(map[string]any)
	if tool["role"] != "tool" || strings.TrimSpace(tool["tool_call_id"].(string)) == "" {
		t.Fatalf("missing repair: %v", tool)
	}
	tools := v["tools"].([]any)
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	schema := fn["parameters"].(map[string]any)
	if schema["type"] != "object" {
		t.Fatalf("schema not coerced: %v", schema)
	}
}

func TestOrphanToolResultIsDetectedWithoutDroppingContent(t *testing.T) {
	in := []byte(`{"model":"p/m","messages":[{"role":"tool","tool_call_id":"missing","content":"important result"},{"role":"user","content":"next"}]}`)
	out, _, report, err := TranslateRequestDetailed(ChatCompletions, ChatCompletions, in)
	if err != nil {
		t.Fatal(err)
	}
	if report.OrphanToolResults != 1 {
		t.Fatalf("report=%+v", report)
	}
	if !strings.Contains(string(out), "important result") || !strings.Contains(string(out), `"role":"user"`) {
		t.Fatalf("orphan content lost: %s", out)
	}
}

func TestStructuredOutputMappingChatToResponses(t *testing.T) {
	in := []byte(`{"model":"p/m","messages":[{"role":"user","content":"x"}],"response_format":{"type":"json_schema","json_schema":{"name":"answer","strict":true,"schema":{"properties":{"ok":{"type":"boolean"}}}}}}`)
	out, _, _, err := TranslateRequestDetailed(ChatCompletions, Responses, in)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	_ = json.Unmarshal(out, &v)
	text := v["text"].(map[string]any)
	format := text["format"].(map[string]any)
	if format["type"] != "json_schema" || format["name"] != "answer" {
		t.Fatalf("format=%v", format)
	}
	if _, nested := format["json_schema"]; nested {
		t.Fatalf("Responses received Chat wrapper: %v", format)
	}
	schema := format["schema"].(map[string]any)
	if schema["type"] != "object" {
		t.Fatalf("schema=%v", schema)
	}
}

func TestReasoningReplayAnthropicToChat(t *testing.T) {
	in := []byte(`{"model":"claude","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"private chain token","signature":"sig"},{"type":"text","text":"answer"}]},{"role":"user","content":"continue"}]}`)
	out, _, _, err := TranslateRequestDetailed(Messages, ChatCompletions, in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"reasoning_content":"private chain token"`) {
		t.Fatalf("reasoning replay lost: %s", out)
	}
}

func TestResponseReasoningAndToolIdNormalization(t *testing.T) {
	in := []byte(`{"id":"m","model":"claude","content":[{"type":"thinking","thinking":"r"},{"type":"tool_use","name":"f","input":{"x":1}}],"stop_reason":"tool_use"}`)
	out, report, err := TranslateResponseDetailed(Messages, ChatCompletions, in)
	if err != nil {
		t.Fatal(err)
	}
	if report.ToolIDsGenerated != 1 {
		t.Fatalf("report=%+v", report)
	}
	if !strings.Contains(string(out), `"reasoning_content":"r"`) || !strings.Contains(string(out), `"tool_calls"`) {
		t.Fatalf("out=%s", out)
	}
}

func TestCanonicalStreamEventContract(t *testing.T) {
	ev := CanonicalStreamEvent{Type: "text_delta", ID: "x", Model: "m", TextDelta: "hi", Usage: CanonicalUsage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3}}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"text_delta":"hi"`) || !strings.Contains(string(b), `"total_tokens":3`) {
		t.Fatalf("event=%s", b)
	}
}

func TestSameProtocolResponsePreservesVendorFieldsAndGeneratesToolID(t *testing.T) {
	in := []byte(`{"id":"x","vendor_trace":"keep","choices":[{"message":{"role":"assistant","tool_calls":[{"type":"function","function":{"name":"f","arguments":"{\"x\":1,}"}}]}}]}`)
	out, report, err := TranslateResponseDetailed(ChatCompletions, ChatCompletions, in)
	if err != nil {
		t.Fatal(err)
	}
	if report.ToolIDsGenerated != 1 || report.ArgumentsRepaired != 1 || !report.UnknownFieldsPreserved {
		t.Fatalf("report=%+v", report)
	}
	if !strings.Contains(string(out), `"vendor_trace":"keep"`) || !strings.Contains(string(out), `"id":"call_ob_`) {
		t.Fatalf("out=%s", out)
	}
}
