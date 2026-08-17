package providers

import (
	"encoding/json"
	"omnibridge/internal/config"
	"omnibridge/internal/protocol"
	"omnibridge/internal/providerdefs"
	"strings"
)

// sanitizeProviderRequest is the final target-aware safety pass before an
// executor sends JSON upstream. Protocol translation remains in internal/protocol;
// this layer only removes OmniBridge-private fields and applies declarative
// provider request policies.
func sanitizeProviderRequest(p config.Provider, apiType string, body []byte) ([]byte, error) {
	if len(body) == 0 {
		return body, nil
	}
	// Provider probes and non-server callers still pass through Protocol Engine
	// 3.0. The same-protocol normalizer is lossless for unknown fields.
	if protocol.Supports(apiType) {
		normalized, _, err := protocol.NormalizeSameProtocolRequest(apiType, body)
		if err != nil {
			return nil, err
		}
		body = normalized
	}
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, err
	}
	for k := range root {
		if strings.HasPrefix(strings.ToLower(k), "_omnibridge_") {
			delete(root, k)
		}
	}
	if d, ok := providerdefs.ByID(p.Kind); ok {
		policy := d.RequestPolicies[apiType]
		if len(policy.Remove) == 0 && len(policy.Rename) == 0 {
			policy = d.RequestPolicies["*"]
		}
		for _, k := range policy.Remove {
			delete(root, k)
		}
		for from, to := range policy.Rename {
			if to == "" {
				continue
			}
			if v, ok := root[from]; ok {
				if _, exists := root[to]; !exists {
					root[to] = v
				}
				delete(root, from)
			}
		}
	}
	return json.Marshal(root)
}
