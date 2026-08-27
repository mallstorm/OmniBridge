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
		var inputTokens, outputTokens, totalTokens int64
		for _, step := range ex.Steps {
			if step.Model != "" {
				models[step.Model] = struct{}{}
			}
			if step.Provider != "" {
				providers[step.Provider] = struct{}{}
			}
			inputTokens += step.InputTokens
			outputTokens += step.OutputTokens
			totalTokens += step.TotalTokens
		}
		if totalTokens == 0 {
			totalTokens = inputTokens + outputTokens
		}
		modelList := make([]string, 0, len(models))
		for id := range models { modelList = append(modelList, id) }
		providerList := make([]string, 0, len(providers))
		for id := range providers { providerList = append(providerList, id) }
		sort.Strings(modelList)
		sort.Strings(providerList)

		tr.Attributes["workflow_execution_id"] = ex.ID
		tr.Attributes["workflow_id"] = ex.WorkflowID
		tr.Attributes["workflow_version"] = ex.WorkflowVersion
		tr.Attributes["workflow_status"] = ex.Status
		tr.Attributes["workflow_iterations"] = ex.Iteration
		tr.Attributes["workflow_upstream_calls"] = len(ex.Steps)
		tr.Attributes["workflow_models"] = modelList
		tr.Attributes["workflow_providers"] = providerList
		tr.Attributes["workflow_input_tokens"] = inputTokens
		tr.Attributes["workflow_output_tokens"] = outputTokens
		tr.Attributes["workflow_total_tokens"] = totalTokens
		tr.Attributes["workflow_review_status"] = ex.Review.Status
		if !ex.StartedAt.IsZero() {
			finished := ex.FinishedAt
			if finished.IsZero() { finished = time.Now() }
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
