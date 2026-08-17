package providers

import (
	"fmt"
	"omnibridge/internal/config"
	"omnibridge/internal/providerdefs"
	"strings"
)

type TargetPlan struct {
	ProviderKind string            `json:"provider_kind"`
	Executor     string            `json:"executor"`
	SupportLevel string            `json:"support_level"`
	APIType      string            `json:"api_type"`
	BaseURL      string            `json:"base_url"`
	AuthType     string            `json:"auth_type"`
	Headers      map[string]string `json:"headers,omitempty"`
}

func ResolveTarget(p config.Provider, apiType string) (TargetPlan, error) {
	p = config.NormalizeProvider(p)
	apiType = strings.ToLower(strings.TrimSpace(apiType))
	if apiType == "" {
		apiType = p.PreferredProtocol
	}
	if apiType != "embeddings" && !config.SupportsProtocol(p, apiType) {
		return TargetPlan{}, ErrUnsupportedAPI
	}
	d, ok := providerdefs.ByID(p.Kind)
	executor := "openai-compatible"
	support := "custom"
	if ok {
		executor = d.Executor
		support = d.SupportLevel
	}
	if executor == "unsupported-native" {
		return TargetPlan{}, fmt.Errorf("%w: %s driver is reserved", ErrUnsupportedAPI, p.Kind)
	}
	base := config.ProtocolBaseURL(p, apiType)
	if strings.TrimSpace(base) == "" {
		return TargetPlan{}, fmt.Errorf("provider %s has no Base URL for %s", p.Kind, apiType)
	}
	return TargetPlan{ProviderKind: p.Kind, Executor: executor, SupportLevel: support, APIType: apiType, BaseURL: base, AuthType: config.ProtocolAuthType(p, apiType), Headers: cloneHeaders(p.Headers)}, nil
}

func cloneHeaders(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
