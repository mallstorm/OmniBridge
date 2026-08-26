package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"omnibridge/internal/orchestration"
	"omnibridge/internal/storage"
)

// Save makes orchestrationExecutor also satisfy orchestration.Recorder. The
// Engine detects this optional interface and persists every terminal execution
// (success, review-limit, failure or cancellation) without coupling the domain
// package to SQLite.
func (x orchestrationExecutor) Save(ex orchestration.Execution) error {
	if x.server == nil || x.server.Storage == nil {
		return nil
	}
	if err := x.server.Storage.EnsureOrchestrationSchema(); err != nil {
		return err
	}
	if wf, ok := orchestration.NewRegistry().Get(ex.WorkflowID); ok {
		if err := x.server.Storage.SaveOrchestrationWorkflow(wf.ID, wf.Version, wf.Name, wf.Enabled, wf); err != nil {
			return err
		}
	}
	review, err := json.Marshal(ex.Review)
	if err != nil {
		return err
	}
	record := storage.OrchestrationExecutionRecord{
		ID:              ex.ID,
		WorkflowID:      ex.WorkflowID,
		WorkflowVersion: ex.WorkflowVersion,
		Status:          string(ex.Status),
		Iteration:       ex.Iteration,
		StartedAt:       ex.StartedAt,
		FinishedAt:      ex.FinishedAt,
		FinalResult:     ex.FinalResult,
		Error:           ex.Error,
		ReviewJSON:      string(review),
	}
	steps := make([]storage.OrchestrationStepRecord, 0, len(ex.Steps))
	for i, step := range ex.Steps {
		steps = append(steps, storage.OrchestrationStepRecord{
			ExecutionID:  ex.ID,
			Ordinal:      i,
			StepID:       step.StepID,
			Role:         string(step.Role),
			Status:       string(step.Status),
			Model:        step.Model,
			Provider:     step.Provider,
			StartedAt:    step.StartedAt,
			FinishedAt:   step.FinishedAt,
			InputTokens:  step.InputTokens,
			OutputTokens: step.OutputTokens,
			TotalTokens:  step.TotalTokens,
			Output:       step.Output,
			Error:        step.Error,
		})
	}
	return x.server.Storage.SaveOrchestrationExecution(record, steps)
}

func (s *Server) ensureOrchestrationStorage() error {
	if s.Storage == nil {
		return fmt.Errorf("orchestration persistence is not attached")
	}
	if err := s.Storage.EnsureOrchestrationSchema(); err != nil {
		return err
	}
	for _, wf := range orchestration.NewRegistry().List() {
		if err := s.Storage.SaveOrchestrationWorkflow(wf.ID, wf.Version, wf.Name, wf.Enabled, wf); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) orchestrationExecutions(w http.ResponseWriter, r *http.Request) {
	if err := s.ensureOrchestrationStorage(); err != nil {
		jsonErr(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	executions, err := s.Storage.ListOrchestrationExecutions(limit)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOut(w, http.StatusOK, map[string]any{"executions": executions})
}

func (s *Server) orchestrationExecution(w http.ResponseWriter, r *http.Request) {
	if err := s.ensureOrchestrationStorage(); err != nil {
		jsonErr(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		jsonErr(w, http.StatusBadRequest, "execution id is required")
		return
	}
	detail, ok, err := s.Storage.GetOrchestrationExecution(id)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		jsonOut(w, http.StatusNotFound, map[string]any{"error": map[string]any{"message": "workflow execution not found", "type": "workflow_execution_not_found", "code": "workflow_execution_not_found"}})
		return
	}
	jsonOut(w, http.StatusOK, detail)
}
