package providerdefs

import "testing"

func TestCatalogIsDataDrivenAndLarge(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 80 {
		t.Fatalf("catalog too small: %d", len(all))
	}
	seen := map[string]bool{}
	for _, d := range all {
		if d.ID == "" || d.Name == "" || d.Executor == "" || d.SupportLevel == "" {
			t.Fatalf("incomplete definition: %+v", d)
		}
		if seen[d.ID] {
			t.Fatalf("duplicate id %q", d.ID)
		}
		seen[d.ID] = true
		if len(d.Protocols) == 0 {
			t.Fatalf("%s has no protocols", d.ID)
		}
	}
	for _, id := range []string{"openai", "anthropic", "gemini", "gigachat", "openrouter", "ollama", "custom", "deepinfra", "litellm", "llamacpp"} {
		if _, ok := ByID(id); !ok {
			t.Fatalf("missing %s", id)
		}
	}
}

func TestDetectionLongestSpecificKeyPrefixWins(t *testing.T) {
	if got := Detect("", "sk-or-test"); got != "openrouter" {
		t.Fatalf("got %s", got)
	}
	if got := Detect("", "sk-ant-test"); got != "anthropic" {
		t.Fatalf("got %s", got)
	}
	if got := Detect("", "ordinary-sk-looking-key"); got != "custom" {
		t.Fatalf("got %s", got)
	}
}

func TestTemplatesDoNotInventVendorEndpoint(t *testing.T) {
	for _, id := range []string{"deepinfra", "databricks", "portkey", "llamacpp"} {
		d, ok := ByID(id)
		if !ok {
			t.Fatal(id)
		}
		if !d.RequiresBaseURL {
			t.Fatalf("%s must require explicit Base URL", id)
		}
		if d.BaseURL != "" {
			t.Fatalf("%s template must not hardcode endpoint: %q", id, d.BaseURL)
		}
	}
}
