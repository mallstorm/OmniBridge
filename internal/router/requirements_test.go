package router

import "testing"

func TestAnalyzeRequestCapabilities(t *testing.T) {
	body := []byte(`{
		"model":"auto/smart",
		"stream":true,
		"reasoning_effort":"low",
		"max_tokens":2048,
		"messages":[
			{"role":"system","content":"You are a coding agent"},
			{"role":"user","content":[{"type":"text","text":"inspect this"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]},
			{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"read","arguments":"{}"}}]},
			{"role":"tool","tool_call_id":"c1","content":"ok"}
		],
		"tools":[{"type":"function","function":{"name":"read","parameters":{"type":"object"}}}],
		"parallel_tool_calls":true,
		"response_format":{"type":"json_schema","json_schema":{"name":"x","schema":{"type":"object"}}}
	}`)
	r := AnalyzeRequest(body, "chat-completions")
	if !r.Streaming || !r.SystemPrompt || !r.ToolCalling || !r.ParallelTools || !r.StructuredOutput || !r.Vision || !r.Reasoning || !r.AgentMode || !r.RequiresDoctorProof {
		t.Fatalf("unexpected requirements: %+v", r)
	}
	if r.RequestedOutput != 2048 || r.MinimumContext <= 2048 || r.EstimatedInput <= 0 {
		t.Fatalf("bad token preflight: %+v", r)
	}
}

func TestAnalyzeRequestSimpleChatDoesNotRequireDoctorProof(t *testing.T) {
	r := AnalyzeRequest([]byte(`{"model":"auto/smart","messages":[{"role":"user","content":"hello"}]}`), "chat-completions")
	if r.RequiresDoctorProof || r.ToolCalling || r.Vision || r.Reasoning || r.AgentMode {
		t.Fatalf("simple chat should not require deep doctor proof: %+v", r)
	}
	if r.RequestedOutput != 1024 {
		t.Fatalf("expected default output reserve, got %d", r.RequestedOutput)
	}
}

func TestCompatibilityAutoFailsClosedForUnverifiedToolRequest(t *testing.T) {
	c := Candidate{ID: "m", ToolCalling: true, Streaming: true}
	r := Requirements{ToolCalling: true, RequiresDoctorProof: true}
	got := CheckCompatibility(c, r, true)
	if got.Compatible {
		t.Fatalf("auto route should require doctor proof: %+v", got)
	}
}

func TestCompatibilityExplicitRouteTreatsUnverifiedCapabilityAsUnknown(t *testing.T) {
	c := Candidate{ID: "m"}
	r := Requirements{ToolCalling: true, RequiresDoctorProof: true}
	got := CheckCompatibility(c, r, false)
	if !got.Compatible || len(got.Warnings) == 0 {
		t.Fatalf("explicit unverified route should warn, not block: %+v", got)
	}
}
