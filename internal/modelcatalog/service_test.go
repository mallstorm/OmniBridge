package modelcatalog

import "testing"

func TestParseModelsDevReferenceMetadata(t *testing.T) {
	raw := []byte(`{"openai":{"models":{"gpt-x":{"name":"GPT X","context_window":128000,"tool_call":true,"cost":{"input":0.000001,"output":0.000002}}}}}`)
	got := parse(raw)
	if len(got) != 1 || got[0].Confidence != "declared" || got[0].ContextWindow != 128000 || got[0].InputUSDPerMillion != 1 {
		t.Fatalf("entries=%+v", got)
	}
}

func TestCatalogURLRejectsLocalAndUnsafeTargets(t *testing.T) {
	if _, err := validateCatalogURL("https://models.dev/api.json"); err != nil {
		t.Fatalf("public HTTPS catalog rejected: %v", err)
	}
	for _, raw := range []string{
		"http://models.dev/api.json",
		"https://localhost/api.json",
		"https://127.0.0.1/api.json",
		"https://10.0.0.1/api.json",
		"https://169.254.169.254/latest/meta-data",
		"https://metadata/api.json",
		"https://user:pass@models.dev/api.json",
	} {
		if _, err := validateCatalogURL(raw); err == nil {
			t.Fatalf("unsafe catalog URL accepted: %s", raw)
		}
	}
}
