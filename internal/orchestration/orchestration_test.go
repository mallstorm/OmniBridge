package orchestration

import (
	"context"
	"errors"
	"testing"
)

type fakeExecutor struct {
	outputs []CallResult
	calls   []Call
	errAt   int
}

func (f *fakeExecutor) Execute(_ context.Context, c Call) (CallResult, error) {
	f.calls = append(f.calls, c)
	n := len(f.calls)
	if f.errAt == n {
		return CallResult{}, errors.New("boom")
	}
	if n > len(f.outputs) {
		return CallResult{}, errors.New("unexpected call")
	}
	return f.outputs[n-1], nil
}

func TestBuiltinCodingReviewValid(t *testing.T) {
	if err := BuiltinCodingReview().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsDuplicateAndCycle(t *testing.T) {
	w := BuiltinCodingReview()
	w.Steps = append(w.Steps, w.Steps[0])
	if w.Validate() == nil {
		t.Fatal("expected duplicate error")
	}
	w = BuiltinCodingReview()
	w.Steps[0].DependsOn = []string{"reviewer"}
	if w.Validate() == nil {
		t.Fatal("expected cycle error")
	}
}

func TestCodingReviewPass(t *testing.T) {
	f := &fakeExecutor{outputs: []CallResult{
		{Output: "draft", Model: "m1", Provider: "p1"},
		{Output: "critique", Model: "m2", Provider: "p2"},
		{Output: "fixed", Model: "m1", Provider: "p1"},
		{Output: "PASS\nLooks good", Model: "m3", Provider: "p3"},
	}}
	e := Engine{Registry: NewRegistry(), Executor: f, Verdicts: StrictVerdictParser{}}
	ex, err := e.Run(context.Background(), CodingReviewModel, Request{ID: "x", Input: "task"})
	if err != nil {
		t.Fatal(err)
	}
	if ex.Status != ExecutionCompleted || ex.FinalResult != "fixed" || ex.Review.Status != "PASS" {
		t.Fatalf("unexpected execution: %+v", ex)
	}
	if len(f.calls) != 4 {
		t.Fatalf("calls=%d", len(f.calls))
	}
}

func TestReviewerFailThenFixAndPass(t *testing.T) {
	f := &fakeExecutor{outputs: []CallResult{
		{Output: "draft"},
		{Output: "critique"},
		{Output: "fixed1"},
		{Output: "FAIL\nblocking issue"},
		{Output: "fixed2"},
		{Output: "PASS"},
	}}
	e := Engine{Registry: NewRegistry(), Executor: f, Verdicts: StrictVerdictParser{}}
	ex, err := e.Run(context.Background(), CodingReviewModel, Request{ID: "x", Input: "task"})
	if err != nil {
		t.Fatal(err)
	}
	if ex.FinalResult != "fixed2" || ex.Iteration != 2 {
		t.Fatalf("unexpected execution: %+v", ex)
	}
	if len(f.calls) != 6 {
		t.Fatalf("calls=%d", len(f.calls))
	}
}

func TestInvalidReviewerVerdictFailsClosed(t *testing.T) {
	f := &fakeExecutor{outputs: []CallResult{{Output: "draft"}, {Output: "critique"}, {Output: "fixed"}, {Output: "maybe"}}}
	e := Engine{Registry: NewRegistry(), Executor: f, Verdicts: StrictVerdictParser{}}
	ex, err := e.Run(context.Background(), CodingReviewModel, Request{ID: "x", Input: "task"})
	if err == nil || ex.Status != ExecutionFailed {
		t.Fatalf("expected fail-closed, ex=%+v err=%v", ex, err)
	}
}

func TestStrictVerdictParserRejectsPrefixLookalikes(t *testing.T) {
	parser := StrictVerdictParser{}
	for _, input := range []string{"PASSING", "PASSIVE response", "FAILURE maybe"} {
		if verdict, err := parser.Parse(input); err == nil {
			t.Fatalf("input=%q unexpectedly parsed as %+v", input, verdict)
		}
	}
}

func TestStrictVerdictParserAcceptsStructuredVerdict(t *testing.T) {
	parser := StrictVerdictParser{}
	verdict, err := parser.Parse(`{"status":"PASS","score":97,"summary":"quality gate passed"}`)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Status != "PASS" || verdict.Score != 97 {
		t.Fatalf("verdict=%+v", verdict)
	}
}
