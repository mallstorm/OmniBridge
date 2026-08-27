package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"omnibridge/internal/orchestration"
	"omnibridge/internal/protocol"
)

func (s *Server) handleWorkflowResponses(w http.ResponseWriter, r *http.Request, body []byte, model string) {
	canonical, err := protocol.ParseRequest(protocol.Responses, body)
	if err != nil {
		jsonOut(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"message": err.Error(), "type": "workflow_request_invalid", "code": "workflow_request_invalid"}})
		return
	}
	if len(canonical.Tools) > 0 {
		jsonOut(w, http.StatusUnprocessableEntity, map[string]any{"error": map[string]any{"message": "workspace/tool execution is intentionally outside orchestration v1.1", "type": "workflow_tools_not_supported", "code": "workflow_tools_not_supported"}})
		return
	}
	input := canonicalWorkflowInput(canonical)
	if strings.TrimSpace(input) == "" {
		jsonOut(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"message": "workflow request contains no textual input", "type": "workflow_request_invalid", "code": "workflow_request_invalid"}})
		return
	}

	executionID := fmt.Sprintf("wf-%d", time.Now().UnixNano())
	ex, err := newOrchestrationEngine(s).Run(r.Context(), model, orchestration.Request{ID: executionID, Input: input})
	annotateWorkflowExecution(r.Context(), ex)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || r.Context().Err() != nil {
			return
		}
		code := "workflow_failed"
		status := http.StatusBadGateway
		switch {
		case strings.Contains(err.Error(), "workflow_not_found"):
			code, status = "workflow_not_found", http.StatusNotFound
		case strings.Contains(err.Error(), "workflow_no_candidate"):
			code, status = "workflow_no_candidate", http.StatusUnprocessableEntity
		case strings.Contains(err.Error(), "workflow_budget_exceeded"):
			code, status = "workflow_budget_exceeded", http.StatusPaymentRequired
		}
		jsonOut(w, status, map[string]any{"error": map[string]any{"message": err.Error(), "type": code, "code": code}, "execution_id": ex.ID})
		return
	}

	var inputTokens, outputTokens, totalTokens int64
	for _, step := range ex.Steps {
		inputTokens += step.InputTokens
		outputTokens += step.OutputTokens
		totalTokens += step.TotalTokens
	}
	if totalTokens == 0 {
		totalTokens = inputTokens + outputTokens
	}
	encoded, err := protocol.EncodeResponse(protocol.Responses, protocol.CanonicalResponse{
		ID:           ex.ID,
		Model:        model,
		Text:         ex.FinalResult,
		FinishReason: "stop",
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		TotalTokens:  totalTokens,
	})
	if err != nil {
		jsonOut(w, http.StatusBadGateway, map[string]any{"error": map[string]any{"message": err.Error(), "type": "workflow_response_encode_failed", "code": "workflow_response_encode_failed"}})
		return
	}

	w.Header().Set("X-OmniBridge-Workflow-Execution", ex.ID)
	if canonical.Stream {
		sse, err := protocol.SyntheticSSE(protocol.Responses, encoded)
		if err != nil {
			jsonOut(w, http.StatusBadGateway, map[string]any{"error": map[string]any{"message": err.Error(), "type": "workflow_stream_encode_failed", "code": "workflow_stream_encode_failed"}})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(sse)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)
}
