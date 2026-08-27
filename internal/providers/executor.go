package providers

import (
	"context"
	"fmt"
	"omnibridge/internal/config"
	"omnibridge/internal/providerdefs"
)

type Executor interface {
	Name() string
	ListModels(context.Context, config.Provider) ([]Model, error)
	Proxy(context.Context, config.Provider, string, []byte) (*UpstreamResponse, error)
}

type executorRegistry struct {
	manager *Manager
	items   map[string]Executor
}

func newExecutorRegistry(m *Manager) *executorRegistry {
	r := &executorRegistry{manager: m, items: map[string]Executor{}}
	r.items["openai-compatible"] = &openAICompatibleExecutor{m: m}
	r.items["openai"] = &openAICompatibleExecutor{m: m}
	r.items["anthropic"] = &anthropicExecutor{m: m}
	r.items["gemini"] = &geminiExecutor{m: m}
	r.items["gigachat"] = &gigaExecutor{m: m}
	r.items["yandex"] = &yandexExecutor{m: m}
	r.items["unsupported-native"] = &unsupportedExecutor{}
	return r
}

func (r *executorRegistry) resolve(p config.Provider) Executor {
	name := "openai-compatible"
	if d, ok := providerdefs.ByID(p.Kind); ok && d.Executor != "" {
		name = d.Executor
	}
	if x := r.items[name]; x != nil {
		return x
	}
	return r.items["openai-compatible"]
}

type openAICompatibleExecutor struct{ m *Manager }

func (e *openAICompatibleExecutor) Name() string { return "openai-compatible" }
func (e *openAICompatibleExecutor) ListModels(ctx context.Context, p config.Provider) ([]Model, error) {
	return e.m.listOpenAIModels(ctx, p)
}
func (e *openAICompatibleExecutor) Proxy(ctx context.Context, p config.Provider, apiType string, body []byte) (*UpstreamResponse, error) {
	clean, err := sanitizeProviderRequest(p, apiType, body)
	if err != nil {
		return nil, err
	}
	p2 := p
	p2.BaseURL = config.ProtocolBaseURL(p, apiType)
	p2.AuthType = config.ProtocolAuthType(p, apiType)
	if apiType == "messages" && p2.Headers["anthropic-version"] == "" {
		if p2.Headers == nil {
			p2.Headers = map[string]string{}
		}
		p2.Headers["anthropic-version"] = "2023-06-01"
	}
	return e.m.proxyOpenAICompatible(ctx, p2, apiType, clean)
}

type anthropicExecutor struct{ m *Manager }

func (e *anthropicExecutor) Name() string { return "anthropic" }
func (e *anthropicExecutor) ListModels(ctx context.Context, p config.Provider) ([]Model, error) {
	return e.m.listAnthropicModels(ctx, p)
}
func (e *anthropicExecutor) Proxy(ctx context.Context, p config.Provider, apiType string, body []byte) (*UpstreamResponse, error) {
	if apiType != "messages" {
		return nil, ErrUnsupportedAPI
	}
	clean, err := sanitizeProviderRequest(p, apiType, body)
	if err != nil {
		return nil, err
	}
	return e.m.proxyAnthropic(ctx, p, clean)
}

type geminiExecutor struct{ m *Manager }

func (e *geminiExecutor) Name() string { return "gemini" }
func (e *geminiExecutor) ListModels(ctx context.Context, p config.Provider) ([]Model, error) {
	return e.m.listGeminiModels(ctx, p)
}
func (e *geminiExecutor) Proxy(ctx context.Context, p config.Provider, apiType string, body []byte) (*UpstreamResponse, error) {
	clean, err := sanitizeProviderRequest(p, apiType, body)
	if err != nil {
		return nil, err
	}
	p2 := p
	p2.BaseURL = config.ProtocolBaseURL(p, apiType)
	p2.AuthType = config.ProtocolAuthType(p, apiType)
	return e.m.proxyOpenAICompatible(ctx, p2, apiType, clean)
}

type gigaExecutor struct{ m *Manager }

func (e *gigaExecutor) Name() string { return "gigachat" }
func (e *gigaExecutor) ListModels(ctx context.Context, p config.Provider) ([]Model, error) {
	return e.m.listGigaModels(ctx, p)
}
func (e *gigaExecutor) Proxy(ctx context.Context, p config.Provider, apiType string, body []byte) (*UpstreamResponse, error) {
	if apiType != "chat-completions" {
		return nil, ErrUnsupportedAPI
	}
	clean, err := sanitizeProviderRequest(p, apiType, body)
	if err != nil {
		return nil, err
	}
	return e.m.proxyGigaChatAgentSafe(ctx, p, clean)
}

type unsupportedExecutor struct{}

func (*unsupportedExecutor) Name() string { return "unsupported-native" }
func (*unsupportedExecutor) ListModels(context.Context, config.Provider) ([]Model, error) {
	return nil, fmt.Errorf("%w: native provider driver is reserved but not enabled", ErrUnsupportedAPI)
}
func (*unsupportedExecutor) Proxy(context.Context, config.Provider, string, []byte) (*UpstreamResponse, error) {
	return nil, ErrUnsupportedAPI
}
