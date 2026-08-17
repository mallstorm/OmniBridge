package providers

import (
	"context"
	"errors"
	"fmt"
	"omnibridge/internal/config"
	"strings"
)

const yandexAIStudioBaseURL = "https://ai.api.cloud.yandex.net/v1"

type yandexExecutor struct{ m *Manager }

func (e *yandexExecutor) Name() string { return "yandex" }

func prepareYandexProvider(p config.Provider) (config.Provider, error) {
	p = config.NormalizeProvider(p)
	projectID := strings.TrimSpace(p.ProjectID)
	if projectID == "" {
		projectID = strings.TrimSpace(p.Headers["OpenAI-Project"])
	}
	if projectID == "" {
		return config.Provider{}, errors.New("Yandex Folder ID is required (OpenAI-Project)")
	}
	if strings.ContainsAny(projectID, "\r\n\t ") || len(projectID) > 128 {
		return config.Provider{}, errors.New("Yandex Folder ID has invalid format")
	}
	if p.BaseURL == "" {
		p.BaseURL = yandexAIStudioBaseURL
	}
	if p.ModelsURL == "" {
		p.ModelsURL = strings.TrimRight(p.BaseURL, "/") + "/models"
	}
	if p.Headers == nil {
		p.Headers = map[string]string{}
	}
	p.ProjectID = projectID
	p.Headers["OpenAI-Project"] = projectID
	p.AuthType = "yandex-api-key"
	return p, nil
}

func (e *yandexExecutor) ListModels(ctx context.Context, p config.Provider) ([]Model, error) {
	prepared, err := prepareYandexProvider(p)
	if err != nil {
		return nil, err
	}
	models, err := e.m.listOpenAIModels(ctx, prepared)
	if err != nil {
		return nil, fmt.Errorf("Yandex AI Studio model discovery failed: %w", err)
	}
	return models, nil
}

func (e *yandexExecutor) Proxy(ctx context.Context, p config.Provider, apiType string, body []byte) (*UpstreamResponse, error) {
	if apiType != "responses" && apiType != "chat-completions" {
		return nil, ErrUnsupportedAPI
	}
	prepared, err := prepareYandexProvider(p)
	if err != nil {
		return nil, err
	}
	clean, err := sanitizeProviderRequest(prepared, apiType, body)
	if err != nil {
		return nil, err
	}
	prepared.BaseURL = config.ProtocolBaseURL(prepared, apiType)
	prepared.AuthType = config.ProtocolAuthType(prepared, apiType)
	return e.m.proxyOpenAICompatible(ctx, prepared, apiType, clean)
}
