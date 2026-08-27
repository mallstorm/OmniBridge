package agentcompat

import (
	"testing"

	"omnibridge/internal/providers"
)

func TestEvaluateAgentReadyGigaChat(t *testing.T) {
	model := providers.Model{
		ID:                "giga/GigaChat-3-Ultra",
		ProviderID:        "giga",
		PreferredProtocol: "chat-completions",
		DoctorVerified:    true,
		DoctorStatus:      "agent_ready",
		AgentReady:        true,
		AgentScore:        97,
		SystemPrompt:      true,
		Streaming:         true,
		ToolCalling:       true,
		ProtocolStatus:    map[string]string{"chat-completions": "agent_ready"},
	}

	report := Evaluate(model, "gigachat")
	if !report.Compatible {
		t.Fatalf("expected compatible, blockers=%v", report.Blockers)
	}
	if !report.Requirements.ToolRoundTrip {
		t.Fatal("expected verified tool round trip")
	}
	foundStateAdapter := false
	for _, adapter := range report.Adapters {
		if adapter == "gigachat_functions_state_id_round_trip" {
			foundStateAdapter = true
			break
		}
	}
	if !foundStateAdapter {
		t.Fatalf("missing GigaChat state adapter: %v", report.Adapters)
	}
}

func TestEvaluateAgentPartialExplainsRoundTripBlocker(t *testing.T) {
	model := providers.Model{
		ID:             "mock/model",
		ProviderID:     "mock",
		DoctorVerified: true,
		DoctorStatus:   "chat_ready",
		SystemPrompt:   true,
		Streaming:      true,
		ToolCalling:    true,
		AgentReady:     false,
	}

	report := Evaluate(model, "openai-compatible")
	if report.Compatible {
		t.Fatal("partial model must not be agent compatible")
	}
	if len(report.Blockers) != 1 || report.Blockers[0] != "tool_round_trip_not_verified" {
		t.Fatalf("blockers=%v", report.Blockers)
	}
}
