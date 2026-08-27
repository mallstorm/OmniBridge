package server

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestPeekWorkflowRequest(t *testing.T) {
	r, err := http.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"workflow/coding-review","messages":[{"role":"user","content":"task"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	workflow, body, err := peekWorkflowRequest(r, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !workflow {
		t.Fatal("expected workflow request")
	}
	if !strings.Contains(string(body), "workflow/coding-review") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestPeekOrdinaryRequest(t *testing.T) {
	r, err := http.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"auto/smart","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	workflow, body, err := peekWorkflowRequest(r, 0)
	if err != nil {
		t.Fatal(err)
	}
	if workflow {
		t.Fatal("ordinary model must not enter orchestration")
	}
	if len(body) == 0 {
		t.Fatal("body must be preserved for delegation")
	}
}

func TestWorkflowRoutingPathClassification(t *testing.T) {
	if !isGenerationPath(http.MethodPost, "/v1/chat/completions") {
		t.Fatal("chat completions must be generation path")
	}
	if isGenerationPath(http.MethodGet, "/v1/chat/completions") {
		t.Fatal("GET must not be generation path")
	}
	if !isOrchestrationControlPath(http.MethodGet, "/bridge/orchestration/executions/wf-1") {
		t.Fatal("execution detail must be orchestration control path")
	}
}

func TestPeekMalformedBodyReturnsBytes(t *testing.T) {
	r, err := http.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":`))
	if err != nil {
		t.Fatal(err)
	}
	_, body, err := peekWorkflowRequest(r, 0)
	if err == nil {
		t.Fatal("expected JSON error")
	}
	if string(body) != `{"model":` {
		t.Fatalf("body not preserved: %q", body)
	}
	// Show that the caller can restore the body for the unchanged base handler.
	r.Body = io.NopCloser(strings.NewReader(string(body)))
	restored, _ := io.ReadAll(r.Body)
	if string(restored) != string(body) {
		t.Fatal("body restore failed")
	}
}
