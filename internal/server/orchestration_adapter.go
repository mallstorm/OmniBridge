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

	"omnibridge/internal/contextguard"
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

	// Re-check hard budget before every orchestration step. Execute is invoked
	// separately for Developer, Critic, Fixer and each Reviewer iteration, so a
	// workflow cannot continue spending after an earlier step exhausted budget.
	if x.server.QuotaCost != nil {
		x.server.QuotaCost.UpdateConfig(x.server.Config.Get().QuotaCost)
		if reject, reason := x.server.QuotaCost.ShouldRejectForBudget(); reject {
			return orchestration.CallResult{}, fmt.Errorf("workflow_budget_exceeded: %s", reason)
		}
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

		// Apply the existing Context Guard only after routing, when the concrete
		// model context window is known. Each fallback candidate is evaluated
		// independently because context limits may differ between providers.
		candidateBody := body
		if x.server.Config != nil {
			guarded, _, guardErr := contextguard.Apply(body, "chat-completions", candidate.ContextWindow, x.server.Config.Get().ContextGuard)
			if guardErr != nil {
				lastErr = fmt.Errorf("context guard failed for %s: %w", candidate.ID, guardErr)
				continue
			}
			candidateBody = guarded
		}

		if !x.server.acquireModelResilience(candidate) {
			continue
		}
		started := time.Now()
		result, class, err := x.executeCandidate(ctx, candidate, candidateBody)
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
		"model":  model,
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

func (s *Server) orchestrationModelEntries() []map[string]any {
	workflows := orchestration.NewRegistry().List()
	out := make([]map[string]any, 0, len(workflows))
	for _, wf := range workflows {
		out = append(out, map[string]any{
			"id":       wf.ID,
			"object":   "model",
			"owned_by": "omnibridge-orchestration",
			"type":     "workflow",
		})
	}
	return out
}

func (s *Server) orchestrationStatus(w http.ResponseWriter, r *http.Request) {
	registry := orchestration.NewRegistry()
	jsonOut(w, http.StatusOK, map[string]any{
		"version":   "1.1",
		"enabled":   true,
		"workflows": len(registry.List()),
		"models":    s.orchestrationModelEntries(),
		"scope": map[string]any{
			"project_memory":                false,
			"multi_agent_workspace_editing": false,
			"visual_workflow_editor":         false,
		},
	})
}

func (s *Server) orchestrationWorkflows(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, http.StatusOK, orchestration.NewRegistry().List())
}

func (s *Server) orchestrationEvaluate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Workflow string `json:"workflow"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(in.Workflow) == "" {
		in.Workflow = orchestration.CodingReviewModel
	}
	wf, ok := orchestration.NewRegistry().Get(in.Workflow)
	if !ok {
		jsonOut(w, http.StatusNotFound, map[string]any{"error": map[string]any{"message": "workflow not found", "type": "workflow_not_found", "code": "workflow_not_found"}})
		return
	}
	steps := make([]map[string]any, 0, len(wf.Steps))
	for _, step := range wf.Steps {
		req := obr.Requirements{Reasoning: step.Selector.PreferReasoning, ToolCalling: step.Selector.RequireTools, RequiresDoctorProof: step.Selector.RequireDoctorProof}
		selector := step.Selector.Model
		if strings.TrimSpace(selector) == "" {
			selector = roleDefaultModel(step.Role)
		}
		models, err := s.routingCandidatesForRequest(selector, "", req)
		candidates := make([]map[string]any, 0, len(models))
		for i, m := range models {
			candidates = append(candidates, map[string]any{
				"rank": i + 1, "model": m.ID, "provider": m.ProviderID, "context_window": m.ContextWindow,
			})
		}
		entry := map[string]any{
			"id": step.ID, "role": step.Role, "selector": selector, "candidates": candidates, "permission": step.Permission,
			"requirements": map[string]any{"reasoning": req.Reasoning, "tool_calling": req.ToolCalling, "doctor_proof": req.RequiresDoctorProof},
			"diversity_policy": wf.Policy.Diversity,
		}
		if len(models) > 0 {
			entry["selected_model"] = models[0].ID
			entry["selected_provider"] = models[0].ProviderID
		}
		if err != nil {
			entry["error"] = err.Error()
		}
		steps = append(steps, entry)
	}
	jsonOut(w, http.StatusOK, map[string]any{"workflow": wf.ID, "version": wf.Version, "policy": wf.Policy, "steps": steps, "dry_run": true})
}

// handleWorkflowProxy intercepts only explicit workflow/* models. It returns
// true when the request belongs to orchestration and has been fully handled.
func (s *Server) handleWorkflowProxy(w http.ResponseWriter, r *http.Request, apiType string, body []byte, model string) bool {
	if !orchestration.IsWorkflowModel(model) {
		return false
	}
	if apiType != "chat-completions" {
		jsonOut(w, http.StatusNotImplemented, map[string]any{"error": map[string]any{"message": "workflow models currently support /v1/chat/completions only", "type": "workflow_protocol_not_supported", "code": "workflow_protocol_not_supported"}})
		return true
	}

	canonical, err := protocol.ParseRequest(protocol.ChatCompletions, body)
	if err != nil {
		jsonOut(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"message": err.Error(), "type": "workflow_request_invalid", "code": "workflow_request_invalid"}})
		return true
	}
	if len(canonical.Tools) > 0 {
		jsonOut(w, http.StatusUnprocessableEntity, map[string]any{"error": map[string]any{"message": "workspace/tool execution is intentionally outside orchestration v1", "type": "workflow_tools_not_supported", "code": "workflow_tools_not_supported"}})
		return true
	}
	input := canonicalWorkflowInput(canonical)
	if strings.TrimSpace(input) == "" {
		jsonOut(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"message": "workflow request contains no textual input", "type": "workflow_request_invalid", "code": "workflow_request_invalid"}})
		return true
	}

	executionID := fmt.Sprintf("wf-%d", time.Now().UnixNano())
	ex, err := newOrchestrationEngine(s).Run(r.Context(), model, orchestration.Request{ID: executionID, Input: input})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || r.Context().Err() != nil {
			return true
		}
		code := "workflow_failed"
		status := http.StatusBadGateway
		if strings.Contains(err.Error(), "workflow_not_found") {
			code, status = "workflow_not_found", http.StatusNotFound
		} else if strings.Contains(err.Error(), "workflow_no_candidate") {
			code, status = "workflow_no_candidate", http.StatusUnprocessableEntity
		} else if strings.Contains(err.Error(), "workflow_budget_exceeded") {
			code, status = "workflow_budget_exceeded", http.StatusPaymentRequired
		}
		jsonOut(w, status, map[string]any{"error": map[string]any{"message": err.Error(), "type": code, "code": code}, "execution_id": ex.ID})
		return true
	}

	response := workflowChatCompletion(model, ex)
	if canonical.Stream {
		sse, sseErr := protocol.SyntheticSSE(protocol.ChatCompletions, response)
		if sseErr != nil {
			jsonOut(w, http.StatusBadGateway, map[string]any{"error": map[string]any{"message": sseErr.Error(), "type": "workflow_stream_encode_failed", "code": "workflow_stream_encode_failed"}})
			return true
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(sse)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		return true
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-OmniBridge-Workflow-Execution", ex.ID)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(response)
	return true
}

func canonicalWorkflowInput(req protocol.CanonicalRequest) string {
	var b strings.Builder
	if strings.TrimSpace(req.System) != "" {
		b.WriteString("System context:\n")
		b.WriteString(strings.TrimSpace(req.System))
		b.WriteString("\n\n")
	}
	for _, message := range req.Messages {
		text := canonicalPartsText(message.Parts)
		if strings.TrimSpace(text) == "" {
			continue
		}
		b.WriteString(strings.ToUpper(message.Role))
		b.WriteString(":\n")
		b.WriteString(text)
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String())
}

func canonicalPartsText(parts []protocol.CanonicalPart) string {
	var b strings.Builder
	for _, part := range parts {
		if strings.TrimSpace(part.Text) == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(part.Text)
	}
	return b.String()
}

func workflowChatCompletion(model string, ex orchestration.Execution) []byte {
	var inputTokens, outputTokens, totalTokens int64
	for _, step := range ex.Steps {
		inputTokens += step.InputTokens
		outputTokens += step.OutputTokens
		totalTokens += step.TotalTokens
	}
	if totalTokens == 0 {
		totalTokens = inputTokens + outputTokens
	}
	payload := map[string]any{
		"id":      ex.ID,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": ex.FinalResult},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{
			"prompt_tokens":     inputTokens,
			"completion_tokens": outputTokens,
			"total_tokens":      totalTokens,
		},
	}
	out, _ := json.Marshal(payload)
	return out
}
