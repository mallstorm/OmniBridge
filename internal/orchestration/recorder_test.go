package orchestration

import (
	"context"
	"testing"
)

type recordingExecutor struct {
	outputs []CallResult
	calls   int
	saved   []Execution
}

func (r *recordingExecutor) Execute(_ context.Context, _ Call) (CallResult, error) {
	out := r.outputs[r.calls]
	r.calls++
	return out, nil
}

func (r *recordingExecutor) Save(ex Execution) error {
	r.saved = append(r.saved, ex)
	return nil
}

func TestEngineAutoDetectsRecorderOnExecutor(t *testing.T) {
	exec := &recordingExecutor{outputs: []CallResult{
		{Output: "draft", Model: "m1", Provider: "p1"},
		{Output: "critique", Model: "m2", Provider: "p2"},
		{Output: "fixed", Model: "m1", Provider: "p1"},
		{Output: "PASS", Model: "m3", Provider: "p3"},
	}}
	engine := Engine{Registry: NewRegistry(), Executor: exec, Verdicts: StrictVerdictParser{}}
	result, err := engine.Run(context.Background(), CodingReviewModel, Request{ID: "recorded", Input: "task"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != ExecutionCompleted {
		t.Fatalf("status=%s", result.Status)
	}
	if len(exec.saved) != 1 {
		t.Fatalf("saved executions=%d", len(exec.saved))
	}
	if exec.saved[0].ID != "recorded" || exec.saved[0].Review.Status != "PASS" {
		t.Fatalf("unexpected saved execution: %+v", exec.saved[0])
	}
}
