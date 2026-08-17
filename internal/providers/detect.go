package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"omnibridge/internal/config"
	"strings"
	"time"
)

type DetectResult struct {
	BaseURL           string   `json:"base_url"`
	ModelsURL         string   `json:"models_url"`
	AuthType          string   `json:"auth_type"`
	APIType           string   `json:"api_type"`
	Protocols         []string `json:"protocols,omitempty"`
	PreferredProtocol string   `json:"preferred_protocol,omitempty"`
	Shape             string   `json:"shape"`
	Status            int      `json:"status"`
	ProbeModel        string   `json:"probe_model,omitempty"`
}

// DetectOpenAICompatible probes only URLs on the user-supplied origin.
// Credentials are never sent to another origin. Once a model ID is discovered,
// tiny generation requests are used to learn which compatibility protocols work.
func (m *Manager) DetectOpenAICompatible(ctx context.Context, baseURL, key string) (DetectResult, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return DetectResult{}, fmt.Errorf("invalid Base URL")
	}
	origin := parsed.Scheme + "://" + parsed.Host
	base := strings.TrimRight(baseURL, "/")
	for _, suffix := range []string{"/chat/completions", "/responses", "/messages", "/models"} {
		base = strings.TrimSuffix(base, suffix)
	}
	candidates := []string{base + "/models"}
	if !strings.HasSuffix(base, "/v1") {
		candidates = append(candidates, base+"/v1/models")
	}
	if base == origin {
		candidates = []string{origin + "/v1/models", origin + "/models"}
	}
	seen := map[string]bool{}
	auths := []string{"bearer", "x-api-key", "api-key", "x-goog-api-key"}
	if strings.TrimSpace(key) == "" {
		auths = []string{"none"}
	}
	var last string
	for _, modelsURL := range candidates {
		u, _ := url.Parse(modelsURL)
		if u.Scheme+"://"+u.Host != origin || seen[modelsURL] {
			continue
		}
		seen[modelsURL] = true
		for _, auth := range auths {
			cctx, cancel := context.WithTimeout(ctx, 7*time.Second)
			req, _ := http.NewRequestWithContext(cctx, http.MethodGet, modelsURL, nil)
			req.Header.Set("Accept", "application/json")
			setAuthHeader(req, auth, key)
			resp, er := m.Client.Do(req)
			if er != nil {
				cancel()
				last = er.Error()
				continue
			}
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
			resp.Body.Close()
			cancel()
			last = fmt.Sprintf("%s -> HTTP %d", modelsURL, resp.StatusCode)
			if resp.StatusCode/100 != 2 {
				continue
			}
			shape, modelID := detectModelShape(b)
			if shape == "" {
				continue
			}
			detectedBase := strings.TrimSuffix(modelsURL, "/models")
			protocols := []string{}
			if modelID != "" {
				protocols = m.detectCompatibleProtocols(ctx, origin, detectedBase, auth, key, modelID)
			}
			if len(protocols) == 0 {
				protocols = []string{"chat-completions"}
			}
			preferred := preferredDetectedProtocol(protocols)
			return DetectResult{BaseURL: detectedBase, ModelsURL: modelsURL, AuthType: auth, APIType: preferred, Protocols: protocols, PreferredProtocol: preferred, Shape: shape, Status: resp.StatusCode, ProbeModel: modelID}, nil
		}
	}
	if last == "" {
		last = "no candidate endpoint"
	}
	return DetectResult{}, fmt.Errorf("endpoint detection failed: %s", last)
}

func setAuthHeader(req *http.Request, auth, key string) {
	if key == "" {
		return
	}
	switch auth {
	case "x-api-key":
		req.Header.Set("x-api-key", key)
	case "api-key":
		req.Header.Set("api-key", key)
	case "x-goog-api-key":
		req.Header.Set("x-goog-api-key", key)
	default:
		req.Header.Set("Authorization", "Bearer "+key)
	}
}

func detectModelShape(b []byte) (string, string) {
	var v map[string]any
	if json.Unmarshal(b, &v) != nil {
		return "", ""
	}
	if data, ok := v["data"].([]any); ok {
		return "openai-model-list", firstModelID(data)
	}
	if data, ok := v["models"].([]any); ok {
		return "models-array", firstModelID(data)
	}
	return "", ""
}
func firstModelID(items []any) string {
	for _, x := range items {
		switch v := x.(type) {
		case string:
			if v != "" {
				return v
			}
		case map[string]any:
			for _, k := range []string{"id", "name", "model"} {
				if s, ok := v[k].(string); ok && s != "" {
					return strings.TrimPrefix(s, "models/")
				}
			}
		}
	}
	return ""
}
func preferredDetectedProtocol(p []string) string {
	for _, want := range []string{"responses", "messages", "chat-completions"} {
		for _, x := range p {
			if x == want {
				return x
			}
		}
	}
	if len(p) > 0 {
		return p[0]
	}
	return "chat-completions"
}

func (m *Manager) detectCompatibleProtocols(ctx context.Context, origin, base, auth, key, model string) []string {
	out := []string{}
	p := config.Provider{BaseURL: base, AuthType: auth, Headers: map[string]string{"anthropic-version": "2023-06-01"}}
	for _, apiType := range []string{"responses", "messages", "chat-completions"} {
		u := endpoint(base, apiType)
		parsed, err := url.Parse(u)
		if err != nil || parsed.Scheme+"://"+parsed.Host != origin {
			continue
		}
		var body map[string]any
		switch apiType {
		case "responses":
			body = map[string]any{"model": model, "input": "Reply OK", "max_output_tokens": 8}
		case "messages":
			body = map[string]any{"model": model, "messages": []any{map[string]any{"role": "user", "content": "Reply OK"}}, "max_tokens": 8}
		default:
			body = map[string]any{"model": model, "messages": []any{map[string]any{"role": "user", "content": "Reply OK"}}, "max_tokens": 8}
		}
		b, _ := json.Marshal(body)
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		resp, err := doJSON(cctx, m.Client, http.MethodPost, u, p, key, b)
		cancel()
		if err == nil {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
			if resp.StatusCode/100 == 2 {
				out = append(out, apiType)
			}
		}
	}
	return out
}
