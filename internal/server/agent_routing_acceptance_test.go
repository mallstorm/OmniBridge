package server

import (
	"testing"

	obr "omnibridge/internal/router"
)

func TestAutoAgentVSCodeShapedRequestRequiresDoctorProof(t *testing.T) {
	body := []byte(`{
		"model":"auto/agent",
		"messages":[
			{"role":"system","content":"You are an editor agent."},
			{"role":"user","content":"Read README.md and inspect it."},
			{"role":"assistant","content":"","tool_calls":[{"id":"call-1","type":"function","function":{"name":"workbench.action.read_file","arguments":"{\"path\":\"README.md\"}"}}]},
			{"role":"tool","tool_call_id":"call-1","content":"file contents"}
		],
		"tools":[{"type":"function","function":{"name":"workbench.action.read_file","parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}}}],
		"tool_choice":"auto",
		"stream":true
	}`)

	req := obr.AnalyzeRequest(body, "chat-completions")
	req = requirementsForProfile(req, "agent")
	if !req.AgentMode || !req.ToolCalling || !req.Streaming || !req.RequiresDoctorProof {
		t.Fatalf("agent requirements not inferred: %+v", req)
	}

	unverified := obr.Candidate{ID: "fast-unverified", ToolCalling: true, Streaming: true, SystemPrompt: true, AgentReady: true, DoctorVerified: false, ContextWindow: 128000}
	first := obr.CheckCompatibility(unverified, req, true)
	if first.Compatible {
		t.Fatalf("unverified candidate must fail closed: %+v", first)
	}

	primary := obr.Candidate{ID: "agent-a", ProviderID: "p1", ToolCalling: true, Streaming: true, SystemPrompt: true, AgentReady: true, DoctorVerified: true, ContextWindow: 128000, AgentScore: 95, ReliabilityScore: 90}
	fallback := obr.Candidate{ID: "agent-b", ProviderID: "p2", ToolCalling: true, Streaming: true, SystemPrompt: true, AgentReady: true, DoctorVerified: true, ContextWindow: 128000, AgentScore: 90, ReliabilityScore: 92}
	for _, candidate := range []obr.Candidate{primary, fallback} {
		check := obr.CheckCompatibility(candidate, req, true)
		if !check.Compatible {
			t.Fatalf("Doctor-proven fallback candidate rejected: %s reasons=%v", candidate.ID, check.Reasons)
		}
	}
}
