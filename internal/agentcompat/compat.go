package agentcompat

import (
	"sort"
	"strings"

	"omnibridge/internal/providers"
)

const Version = "1.0"

type ProtocolStatus struct {
	Protocol string `json:"protocol"`
	Status   string `json:"status"`
}

type Requirements struct {
	DoctorVerified bool `json:"doctor_verified"`
	SystemPrompt   bool `json:"system_prompt"`
	Streaming      bool `json:"streaming"`
	ToolCalling    bool `json:"tool_calling"`
	ToolRoundTrip  bool `json:"tool_round_trip"`
}

type Report struct {
	Version           string           `json:"version"`
	ModelID           string           `json:"model_id"`
	ProviderID        string           `json:"provider_id"`
	ProviderKind      string           `json:"provider_kind,omitempty"`
	PreferredProtocol string           `json:"preferred_protocol,omitempty"`
	DoctorStatus      string           `json:"doctor_status,omitempty"`
	AgentScore        float64          `json:"agent_score"`
	Compatible        bool             `json:"compatible"`
	Requirements      Requirements     `json:"requirements"`
	Protocols         []ProtocolStatus `json:"protocols,omitempty"`
	Adapters          []string         `json:"adapters"`
	Blockers          []string         `json:"blockers,omitempty"`
}

// Evaluate projects Model Doctor evidence into an explicit Agent Compatibility
// contract. It does not infer unsupported capabilities from model names or
// provider marketing metadata: compatibility remains fail-closed on Doctor
// evidence and the persisted AgentReady verdict.
func Evaluate(model providers.Model, providerKind string) Report {
	toolRoundTrip := model.AgentReady
	req := Requirements{
		DoctorVerified: model.DoctorVerified,
		SystemPrompt:   model.SystemPrompt,
		Streaming:      model.Streaming,
		ToolCalling:    model.ToolCalling,
		ToolRoundTrip:  toolRoundTrip,
	}

	report := Report{
		Version:           Version,
		ModelID:           model.ID,
		ProviderID:        model.ProviderID,
		ProviderKind:      strings.ToLower(strings.TrimSpace(providerKind)),
		PreferredProtocol: model.PreferredProtocol,
		DoctorStatus:      model.DoctorStatus,
		AgentScore:        model.AgentScore,
		Requirements:      req,
		Adapters: []string{
			"canonical_tool_calls",
			"tool_argument_repair",
			"finish_reason_normalization",
			"provider_role_translation",
			"streaming_tool_events",
		},
	}

	keys := make([]string, 0, len(model.ProtocolStatus))
	for protocol := range model.ProtocolStatus {
		keys = append(keys, protocol)
	}
	sort.Strings(keys)
	for _, protocol := range keys {
		report.Protocols = append(report.Protocols, ProtocolStatus{Protocol: protocol, Status: model.ProtocolStatus[protocol]})
	}

	if report.ProviderKind == "gigachat" {
		report.Adapters = append(report.Adapters,
			"gigachat_safe_function_aliases",
			"gigachat_tool_choice_to_function_call",
			"gigachat_functions_state_id_round_trip",
			"gigachat_function_role_translation",
		)
	}

	if !model.DoctorVerified {
		report.Blockers = append(report.Blockers, "doctor_not_verified")
	}
	if !model.SystemPrompt {
		report.Blockers = append(report.Blockers, "system_prompt_not_verified")
	}
	if !model.Streaming {
		report.Blockers = append(report.Blockers, "streaming_not_verified")
	}
	if !model.ToolCalling {
		report.Blockers = append(report.Blockers, "tool_calling_not_verified")
	}
	if model.DoctorVerified && model.SystemPrompt && model.Streaming && model.ToolCalling && !model.AgentReady {
		report.Blockers = append(report.Blockers, "tool_round_trip_not_verified")
	}

	report.Compatible = len(report.Blockers) == 0 && model.AgentReady
	return report
}
