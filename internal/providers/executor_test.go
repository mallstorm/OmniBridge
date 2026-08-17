package providers

import (
	"encoding/json"
	"omnibridge/internal/config"
	"testing"
)

func TestResolveTargetUsesDefinitionExecutor(t *testing.T) {
	p := config.NormalizeProvider(config.Provider{Kind: "anthropic", ID: "a", Enabled: true})
	plan, err := ResolveTarget(p, "messages")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Executor != "anthropic" || plan.AuthType != "x-api-key" {
		t.Fatalf("bad plan %+v", plan)
	}
}

func TestResolveTargetRejectsReservedNativeDriver(t *testing.T) {
	p := config.NormalizeProvider(config.Provider{Kind: "cohere", ID: "c", Enabled: true})
	if _, err := ResolveTarget(p, "cohere-chat"); err == nil {
		t.Fatal("expected reserved driver error")
	}
}

func TestTargetSanitizerDropsPrivateAndDeclarativeFields(t *testing.T) {
	p := config.NormalizeProvider(config.Provider{Kind: "gigachat", ID: "g", Enabled: true})
	in := []byte(`{"model":"g/x","parallel_tool_calls":true,"_omnibridge_trace":"secret","messages":[]}`)
	out, err := sanitizeProviderRequest(p, "chat-completions", in)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatal(err)
	}
	if _, ok := v["parallel_tool_calls"]; ok {
		t.Fatal("declarative remove policy not applied")
	}
	if _, ok := v["_omnibridge_trace"]; ok {
		t.Fatal("private field leaked upstream")
	}
}
