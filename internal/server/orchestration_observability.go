package server

import (
	"context"
	"sort"
	"time"

	"omnibridge/internal/observability"
	"omnibridge/internal/orchestration"
)

func annotateWorkflowExecution(ctx context.Context, ex orchestration.Execution) {
	annotateTrace(ctx, func(tr *observability.Trace) {
		if tr.Attributes == nil {
			tr.Attributes = map[string]any{}
		}
		models := map[string]struct{}{}
		providers := map[string]struct{}{}
		families := map[string]struct{}{}
		profiles := map[string]struct{}{}
		fallbackSteps := 0
		var inputTokens, outputTokens, totalTokens int64
		for _, step := range ex.Steps {
			if step.Model != "" {
				models[step.Model] = struct{}{}
			}
			if step.Provider != "" {
				providers[step.Provider] = struct{}{}
			}
			if step.Routing.Family != "" {
				families[step.Routing.Family] = struct{}{}
			}
			if step.Routing.Profile != "" {
				profiles[step.Routing.Profile] = struct{}{}
			}
			if step.Routing.Fallback {
				fallbackSteps++
			}
			inputTokens += step.InputTokens
			outputTokens += step.OutputTokens
			totalTokens += step.TotalTokens
		}
		if totalTokens == 0 {
			totalTokens = inputTokens + outputTokens
		}
		modelList := sortedStringSet(models)
		providerList := sortedStringSet(providers)
		familyList := sortedStringSet(families)
		profileList := sortedStringSet(profiles)

		tr.Attributes["workflow_execution_id"] = ex.ID
		tr.Attributes["workflow_id"] = ex.WorkflowID
		tr.Attributes["workflow_version"] = ex.WorkflowVersion
		tr.Attributes["workflow_status"] = ex.Status
		tr.Attributes["workflow_iterations"] = ex.Iteration
		tr.Attributes["workflow_upstream_calls"] = len(ex.Steps)
		tr.Attributes["workflow_models"] = modelList
		tr.Attributes["workflow_providers"] = providerList
		tr.Attributes["workflow_families"] = familyList
		tr.Attributes["workflow_role_profiles"] = profileList
		tr.Attributes["workflow_provider_count"] = len(providerList)
		tr.Attributes["workflow_family_count"] = len(familyList)
		tr.Attributes["workflow_routing_fallback_steps"] = fallbackSteps
		tr.Attributes["workflow_input_tokens"] = inputTokens
		tr.Attributes["workflow_output_tokens"] = outputTokens
		tr.Attributes["workflow_total_tokens"] = totalTokens
		tr.Attributes["workflow_review_status"] = ex.Review.Status
		if !ex.StartedAt.IsZero() {
			finished := ex.FinishedAt
			if finished.IsZero() {
				finished = time.Now()
			}
			tr.Attributes["workflow_duration_ms"] = finished.Sub(ex.StartedAt).Milliseconds()
		}
		if ex.PersistenceError != "" {
			tr.Attributes["workflow_persistence_error"] = true
		}
		if ex.Error != "" && tr.ErrorKind == "" {
			tr.ErrorKind = "workflow_failed"
		}
	})
}

func sortedStringSet(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
