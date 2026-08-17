package integrations

import "testing"

func TestCatalogAndPlan(t *testing.T) {
	h := New(t.TempDir(), nil)
	states := h.Detect("127.0.0.1:4142")
	if len(states) < 8 {
		t.Fatalf("catalog too small: %d", len(states))
	}
	p, ok := h.Plan("cline", "127.0.0.1:4142")
	if !ok || p.Endpoint != "http://127.0.0.1:4142/v1" || p.Snippet["model"] != "auto/smart" {
		t.Fatalf("bad plan %+v", p)
	}
}
