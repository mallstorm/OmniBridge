package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestChatToAnthropicRequestAndBack(t *testing.T) {
	in := []byte(`{"model":"anthropic/claude-test","messages":[{"role":"system","content":"be terse"},{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"weather","parameters":{"type":"object"}}}],"stream":true}`)
	req, stream, err := TranslateRequest(ChatCompletions, Messages, in)
	if err != nil {
		t.Fatal(err)
	}
	if !stream {
		t.Fatal("stream flag lost")
	}
	s := string(req)
	if !strings.Contains(s, `"system":"be terse"`) || !strings.Contains(s, `"input_schema"`) {
		t.Fatalf("translated=%s", s)
	}
	anth := []byte(`{"id":"m1","model":"claude-test","content":[{"type":"text","text":"OK"},{"type":"tool_use","id":"t1","name":"weather","input":{"city":"Berlin"}}],"stop_reason":"tool_use","usage":{"input_tokens":3,"output_tokens":4}}`)
	out, err := TranslateResponse(Messages, ChatCompletions, anth)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"tool_calls"`) || !strings.Contains(string(out), `"total_tokens":7`) {
		t.Fatalf("out=%s", out)
	}
}

func TestResponsesToChatRequest(t *testing.T) {
	in := []byte(`{"model":"x/model","instructions":"sys","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],"tools":[{"type":"function","name":"f","parameters":{"type":"object"}}]}`)
	out, _, err := TranslateRequest(Responses, ChatCompletions, in)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if json.Unmarshal(out, &v) != nil {
		t.Fatal("bad json")
	}
	if _, ok := v["messages"]; !ok {
		t.Fatalf("%s", out)
	}
}

func TestNormalizeAgentJSONRepairsTrailingComma(t *testing.T) {
	in := []byte(`{"choices":[{"message":{"tool_calls":[{"function":{"name":"f","arguments":"{\"a\":1,}"}}]}}]}`)
	out := NormalizeAgentJSON(in)
	if strings.Contains(string(out), `1,}`) {
		t.Fatalf("not repaired: %s", out)
	}
}

func TestToolConversationChatToMessages(t *testing.T) {
	in := []byte(`{"model":"anth/claude","messages":[{"role":"user","content":"weather"},{"role":"assistant","content":null,"tool_calls":[{"id":"call1","type":"function","function":{"name":"weather","arguments":"{\"city\":\"Berlin\"}"}}]},{"role":"tool","tool_call_id":"call1","content":"sunny"}],"tools":[{"type":"function","function":{"name":"weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}]}`)
	out, _, err := TranslateRequest(ChatCompletions, Messages, in)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, `"type":"tool_use"`) || !strings.Contains(s, `"type":"tool_result"`) || !strings.Contains(s, `"tool_use_id":"call1"`) {
		t.Fatalf("tool conversation was not preserved: %s", s)
	}
}

func TestParallelToolResponseAnthropicToResponses(t *testing.T) {
	in := []byte(`{"id":"m","model":"claude","content":[{"type":"tool_use","id":"a","name":"one","input":{"x":1}},{"type":"tool_use","id":"b","name":"two","input":{"y":2}}],"stop_reason":"tool_use","usage":{"input_tokens":2,"output_tokens":3}}`)
	out, err := TranslateResponse(Messages, Responses, in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(out), `"type":"function_call"`) != 2 {
		t.Fatalf("parallel calls lost: %s", out)
	}
}
