package app

import (
	"os"
	"path/filepath"
	"testing"

	"omnibridge/internal/config"
	"omnibridge/internal/secretstore"
)

func TestReadBridgeKeysRoundTrip(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	s := secretstore.New(filepath.Join(root, "data", "secrets.json"))
	if err := s.Put(config.BridgeDataKeyRef, "data-key-123"); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(config.BridgeAdminKeyRef, "admin-key-456"); err != nil {
		t.Fatal(err)
	}
	keys, err := ReadBridgeKeys(root)
	if err != nil {
		t.Fatalf("ReadBridgeKeys: %v", err)
	}
	if keys.DataAPIKey != "data-key-123" || keys.AdminAPIKey != "admin-key-456" {
		t.Fatalf("unexpected keys: %+v", keys)
	}
}
