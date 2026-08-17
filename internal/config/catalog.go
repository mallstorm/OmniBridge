package config

import (
	"omnibridge/internal/providerdefs"
	"strings"
)

// ProviderPreset is the public/config-facing view of a data-driven provider definition.
// It remains a type alias to preserve the existing admin API and WPF catalog contract.
type ProviderPreset = providerdefs.Definition

func ProviderPresetByID(id string) (ProviderPreset, bool) {
	return providerdefs.ByID(id)
}

func ProviderCatalog() []ProviderPreset {
	out, err := providerdefs.All()
	if err != nil {
		return []ProviderPreset{}
	}
	return out
}

func cloneMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func DetectProviderDefinition(baseURL, key string) string {
	return providerdefs.Detect(strings.TrimSpace(baseURL), strings.TrimSpace(key))
}
