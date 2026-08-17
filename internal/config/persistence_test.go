package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omnibridge/internal/storage"
)

func TestPersistentStorePrefersSQLiteAndKeepsBridgeSecretsOut(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "omnibridge.db"))
	if err != nil {
		if strings.Contains(err.Error(), "requires cgo") {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config.json")
	cfg, err := NewPersistent(cfgPath, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.UpsertProvider(Provider{ID: "p1", Name: "SQLite Provider", Kind: "openai-compatible", BaseURL: "https://example.invalid/v1", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	payload, ok, err := db.GetDocument("config.json")
	if err != nil || !ok {
		t.Fatalf("config document missing: ok=%v err=%v", ok, err)
	}
	text := string(payload)
	if strings.Contains(text, "data_api_key") || strings.Contains(text, "admin_api_key") {
		t.Fatalf("bridge secrets leaked into sqlite: %s", text)
	}
	// Make the compatibility JSON stale; SQLite is canonical once attached.
	if err := os.WriteFile(cfgPath, []byte(`{"schema_version":3,"version":"stale","listen":"127.0.0.1:4142","providers":[],"accounts":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg2, err := NewPersistent(cfgPath, db)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg2.Get()
	if len(got.Providers) != 1 || got.Providers[0].ID != "p1" {
		t.Fatalf("sqlite state did not win over stale JSON: %+v", got.Providers)
	}
	_ = db.Close()
}
