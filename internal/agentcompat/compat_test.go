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
		ToolRoundTrip:     true,
		ProtocolStatus:    map[string]string{"chat-completions": "agent_ready"},
	}

	report := Evaluate(model, "gigachat")
	if !report.Compatible {
		t.Fatalf("expected compatible, blockers=%v", report.Blockers)
	}
	if !report.Requirements.ToolRoundTrip || report.ToolRoundTripSource != "model_doctor" {
		t.Fatalf("expected explicit doctor round-trip evidence, report=%+v", report)
	}
	foundStateAdapter := false
	foundStreamAdapter := false
	for _, adapter := range report.Adapters {
		switch adapter {
		case "gigachat_functions_state_id_round_trip":
			foundStateAdapter = true
		case "gigachat_streaming_function_translation":
			foundStreamAdapter = true
		}
	}
	if !foundStateAdapter || !foundStreamAdapter {
		t.Fatalf("missing GigaChat adapters: %v", report.Adapters)
	}
}

func TestEvaluateLegacyAgentReadyBackfillsRoundTripEvidence(t *testing.T) {
	model := providers.Model{
		ID:             "legacy/model",
		ProviderID:     "legacy",
		DoctorVerified: true,
		SystemPrompt:   true,
		Streaming:      true,
		ToolCalling:    true,
		AgentReady:     true,
	}
	report := Evaluate(model, "openai-compatible")
	if !report.Compatible {
		t.Fatalf("legacy Doctor-verified agent-ready model must remain compatible: %v", report.Blockers)
	}
	if report.ToolRoundTripSource != "legacy_agent_ready_backfill" {
		t.Fatalf("source=%q", report.ToolRoundTripSource)
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

func TestEvaluateRequiresPersistedAgentReadyVerdict(t *testing.T) {
	model := providers.Model{
		ID:             "mock/model",
		ProviderID:     "mock",
		DoctorVerified: true,
		SystemPrompt:   true,
		Streaming:      true,
		ToolCalling:    true,
		ToolRoundTrip:  true,
		AgentReady:     false,
	}
	report := Evaluate(model, "openai-compatible")
	if report.Compatible {
		t.Fatal("missing AgentReady verdict must fail closed")
	}
	if len(report.Blockers) != 1 || report.Blockers[0] != "agent_ready_verdict_missing" {
		t.Fatalf("blockers=%v", report.Blockers)
	}
}
