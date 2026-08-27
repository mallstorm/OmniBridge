package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"omnibridge/internal/config"
	"omnibridge/internal/protocol"
	"strings"
)

const agentProbeCompletionMarker = "TOOL_OK"

// ProbeAgentV2 starts from the normal deep Model Doctor result and then replaces
// its tool-round-trip evidence with a stricter two-round acceptance probe. A
// second HTTP 2xx is not enough: the model must consume the tool result, emit no
// new tool call and finish with the deterministic TOOL_OK marker.
func (m *Manager) ProbeAgentV2(ctx context.Context, p config.Provider, model Model) ProbeResult {
	res := m.Probe(ctx, p, model, true)
	apiType := strings.TrimSpace(res.SelectedProtocol)
	if apiType == "" {
		res.ToolRoundTrip = false
		res.AgentCompatible = false
		res.DoctorStatus = classifyDoctor(res)
		ApplyDoctorScores(&res)
		return res
	}

	verified, reason := m.verifyAgentRoundTrip(ctx, config.NormalizeProvider(p), model.ID, apiType)
	res.ToolRoundTrip = verified
	res.AgentCompatible = res.Basic && res.Streaming && res.SystemPrompt && res.ToolCalling && verified
	if pr, ok := res.Protocols[apiType]; ok {
		pr.ToolRoundTrip = verified
		pr.AgentCompatible = res.AgentCompatible
		if !verified && reason != "" {
			if pr.Error != "" {
				pr.Error += " | "
			}
			pr.Error += "agent round-trip: " + reason
		}
		res.Protocols[apiType] = pr
	}
	if !verified && reason != "" {
		if res.Error != "" {
			res.Error += " | "
		}
		res.Error += "agent round-trip: " + reason
	}
	res.DoctorStatus = classifyDoctor(res)
	ApplyDoctorScores(&res)
	return res
}

func (m *Manager) verifyAgentRoundTrip(ctx context.Context, p config.Provider, modelID, apiType string) (bool, string) {
	first := agentProbeToolBody(modelID, apiType)
	status, body, err := m.runProbe(ctx, p, apiType, first, false)
	if err != nil {
		return false, err.Error()
	}
	if status/100 != 2 {
		return false, fmt.Sprintf("first round HTTP %d", status)
	}
	if countToolCalls(apiType, body) == 0 {
		return false, "first round produced no tool call"
	}
	follow, ok := toolRoundTripBody(modelID, apiType, body)
	if !ok {
		return false, "could not construct second-round tool result request"
	}
	status, finalBody, err := m.runProbe(ctx, p, apiType, follow, false)
	if err != nil {
		return false, err.Error()
	}
	if status/100 != 2 {
		return false, fmt.Sprintf("second round HTTP %d", status)
	}
	final, err := protocol.ParseResponse(apiType, finalBody)
	if err != nil {
		return false, "second round returned invalid protocol response"
	}
	if len(final.ToolCalls) != 0 {
		return false, "second round requested another tool call"
	}
	if strings.TrimSpace(strings.ToUpper(final.Text)) != agentProbeCompletionMarker {
		return false, fmt.Sprintf("second round did not return %s", agentProbeCompletionMarker)
	}
	return true, ""
}

func agentProbeToolBody(model, apiType string) []byte {
	body := probeBody(model, apiType, "tool")
	var v map[string]any
	if json.Unmarshal(body, &v) != nil {
		return body
	}
	prompt := "Call test_probe with value 7. Do not answer normally before the tool call. After receiving the tool result, reply exactly TOOL_OK and do not call another tool."
	switch apiType {
	case protocol.ChatCompletions, protocol.Messages:
		if messages, ok := v["messages"].([]any); ok && len(messages) > 0 {
			if first, ok := messages[0].(map[string]any); ok {
				first["content"] = prompt
			}
		}
	case protocol.Responses:
		v["input"] = prompt
	}
	out, err := json.Marshal(v)
	if err != nil {
		return body
	}
	return out
}
