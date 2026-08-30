package modelcatalog

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"omnibridge/internal/config"
	"omnibridge/internal/storage"
)

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

func TestLastKnownGoodRestoredAfterRestart(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "omnibridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	want := Snapshot{
		Version:       Version,
		SourceURL:     "https://models.dev/api.json",
		UpdatedAt:     time.Unix(1234, 0).UTC(),
		Hash:          "abc123",
		ReferenceOnly: true,
		Entries:       []Entry{{ProviderID: "openai", ModelID: "gpt-x", Name: "GPT X", Source: "models.dev", Confidence: "declared"}},
	}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutDocument(snapshotDocument, raw); err != nil {
		t.Fatal(err)
	}

	svc := New(db, config.CatalogSyncConfig{Enabled: true, URL: "https://catalog.example/new.json", RefreshHours: 24})
	got := svc.Snapshot()
	if len(got.Entries) != 1 || got.Entries[0].ModelID != "gpt-x" || got.Hash != want.Hash {
		t.Fatalf("last-known-good was not restored: %+v", got)
	}
	if got.SourceURL != want.SourceURL {
		t.Fatalf("restored source URL changed: got %q want %q", got.SourceURL, want.SourceURL)
	}
}
