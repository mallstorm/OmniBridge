package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestOrchestrationExecutionRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "omnibridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.EnsureOrchestrationSchema(); err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC().Round(0)
	finished := started.Add(2 * time.Second)
	ex := OrchestrationExecutionRecord{
		ID:              "wf-test-1",
		WorkflowID:      "workflow/coding-review",
		WorkflowVersion: 1,
		Status:          "COMPLETED",
		Iteration:       1,
		StartedAt:       started,
		FinishedAt:      finished,
		FinalResult:     "fixed result",
		ReviewJSON:      `{"Status":"PASS"}`,
	}
	steps := []OrchestrationStepRecord{{
		ExecutionID: "wf-test-1", Ordinal: 0, StepID: "developer", Role: "developer",
		Status: "COMPLETED", Model: "p/model", Provider: "p", StartedAt: started,
		FinishedAt: started.Add(time.Second), InputTokens: 10, OutputTokens: 20,
		TotalTokens: 30, Output: "draft",
	}}
	if err := db.SaveOrchestrationExecution(ex, steps); err != nil {
		t.Fatal(err)
	}
	detail, ok, err := db.GetOrchestrationExecution("wf-test-1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("execution not found")
	}
	if detail.Execution.FinalResult != "fixed result" || detail.Execution.Status != "COMPLETED" {
		t.Fatalf("unexpected execution: %+v", detail.Execution)
	}
	if len(detail.Steps) != 1 || detail.Steps[0].Model != "p/model" || detail.Steps[0].TotalTokens != 30 {
		t.Fatalf("unexpected steps: %+v", detail.Steps)
	}
	list, err := db.ListOrchestrationExecutions(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "wf-test-1" {
		t.Fatalf("unexpected list: %+v", list)
	}
}

func TestOrchestrationSchemaIsIdempotent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "omnibridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.EnsureOrchestrationSchema(); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureOrchestrationSchema(); err != nil {
		t.Fatal(err)
	}
}
