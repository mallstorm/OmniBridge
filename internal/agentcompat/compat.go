package agentcompat

import (
	"sort"
	"strings"

	"omnibridge/internal/providers"
)

const Version = "1.1"

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
	Version             string           `json:"version"`
	ModelID             string           `json:"model_id"`
	ProviderID          string           `json:"provider_id"`
	ProviderKind        string           `json:"provider_kind,omitempty"`
	PreferredProtocol   string           `json:"preferred_protocol,omitempty"`
	DoctorStatus        string           `json:"doctor_status,omitempty"`
	AgentScore          float64          `json:"agent_score"`
	Compatible          bool             `json:"compatible"`
	Requirements        Requirements     `json:"requirements"`
	ToolRoundTripSource string           `json:"tool_round_trip_source,omitempty"`
	Protocols           []ProtocolStatus `json:"protocols,omitempty"`
	Adapters            []string         `json:"adapters"`
	Blockers            []string         `json:"blockers,omitempty"`
}

// Evaluate projects persisted Model Doctor evidence into an explicit Agent
// Compatibility contract. Capabilities are never inferred from model names or
// provider marketing metadata. The only compatibility bridge for old registry
// snapshots is a previously Doctor-verified AgentReady verdict, because older
// OmniBridge versions did not persist ToolRoundTrip as a separate Model field.
func Evaluate(model providers.Model, providerKind string) Report {
	toolRoundTrip := model.ToolRoundTrip
	toolRoundTripSource := "model_doctor"
	if !toolRoundTrip && model.DoctorVerified && model.AgentReady {
		toolRoundTrip = true
		toolRoundTripSource = "legacy_agent_ready_backfill"
	}
	req := Requirements{
		DoctorVerified: model.DoctorVerified,
		SystemPrompt:   model.SystemPrompt,
		Streaming:      model.Streaming,
		ToolCalling:    model.ToolCalling,
		ToolRoundTrip:  toolRoundTrip,
	}

	report := Report{
		Version:             Version,
		ModelID:             model.ID,
		ProviderID:          model.ProviderID,
		ProviderKind:        strings.ToLower(strings.TrimSpace(providerKind)),
		PreferredProtocol:   model.PreferredProtocol,
		DoctorStatus:        model.DoctorStatus,
		AgentScore:          model.AgentScore,
		Requirements:        req,
		ToolRoundTripSource: toolRoundTripSource,
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
			"gigachat_schema_sanitization",
			"gigachat_tool_choice_to_function_call",
			"gigachat_functions_state_id_round_trip",
			"gigachat_function_role_translation",
			"gigachat_streaming_function_translation",
		)
	}

	if !req.DoctorVerified {
		report.Blockers = append(report.Blockers, "doctor_not_verified")
	}
	if !req.SystemPrompt {
		report.Blockers = append(report.Blockers, "system_prompt_not_verified")
	}
	if !req.Streaming {
		report.Blockers = append(report.Blockers, "streaming_not_verified")
	}
	if !req.ToolCalling {
		report.Blockers = append(report.Blockers, "tool_calling_not_verified")
	}
	if !req.ToolRoundTrip {
		report.Blockers = append(report.Blockers, "tool_round_trip_not_verified")
	}
	if len(report.Blockers) == 0 && !model.AgentReady {
		report.Blockers = append(report.Blockers, "agent_ready_verdict_missing")
	}

	report.Compatible = len(report.Blockers) == 0 && model.AgentReady
	return report
}
