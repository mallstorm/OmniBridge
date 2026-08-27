package server

import (
	"encoding/json"
	"testing"

	"omnibridge/internal/orchestration"
	"omnibridge/internal/providers"
)

func TestOrchestrationChatBodyIsNonStreaming(t *testing.T) {
	body, err := orchestrationChatBody("auto/coding", orchestration.Call{
		Step:  orchestration.Step{Role: orchestration.RoleDeveloper},
		Input: "implement feature",
	})
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatal(err)
	}
	if v["model"] != "auto/coding" {
		t.Fatalf("model=%v", v["model"])
	}
	if stream, ok := v["stream"].(bool); !ok || stream {
		t.Fatalf("workflow internal calls must be non-streaming: %v", v["stream"])
	}
}

func TestApplyOrchestrationDiversityPrefersDifferentProvider(t *testing.T) {
	candidates := []providers.Model{
		{ID: "p1/model-a", ProviderID: "p1"},
		{ID: "p2/model-b", ProviderID: "p2"},
	}
	call := orchestration.Call{
		Workflow: orchestration.Workflow{Policy: orchestration.Policy{Diversity: orchestration.DiversityProvider}},
		AvoidProviders: []string{"p1"},
	}
	got := applyOrchestrationDiversity(candidates, call)
	if len(got) != 2 || got[0].ProviderID != "p2" || got[1].ProviderID != "p1" {
		t.Fatalf("unexpected diversity order: %+v", got)
	}
}

func TestApplyOrchestrationDiversityGracefulFallback(t *testing.T) {
	candidates := []providers.Model{{ID: "p1/model-a", ProviderID: "p1"}}
	call := orchestration.Call{
		Workflow: orchestration.Workflow{Policy: orchestration.Policy{Diversity: orchestration.DiversityProvider}},
		AvoidProviders: []string{"p1"},
	}
	got := applyOrchestrationDiversity(candidates, call)
	if len(got) != 1 || got[0].ID != "p1/model-a" {
		t.Fatalf("single-provider workflow must degrade gracefully: %+v", got)
	}
}

func TestExtractChatCompletionTextString(t *testing.T) {
	text, err := extractChatCompletionText([]byte(`{"choices":[{"message":{"content":"final answer"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if text != "final answer" {
		t.Fatalf("text=%q", text)
	}
}

func TestExtractChatCompletionTextParts(t *testing.T) {
	text, err := extractChatCompletionText([]byte(`{"choices":[{"message":{"content":[{"type":"text","text":"hello "},{"type":"text","text":"world"}]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if text != "hello world" {
		t.Fatalf("text=%q", text)
	}
}
