package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"omnibridge/internal/orchestration"
	"omnibridge/internal/protocol"
	"omnibridge/internal/providers"
	"omnibridge/internal/resilience"
	obr "omnibridge/internal/router"
)

// orchestrationExecutor adapts the existing Server routing/provider/protocol
// pipeline to the provider-neutral orchestration.Executor contract. It never
// calls the public HTTP data plane recursively.
type orchestrationExecutor struct {
	server *Server
}

func newOrchestrationEngine(s *Server) *orchestration.Engine {
	return &orchestration.Engine{
		Registry: orchestration.NewRegistry(),
		Executor: orchestrationExecutor{server: s},
		Verdicts: orchestration.StrictVerdictParser{},
	}
}

func (x orchestrationExecutor) Execute(ctx context.Context, call orchestration.Call) (orchestration.CallResult, error) {
	if x.server == nil {
		return orchestration.CallResult{}, errors.New("orchestration server is not configured")
	}
	if err := ctx.Err(); err != nil {
		return orchestration.CallResult{}, err
	}

	selector := strings.TrimSpace(call.Step.Selector.Model)
	if selector == "" {
		selector = roleDefaultModel(call.Step.Role)
	}

	body, err := orchestrationChatBody(selector, call)
	if err != nil {
		return orchestration.CallResult{}, fmt.Errorf("workflow_step_request_invalid: %w", err)
	}

	requirements := obr.AnalyzeRequest(body, "chat-completions")
	if call.Step.Selector.PreferReasoning {
		requirements.Reasoning = true
	}
	if call.Step.Selector.RequireTools {
		requirements.ToolCalling = true
	}
	if call.Step.Selector.RequireDoctorProof {
		requirements.RequiresDoctorProof = true
	}

	candidates, err := x.server.routingCandidatesForRequest(selector, "", requirements)
	if err != nil {
		return orchestration.CallResult{}, fmt.Errorf("workflow_no_candidate: %w", err)
	}
	candidates = applyOrchestrationDiversity(candidates, call)
	if len(candidates) == 0 {
		return orchestration.CallResult{}, errors.New("workflow_no_candidate: no candidate remains after routing")
	}

	x.server.refreshResilienceConfig()
	var lastErr error
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return orchestration.CallResult{}, err
		}
		if !x.server.acquireModelResilience(candidate) {
			continue
		}
		started := time.Now()
		result, class, err := x.executeCandidate(ctx, candidate, body)
		if err == nil {
			x.server.recordResilienceSuccess(candidate)
			x.server.recordUsageCost(ctx, candidate, false, result.InputTokens, result.OutputTokens, result.TotalTokens, time.Since(started).Milliseconds())
			return result, nil
		}
		lastErr = err
		if class.Kind != "" {
			x.server.recordResilienceFailure(candidate, class)
		}
		x.server.recordUsageCost(ctx, candidate, true, 0, 0, 0, time.Since(started).Milliseconds())
		if !fallbackEligible(class) {
			break
		}
	}
	if lastErr == nil {
		lastErr = errors.New("all orchestration routing candidates are blocked by resilience policy")
	}
	return orchestration.CallResult{}, fmt.Errorf("workflow_step_failed: %w", lastErr)
}

func (x orchestrationExecutor) executeCandidate(ctx context.Context, candidate providers.Model, body []byte) (orchestration.CallResult, resilience.Classification, error) {
	call, err := x.server.upstream(ctx, candidate, "chat-completions", body)
	if x.server.QuotaCost != nil && call.Header != nil {
		x.server.QuotaCost.ObserveHeaders(candidate.ProviderID, call.Header)
	}
	if err != nil {
		class := resilience.Classify(call.Status, nil, call.Header, err, ctx.Err())
		return orchestration.CallResult{}, class, err
	}
	if call.Body == nil {
		err := errors.New("empty upstream response")
		class := resilience.Classify(call.Status, nil, call.Header, err, ctx.Err())
		return orchestration.CallResult{}, class, err
	}
	defer call.Body.Close()

	errorBody := captureUpstreamErrorBody(&call)
	if call.Status/100 != 2 {
		class := resilience.Classify(call.Status, errorBody, call.Header, nil, ctx.Err())
		return orchestration.CallResult{}, class, fmt.Errorf("upstream status %d", call.Status)
	}

	raw, err := io.ReadAll(io.LimitReader(call.Body, 32<<20))
	if err != nil {
		class := resilience.Classify(http.StatusBadGateway, nil, call.Header, err, ctx.Err())
		return orchestration.CallResult{}, class, err
	}
	if call.CrossProtocol {
		raw, err = protocol.TranslateResponse(call.TargetProtocol, "chat-completions", raw)
		if err != nil {
			class := resilience.Classify(http.StatusBadGateway, nil, call.Header, err, ctx.Err())
			return orchestration.CallResult{}, class, fmt.Errorf("protocol translation failed: %w", err)
		}
	}
	raw = protocol.NormalizeAgentJSON(raw)
	text, err := extractChatCompletionText(raw)
	if err != nil {
		return orchestration.CallResult{}, resilience.Classification{}, err
	}
	in, out, total, _ := extractUsageDetailed(raw)
	return orchestration.CallResult{
		Model:        candidate.ID,
		Provider:     candidate.ProviderID,
		Output:       text,
		InputTokens:  in,
		OutputTokens: out,
		TotalTokens:  total,
	}, resilience.Classification{}, nil
}

func roleDefaultModel(role orchestration.Role) string {
	switch role {
	case orchestration.RoleDeveloper, orchestration.RoleFixer:
		return "auto/coding"
	case orchestration.RoleCritic:
		return "auto/reasoning"
	default:
		return "auto/smart"
	}
}

func orchestrationChatBody(model string, call orchestration.Call) ([]byte, error) {
	system := orchestrationRolePrompt(call.Step.Role)
	payload := map[string]any{
		"model": model,
		"stream": false,
		"messages": []map[string]any{
			{"role": "system", "content": system},
			{"role": "user", "content": call.Input},
		},
	}
	return json.Marshal(payload)
}

func orchestrationRolePrompt(role orchestration.Role) string {
	switch role {
	case orchestration.RoleDeveloper:
		return "You are the Developer in a bounded OmniBridge workflow. Produce a complete candidate solution for the user's request. Return only the candidate result, not workflow commentary."
	case orchestration.RoleCritic:
		return "You are the Critic in a bounded OmniBridge workflow. Identify concrete defects, omissions, security, performance and edge-case issues in the candidate. Do not rewrite the candidate."
	case orchestration.RoleFixer:
		return "You are the Fixer in a bounded OmniBridge workflow. Correct the candidate using the supplied critique and reviewer issues. Return only the corrected candidate."
	case orchestration.RoleReviewer:
		return "You are the final quality gate. Your response MUST begin with exactly PASS or FAIL. PASS means there are no blocking issues. FAIL must be followed by concrete blocking issues. Do not expose hidden chain-of-thought."
	default:
		return "Complete the requested workflow step."
	}
}

func applyOrchestrationDiversity(candidates []providers.Model, call orchestration.Call) []providers.Model {
	if call.Workflow.Policy.Diversity == orchestration.DiversityNone || len(candidates) < 2 {
		return candidates
	}
	avoidProvider := make(map[string]bool, len(call.AvoidProviders))
	avoidModel := make(map[string]bool, len(call.AvoidModels))
	for _, id := range call.AvoidProviders {
		avoidProvider[id] = true
	}
	for _, id := range call.AvoidModels {
		avoidModel[id] = true
	}
	preferred := make([]providers.Model, 0, len(candidates))
	fallback := make([]providers.Model, 0, len(candidates))
	for _, m := range candidates {
		avoid := false
		switch call.Workflow.Policy.Diversity {
		case orchestration.DiversityProvider, orchestration.DiversityFamily:
			avoid = avoidProvider[m.ProviderID]
		case orchestration.DiversityModel:
			avoid = avoidModel[m.ID]
		}
		if avoid {
			fallback = append(fallback, m)
		} else {
			preferred = append(preferred, m)
		}
	}
	// Diversity is a preference, not a hard availability gate.
	return append(preferred, fallback...)
}

func extractChatCompletionText(raw []byte) (string, error) {
	var envelope struct {
		Choices []struct {
			Message struct {
				Content any `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "", fmt.Errorf("invalid chat completion response: %w", err)
	}
	if len(envelope.Choices) == 0 {
		return "", errors.New("chat completion contains no choices")
	}
	switch content := envelope.Choices[0].Message.Content.(type) {
	case string:
		if strings.TrimSpace(content) == "" {
			return "", errors.New("chat completion contains empty content")
		}
		return content, nil
	case []any:
		var b strings.Builder
		for _, item := range content {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text, _ := m["text"].(string); text != "" {
				b.WriteString(text)
			}
		}
		if strings.TrimSpace(b.String()) == "" {
			return "", errors.New("chat completion contains no text content")
		}
		return b.String(), nil
	default:
		return "", errors.New("chat completion content has unsupported shape")
	}
}
