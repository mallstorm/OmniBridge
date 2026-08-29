package providers

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"omnibridge/internal/config"
	"strings"
	"sync"
	"time"
)

type gigaToken struct {
	Value   string
	Expires time.Time
}
type gigaTokenCache struct {
	mu         sync.Mutex
	byProvider map[string]gigaToken
}

func newGigaTokenCache() *gigaTokenCache { return &gigaTokenCache{byProvider: map[string]gigaToken{}} }

// GigaTokenState exposes only non-secret OAuth cache metadata for local UI/status.
// The access token value itself never leaves the provider manager.
func (m *Manager) GigaTokenState(providerID string) (time.Time, bool) {
	m.gigaTokens.mu.Lock()
	defer m.gigaTokens.mu.Unlock()
	t, ok := m.gigaTokens.byProvider[providerID]
	if !ok || strings.TrimSpace(t.Value) == "" {
		return time.Time{}, false
	}
	return t.Expires, true
}

func (m *Manager) gigaAccessToken(ctx context.Context, p config.Provider) (string, error) {
	m.gigaTokens.mu.Lock()
	if t, ok := m.gigaTokens.byProvider[p.ID]; ok && time.Until(t.Expires) > 90*time.Second {
		m.gigaTokens.mu.Unlock()
		return t.Value, nil
	}
	m.gigaTokens.mu.Unlock()
	key, err := m.credential(p)
	if err != nil {
		return "", err
	}
	oauth := "https://ngw.devices.sberbank.ru:9443/api/v2/oauth"
	if custom := strings.TrimSpace(p.Quirks["gigachat_oauth_url"]); custom != "" {
		oauth = custom
	}
	scope := p.Scope
	if scope == "" {
		scope = "GIGACHAT_API_PERS"
	}
	form := url.Values{}
	form.Set("scope", scope)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oauth, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Basic "+strings.TrimSpace(key))
	req.Header.Set("RqUID", uuid4())
	resp, err := m.gigaClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", decodeError(resp)
	}
	var v struct {
		AccessToken string `json:"access_token"`
		ExpiresAt   int64  `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return "", err
	}
	if v.AccessToken == "" {
		return "", fmt.Errorf("GigaChat OAuth returned empty access_token")
	}
	exp := time.Now().Add(29 * time.Minute)
	if v.ExpiresAt > 0 {
		if v.ExpiresAt > 10_000_000_000 {
			exp = time.UnixMilli(v.ExpiresAt)
		} else {
			exp = time.Unix(v.ExpiresAt, 0)
		}
	}
	m.gigaTokens.mu.Lock()
	m.gigaTokens.byProvider[p.ID] = gigaToken{Value: v.AccessToken, Expires: exp}
	m.gigaTokens.mu.Unlock()
	return v.AccessToken, nil
}

func uuid4() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func (m *Manager) listGigaModels(ctx context.Context, p config.Provider) ([]Model, error) {
	return m.listGigaModelsAttempt(ctx, p, true)
}

func (m *Manager) clearGigaToken(providerID string) {
	m.gigaTokens.mu.Lock()
	delete(m.gigaTokens.byProvider, providerID)
	m.gigaTokens.mu.Unlock()
}

func (m *Manager) listGigaModelsAttempt(ctx context.Context, p config.Provider, retry401 bool) ([]Model, error) {
	token, err := m.gigaAccessToken(ctx, p)
	if err != nil {
		return nil, err
	}
	base := p.BaseURL
	if base == "" {
		base = "https://api.giga.chat/v1"
	}
	u := p.ModelsURL
	if u == "" {
		u = modelsEndpoint(base)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := m.gigaClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		m.clearGigaToken(p.ID)
		if retry401 {
			return m.listGigaModelsAttempt(ctx, p, false)
		}
		return nil, fmt.Errorf("GigaChat models authorization failed after token refresh: HTTP 401")
	}
	if resp.StatusCode/100 != 2 {
		return nil, decodeError(resp)
	}
	var data openAIModelList
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	out := []Model{}
	for _, x := range data.Data {
		if x.ID == "" || strings.EqualFold(x.Type, "embedder") || strings.EqualFold(x.Type, "aicheck") {
			continue
		}
		out = append(out, Model{ID: p.ID + "/" + x.ID, NativeID: x.ID, ProviderID: p.ID, ProviderName: p.Name, Name: x.ID,
			APIType: "chat-completions", PreferredProtocol: "chat-completions", Protocols: append([]string(nil), p.Protocols...), OwnedBy: x.OwnedBy, Type: x.Type, Streaming: true, ToolCalling: false, Status: "discovered"})
	}
	return out, nil
}

func safeGigaAlias(name string) string {
	h := sha256.Sum256([]byte(name))
	var b strings.Builder
	b.WriteByte('F')
	for i := 0; i < 12; i++ {
		b.WriteByte(byte('a' + h[i]%26))
	}
	return b.String()
}

func sanitizeGigaSchema(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range x {
			switch k {
			case "$schema", "$id", "unevaluatedProperties", "prefixItems", "const", "nullable":
				continue
			}
			if k == "anyOf" || k == "oneOf" {
				if arr, ok := val.([]any); ok {
					for _, candidate := range arr {
						if m, ok := candidate.(map[string]any); ok {
							if t, ok := m["type"].(string); ok && t == "null" {
								continue
							}
							merged := sanitizeGigaSchema(m).(map[string]any)
							for mk, mv := range merged {
								out[mk] = mv
							}
							break
						}
					}
				}
				continue
			}
			if k == "allOf" {
				if arr, ok := val.([]any); ok && len(arr) > 0 {
					if m, ok := sanitizeGigaSchema(arr[0]).(map[string]any); ok {
						for mk, mv := range m {
							out[mk] = mv
						}
					}
				}
				continue
			}
			if k == "type" {
				if arr, ok := val.([]any); ok {
					for _, tv := range arr {
						if s, ok := tv.(string); ok && s != "null" {
							out[k] = s
							break
						}
					}
					continue
				}
			}
			out[k] = sanitizeGigaSchema(val)
		}
		if _, ok := out["type"]; !ok {
			if _, ok := out["properties"]; ok {
				out["type"] = "object"
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = sanitizeGigaSchema(val)
		}
		return out
	default:
		return v
	}
}

func translateOpenAIToGiga(body []byte) ([]byte, map[string]string, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, nil, err
	}
	if model, ok := req["model"].(string); ok {
		_, native := nativeFromQualified(model)
		req["model"] = native
	}
	aliases := map[string]string{}
	reverse := map[string]string{}
	if tools, ok := req["tools"].([]any); ok {
		funcs := []any{}
		for _, raw := range tools {
			tm, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			fn, ok := tm["function"].(map[string]any)
			if !ok {
				continue
			}
			orig, _ := fn["name"].(string)
			if orig == "" {
				continue
			}
			alias := safeGigaAlias(orig)
			aliases[orig] = alias
			reverse[alias] = orig
			gm := map[string]any{"name": alias}
			if d, ok := fn["description"].(string); ok {
				gm["description"] = d
			}
			if p, ok := fn["parameters"]; ok {
				gm["parameters"] = sanitizeGigaSchema(p)
			} else {
				gm["parameters"] = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			funcs = append(funcs, gm)
		}
		req["functions"] = funcs
		delete(req, "tools")
	}
	if tc, ok := req["tool_choice"]; ok {
		switch v := tc.(type) {
		case string:
			if v == "none" {
				req["function_call"] = "none"
			} else {
				req["function_call"] = "auto"
			}
		case map[string]any:
			if fn, ok := v["function"].(map[string]any); ok {
				if n, ok := fn["name"].(string); ok {
					if a := aliases[n]; a != "" {
						req["function_call"] = map[string]any{"name": a}
					}
				}
			}
		}
		delete(req, "tool_choice")
	} else if len(aliases) > 0 {
		req["function_call"] = "auto"
	}
	delete(req, "parallel_tool_calls")
	// Merge system/developer messages and translate tool history.
	if msgs, ok := req["messages"].([]any); ok {
		out := []any{}
		sys := []string{}
		for _, raw := range msgs {
			msg, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			role, _ := msg["role"].(string)
			if role == "system" || role == "developer" {
				if c, ok := msg["content"].(string); ok && c != "" {
					sys = append(sys, c)
				}
				continue
			}
			if role == "assistant" {
				if tcs, ok := msg["tool_calls"].([]any); ok && len(tcs) > 0 {
					for _, tcr := range tcs {
						tc, ok := tcr.(map[string]any)
						if !ok {
							continue
						}
						fn, _ := tc["function"].(map[string]any)
						orig, _ := fn["name"].(string)
						alias := aliases[orig]
						if alias == "" {
							alias = safeGigaAlias(orig)
							aliases[orig] = alias
							reverse[alias] = orig
						}
						gm := map[string]any{"role": "assistant", "content": "", "function_call": map[string]any{"name": alias, "arguments": fn["arguments"]}}
						if id, ok := tc["id"].(string); ok {
							if st, _, ok := decodeGigaToolID(id); ok {
								gm["functions_state_id"] = st
							}
						}
						out = append(out, gm)
					}
					continue
				}
			}
			if role == "tool" {
				id, _ := msg["tool_call_id"].(string)
				_, alias, ok := decodeGigaToolID(id)
				if !ok {
					if n, ok2 := msg["name"].(string); ok2 {
						alias = aliases[n]
						if alias == "" {
							alias = safeGigaAlias(n)
						}
					}
				}
				content := msg["content"]
				if s, ok := content.(string); ok && !json.Valid([]byte(s)) {
					bb, _ := json.Marshal(map[string]any{"result": s})
					content = string(bb)
				}
				out = append(out, map[string]any{"role": "function", "function_name": alias, "content": content})
				continue
			}
			out = append(out, msg)
		}
		if len(sys) > 0 {
			out = append([]any{map[string]any{"role": "system", "content": strings.Join(sys, "\n\n")}}, out...)
		}
		req["messages"] = out
	}
	if len(aliases) > 0 {
		// A short adapter note materially improves editor-agent function selection without touching normal chat.
		if msgs, ok := req["messages"].([]any); ok && len(msgs) > 0 {
			note := "When a tool/function is needed, call one of the provided functions. Do not invent function names."
			first, _ := msgs[0].(map[string]any)
			if first != nil && first["role"] == "system" {
				if c, ok := first["content"].(string); ok {
					first["content"] = c + "\n\n" + note
				}
			} else {
				req["messages"] = append([]any{map[string]any{"role": "system", "content": note}}, msgs...)
			}
		}
	}
	b, err := json.Marshal(req)
	return b, reverse, err
}

func encodeGigaToolID(state, alias string) string {
	return "giga_" + base64.RawURLEncoding.EncodeToString([]byte(state)) + "_" + alias
}
func decodeGigaToolID(id string) (state, alias string, ok bool) {
	if !strings.HasPrefix(id, "giga_") {
		return "", "", false
	}
	rest := strings.TrimPrefix(id, "giga_")
	i := strings.LastIndex(rest, "_")
	if i < 0 {
		return "", "", false
	}
	b, err := base64.RawURLEncoding.DecodeString(rest[:i])
	if err != nil {
		return "", "", false
	}
	return string(b), rest[i+1:], true
}

func translateGigaJSON(data []byte, reverse map[string]string) ([]byte, error) {
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	choices, _ := root["choices"].([]any)
	for _, raw := range choices {
		ch, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if fr, ok := ch["finish_reason"].(string); ok && fr == "function_call" {
			ch["finish_reason"] = "tool_calls"
		}
		for _, field := range []string{"message", "delta"} {
			msg, ok := ch[field].(map[string]any)
			if !ok {
				continue
			}
			fc, ok := msg["function_call"].(map[string]any)
			if !ok {
				continue
			}
			alias, _ := fc["name"].(string)
			orig := reverse[alias]
			if orig == "" {
				orig = alias
			}
			args := fc["arguments"]
			var argstr string
			switch a := args.(type) {
			case string:
				argstr = a
			default:
				bb, _ := json.Marshal(a)
				argstr = string(bb)
			}
			state, _ := msg["functions_state_id"].(string)
			msg["tool_calls"] = []any{map[string]any{"id": encodeGigaToolID(state, alias), "type": "function", "function": map[string]any{"name": orig, "arguments": argstr}}}
			delete(msg, "function_call")
			delete(msg, "functions_state_id")
		}
	}
	return json.Marshal(root)
}

func (m *Manager) proxyGigaChat(ctx context.Context, p config.Provider, body []byte) (*UpstreamResponse, error) {
	return m.proxyGigaChatAttempt(ctx, p, body, true)
}

func (m *Manager) proxyGigaChatAttempt(ctx context.Context, p config.Provider, body []byte, retry401 bool) (*UpstreamResponse, error) {
	token, err := m.gigaAccessToken(ctx, p)
	if err != nil {
		return nil, err
	}
	translated, reverse, err := translateOpenAIToGiga(body)
	if err != nil {
		return nil, err
	}
	base := p.BaseURL
	if base == "" {
		base = "https://api.giga.chat/v1"
	}
	u := endpoint(base, "chat-completions")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(translated))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := clientForBody(m.gigaClient(), body).Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == 401 {
		resp.Body.Close()
		m.clearGigaToken(p.ID)
		if retry401 {
			return m.proxyGigaChatAttempt(ctx, p, body, false)
		}
		return nil, fmt.Errorf("GigaChat authorization failed after token refresh: HTTP 401")
	}
	stream := false
	var meta map[string]any
	if json.Unmarshal(body, &meta) == nil {
		stream, _ = meta["stream"].(bool)
	}
	if !stream {
		b, er := io.ReadAll(resp.Body)
		resp.Body.Close()
		if er != nil {
			return nil, er
		}
		if resp.StatusCode/100 == 2 {
			if tr, er := translateGigaJSON(b, reverse); er == nil {
				b = tr
			}
		}
		return &UpstreamResponse{StatusCode: resp.StatusCode, Header: resp.Header.Clone(), Body: io.NopCloser(bytes.NewReader(b))}, nil
	}
	pr, pw := io.Pipe()
	go func() {
		defer resp.Body.Close()
		defer pw.Close()
		s := bufio.NewScanner(resp.Body)
		buf := make([]byte, 0, 64*1024)
		s.Buffer(buf, 2*1024*1024)
		for s.Scan() {
			line := s.Text()
			if strings.HasPrefix(line, "data:") {
				payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if payload != "" && payload != "[DONE]" {
					if tr, er := translateGigaJSON([]byte(payload), reverse); er == nil {
						line = "data: " + string(tr)
					}
				}
			}
			_, _ = io.WriteString(pw, line+"\n")
		}
		if err := s.Err(); err != nil {
			_ = pw.CloseWithError(err)
		}
	}()
	return &UpstreamResponse{StatusCode: resp.StatusCode, Header: resp.Header.Clone(), Body: pr}, nil
}

func hashKeyFingerprint(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:6])
}

type BalanceResult struct {
	ProviderID string           `json:"provider_id"`
	Status     string           `json:"status"`
	Official   bool             `json:"official"`
	Items      []map[string]any `json:"items,omitempty"`
	Error      string           `json:"error,omitempty"`
}

func (m *Manager) Balance(ctx context.Context, p config.Provider) BalanceResult {
	if p.Kind != "gigachat" {
		return BalanceResult{ProviderID: p.ID, Status: "unsupported", Official: false}
	}
	token, err := m.gigaAccessToken(ctx, p)
	if err != nil {
		return BalanceResult{ProviderID: p.ID, Status: "error", Official: true, Error: err.Error()}
	}
	base := p.BaseURL
	if base == "" {
		base = "https://api.giga.chat/v1"
	}
	u := strings.TrimRight(base, "/") + "/balance"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return BalanceResult{ProviderID: p.ID, Status: "error", Official: true, Error: err.Error()}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "OmniBridge/"+config.CurrentVersion)
	resp, err := m.gigaClient().Do(req)
	if err != nil {
		return BalanceResult{ProviderID: p.ID, Status: "error", Official: true, Error: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode == 403 {
		return BalanceResult{ProviderID: p.ID, Status: "unavailable", Official: true, Error: "balance is unavailable for this billing scheme"}
	}
	if resp.StatusCode/100 != 2 {
		return BalanceResult{ProviderID: p.ID, Status: "error", Official: true, Error: decodeError(resp).Error()}
	}
	var v struct {
		Balance []map[string]any `json:"balance"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return BalanceResult{ProviderID: p.ID, Status: "error", Official: true, Error: err.Error()}
	}
	return BalanceResult{ProviderID: p.ID, Status: "ok", Official: true, Items: v.Balance}
}
