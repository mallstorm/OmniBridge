package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"omnibridge/internal/config"
	"strings"
)

type anthropicModelList struct {
	Data []struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
		Type        string `json:"type"`
	} `json:"data"`
}

func (m *Manager) listAnthropicModels(ctx context.Context, p config.Provider) ([]Model, error) {
	key, err := m.credential(p)
	if err != nil {
		return nil, err
	}
	u := p.ModelsURL
	if u == "" {
		u = "https://api.anthropic.com/v1/models"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-api-key", key)
	ver := p.Headers["anthropic-version"]
	if ver == "" {
		ver = "2023-06-01"
	}
	req.Header.Set("anthropic-version", ver)
	resp, err := m.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, decodeError(resp)
	}
	var data anthropicModelList
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	out := make([]Model, 0, len(data.Data))
	for _, x := range data.Data {
		if x.ID == "" {
			continue
		}
		name := x.DisplayName
		if name == "" {
			name = x.ID
		}
		out = append(out, Model{ID: p.ID + "/" + x.ID, NativeID: x.ID, ProviderID: p.ID, ProviderName: p.Name, Name: name,
			APIType: "messages", PreferredProtocol: "messages", Protocols: append([]string(nil), p.Protocols...), Type: x.Type, Streaming: true, ToolCalling: true, Status: "discovered"})
	}
	return out, nil
}

func (m *Manager) proxyAnthropic(ctx context.Context, p config.Provider, body []byte) (*UpstreamResponse, error) {
	key, err := m.credential(p)
	if err != nil {
		return nil, err
	}
	var meta struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil, err
	}
	_, native := nativeFromQualified(meta.Model)
	body, err = rewriteModel(body, native)
	if err != nil {
		return nil, err
	}
	u := endpoint(p.BaseURL, "messages")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-api-key", key)
	ver := p.Headers["anthropic-version"]
	if ver == "" {
		ver = "2023-06-01"
	}
	req.Header.Set("anthropic-version", ver)
	for k, v := range p.Headers {
		if !strings.EqualFold(k, "anthropic-version") {
			req.Header.Set(k, v)
		}
	}
	resp, err := clientForBody(m.Client, body).Do(req)
	if err != nil {
		return nil, err
	}
	return &UpstreamResponse{StatusCode: resp.StatusCode, Header: resp.Header.Clone(), Body: resp.Body}, nil
}
