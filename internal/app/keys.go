package app

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"omnibridge/internal/config"
	"omnibridge/internal/secretstore"
)

// BridgeKeys are the two local secrets of an OmniBridge Core installation.
type BridgeKeys struct {
	DataAPIKey  string `json:"data_api_key"`
	AdminAPIKey string `json:"admin_api_key"`
}

// ReadBridgeKeys loads the bridge keys from the secret store under root/data.
// It runs in the same user context as the Core and never starts the server.
func ReadBridgeKeys(root string) (BridgeKeys, error) {
	secrets := secretstore.New(filepath.Join(root, "data", "secrets.json"))
	data, err := secrets.Get(config.BridgeDataKeyRef)
	if err != nil {
		return BridgeKeys{}, fmt.Errorf("read data key: %w", err)
	}
	admin, err := secrets.Get(config.BridgeAdminKeyRef)
	if err != nil {
		return BridgeKeys{}, fmt.Errorf("read admin key: %w", err)
	}
	return BridgeKeys{DataAPIKey: data, AdminAPIKey: admin}, nil
}

// KeysJSON renders ReadBridgeKeys output for the Hub shell.
func KeysJSON(root string) ([]byte, error) {
	k, err := ReadBridgeKeys(root)
	if err != nil {
		return nil, err
	}
	return json.Marshal(k)
}
