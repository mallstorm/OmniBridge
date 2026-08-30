package modelcatalog

import "testing"

func TestParseModelsDevReferenceMetadata(t *testing.T) {
	raw := []byte(`{"openai":{"models":{"gpt-x":{"name":"GPT X","context_window":128000,"tool_call":true,"cost":{"input":0.000001,"output":0.000002}}}}}`)
	got := parse(raw)
	if len(got) != 1 || got[0].Confidence != "declared" || got[0].ContextWindow != 128000 || got[0].InputUSDPerMillion != 1 {
		t.Fatalf("entries=%+v", got)
	}
}
