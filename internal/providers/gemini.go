package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"omnibridge/internal/config"
	"strings"
)

type geminiModels struct {
	Models []struct {
		Name                       string   `json:"name"`
		DisplayName                string   `json:"displayName"`
		InputTokenLimit            int      `json:"inputTokenLimit"`
		OutputTokenLimit           int      `json:"outputTokenLimit"`
		SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
	} `json:"models"`
}

func (m *Manager) listGeminiModels(ctx context.Context, p config.Provider) ([]Model, error) {
	key, err := m.credential(p)
	if err != nil {
		return nil, err
	}
	u := p.ModelsURL
	if u == "" {
		u = "https://generativelanguage.googleapis.com/v1beta/models"
	}
	// Native Gemini models listing accepts x-goog-api-key. Keep provider config immutable.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-goog-api-key", key)
	resp, err := m.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, decodeError(resp)
	}
	var data geminiModels
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	out := []Model{}
	for _, x := range data.Models {
		native := strings.TrimPrefix(x.Name, "models/")
		if native == "" {
			continue
		}
		generation := false
		for _, method := range x.SupportedGenerationMethods {
			if method == "generateContent" {
				generation = true
				break
			}
		}
		if !generation {
			continue
		}
		out = append(out, Model{ID: p.ID + "/" + native, NativeID: native, ProviderID: p.ID, ProviderName: p.Name,
			Name: func() string {
				if x.DisplayName != "" {
					return x.DisplayName
				}
				return native
			}(), APIType: p.PreferredProtocol, PreferredProtocol: p.PreferredProtocol, Protocols: append([]string(nil), p.Protocols...),
			Streaming: true, ToolCalling: true, Status: "discovered", ContextWindow: x.InputTokenLimit + x.OutputTokenLimit, MaxOutputTokens: x.OutputTokenLimit})
	}
	return out, nil
}
