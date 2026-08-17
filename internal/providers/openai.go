package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"omnibridge/internal/config"
	"strings"
	"time"
)

type openAICapabilities struct {
	Vision            bool `json:"vision"`
	Reasoning         bool `json:"reasoning"`
	ToolCalling       bool `json:"tool_calling"`
	ParallelToolCalls bool `json:"parallel_tool_calls"`
	StructuredOutput  bool `json:"structured_output"`
	PDFUpload         bool `json:"pdf_upload"`
}

type openAIModelItem struct {
	ID                  string             `json:"id"`
	Object              string             `json:"object"`
	OwnedBy             string             `json:"owned_by"`
	Type                string             `json:"type"`
	Name                string             `json:"name"`
	DisplayName         string             `json:"displayName"`
	ContextLength       int                `json:"context_length"`
	InputTokenLimit     int                `json:"inputTokenLimit"`
	OutputTokenLimit    int                `json:"outputTokenLimit"`
	MaxOutputTokens     int                `json:"max_output_tokens"`
	Capabilities        openAICapabilities `json:"capabilities"`
	SupportedParameters []string           `json:"supported_parameters"`
	Architecture        map[string]any     `json:"architecture"`
	TopProvider         map[string]any     `json:"top_provider"`
}

func (x *openAIModelItem) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var id string
		if err := json.Unmarshal(b, &id); err != nil {
			return err
		}
		x.ID = strings.TrimPrefix(id, "models/")
		return nil
	}
	type alias openAIModelItem
	var v alias
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*x = openAIModelItem(v)
	return nil
}

type openAIModelList struct {
	Data   []openAIModelItem `json:"data"`
	Models []openAIModelItem `json:"models"`
}

func (m *Manager) listOpenAIModels(ctx context.Context, p config.Provider) ([]Model, error) {
	p = config.NormalizeProvider(p)
	key, err := m.credential(p)
	if err != nil {
		return nil, err
	}
	u := p.ModelsURL
	if u == "" {
		u = modelsEndpoint(p.BaseURL)
	}
	resp, err := doJSON(ctx, m.Client, http.MethodGet, u, p, key, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, decodeError(resp)
	}
	var data openAIModelList
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	items := data.Data
	if len(items) == 0 {
		items = data.Models
	}
	out := make([]Model, 0, len(items))
	for _, x := range items {
		x.ID = strings.TrimPrefix(x.ID, "models/")
		if x.ID == "" {
			x.ID = strings.TrimPrefix(x.Name, "models/")
		}
		if x.ID == "" || clearlyNonGenerationModel(x.ID) {
			continue
		}
		name := x.DisplayName
		if name == "" {
			name = x.Name
		}
		if name == "" {
			name = x.ID
		}
		tool := x.Capabilities.ToolCalling || hasParameter(x.SupportedParameters, "tools") || hasParameter(x.SupportedParameters, "tool_choice")
		vision := x.Capabilities.Vision
		if !vision && x.Architecture != nil {
			if mods, ok := x.Architecture["input_modalities"].([]any); ok {
				for _, v := range mods {
					if s, ok := v.(string); ok && (s == "image" || s == "video") {
						vision = true
					}
				}
			}
		}
		// Direct OpenAI/Anthropic-style gateways generally support streaming even when /models omits flags.
		stream := true
		out = append(out, Model{
			ID: p.ID + "/" + x.ID, NativeID: x.ID, ProviderID: p.ID, ProviderName: p.Name, Name: name,
			APIType: p.PreferredProtocol, PreferredProtocol: p.PreferredProtocol, Protocols: append([]string(nil), p.Protocols...),
			OwnedBy: x.OwnedBy, Type: x.Type, Streaming: stream, ToolCalling: tool || p.Kind == "openai",
			ParallelTools: x.Capabilities.ParallelToolCalls, StructuredOutput: x.Capabilities.StructuredOutput,
			Reasoning: x.Capabilities.Reasoning, Vision: vision, PDF: x.Capabilities.PDFUpload,
			ContextWindow: func() int {
				if x.ContextLength > 0 {
					return x.ContextLength
				}
				if x.InputTokenLimit > 0 || x.OutputTokenLimit > 0 {
					return x.InputTokenLimit + x.OutputTokenLimit
				}
				return 0
			}(), MaxOutputTokens: func() int {
				if x.MaxOutputTokens > 0 {
					return x.MaxOutputTokens
				}
				return x.OutputTokenLimit
			}(), Status: "discovered",
		})
	}
	return out, nil
}

func hasParameter(in []string, names ...string) bool {
	for _, x := range in {
		for _, n := range names {
			if strings.EqualFold(x, n) {
				return true
			}
		}
	}
	return false
}

func (m *Manager) proxyOpenAICompatible(ctx context.Context, p config.Provider, apiType string, body []byte) (*UpstreamResponse, error) {
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
	u := endpoint(p.BaseURL, apiType)
	resp, err := doJSON(ctx, clientForBody(m.Client, body), http.MethodPost, u, p, key, body)
	if err != nil {
		return nil, err
	}
	return &UpstreamResponse{StatusCode: resp.StatusCode, Header: resp.Header.Clone(), Body: resp.Body}, nil
}

func readSmall(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 4096))
	return strings.TrimSpace(string(b))
}
func deadlineContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 30*time.Second)
}
