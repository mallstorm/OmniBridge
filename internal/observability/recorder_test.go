package observability

import (
	"net/http/httptest"
	"testing"
)

func TestRecorderDoesNotCapturePrompt(t *testing.T) {
	r := New(nil)
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	ctx, tr := r.Begin(req)
	_ = ctx
	tr.Model = "auto/smart"
	tr.SelectedModel = "p/m"
	r.Finish(tr, 200)
	got := r.Recent(1)
	if len(got) != 1 || got[0].Model != "auto/smart" {
		t.Fatal(got)
	}
	if _, ok := got[0].Attributes["prompt"]; ok {
		t.Fatal("prompt must not be stored")
	}
}
