package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"omnibridge/internal/config"
	"omnibridge/internal/protocol"
	"omnibridge/internal/providers"
	"omnibridge/internal/secretstore"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var ErrUnsupportedAccount = errors.New("account connector is not available")
var ErrUnsupportedProtocol = errors.New("account connector does not support requested protocol")

type CatalogEntry struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Status         string   `json:"status"`
	Protocols      []string `json:"protocols,omitempty"`
	RequiresHelper string   `json:"requires_helper,omitempty"`
	RequiresConfig string   `json:"requires_config,omitempty"`
	Note           string   `json:"note,omitempty"`
}

func Catalog() []CatalogEntry {
	return []CatalogEntry{
		{ID: "chatgpt-codex", Name: "ChatGPT / Codex", Status: "supported", Protocols: []string{"chat-completions"}, RequiresHelper: "codex", Note: "Official Codex CLI browser login with Codex app-server device-code compatibility. ChatGPT account credentials are managed by Codex, not OmniBridge."},
		{ID: "gemini-developer-oauth", Name: "Google Gemini Developer API OAuth", Status: "supported", Protocols: []string{"chat-completions"}, RequiresConfig: "Google Desktop OAuth client JSON", Note: "Official Gemini Developer API OAuth using your Google Cloud project. This does not consume Gemini CLI / Google AI Pro subscription quota."},
	}
}

type LoginStart struct {
	AccountID       string `json:"account_id,omitempty"`
	AttemptID       string `json:"attempt_id"`
	AuthURL         string `json:"auth_url,omitempty"`
	VerificationURL string `json:"verification_url,omitempty"`
	UserCode        string `json:"user_code,omitempty"`
	Status          string `json:"status"`
	Backend         string `json:"backend,omitempty"`
	BrowserManaged  bool   `json:"browser_managed,omitempty"`
}

type LoginStatus struct {
	AccountID string `json:"account_id,omitempty"`
	AttemptID string `json:"attempt_id"`
	Status    string `json:"status"`
	Success   bool   `json:"success"`
	Error     string `json:"error,omitempty"`
	Email     string `json:"email,omitempty"`
	Plan      string `json:"plan,omitempty"`
	Backend   string `json:"backend,omitempty"`
}

// AccountPreflight is a secret-safe diagnostic snapshot used before an interactive
// account login. It deliberately contains paths/versions/status only; credentials
// and OAuth tokens are never returned.
type AccountPreflight struct {
	AccountID   string         `json:"account_id"`
	Kind        string         `json:"kind"`
	Ready       bool           `json:"ready"`
	Status      string         `json:"status"`
	Backend     string         `json:"backend,omitempty"`
	Helper      string         `json:"helper,omitempty"`
	Version     string         `json:"version,omitempty"`
	Diagnostics map[string]any `json:"diagnostics,omitempty"`
	Warnings    []string       `json:"warnings,omitempty"`
	Error       string         `json:"error,omitempty"`
}

type AccountInfo struct {
	ID             string         `json:"id"`
	Kind           string         `json:"kind"`
	Status         string         `json:"status"`
	Email          string         `json:"email,omitempty"`
	Plan           string         `json:"plan,omitempty"`
	CheckedAt      time.Time      `json:"checked_at"`
	ReauthRequired bool           `json:"reauth_required,omitempty"`
	Details        map[string]any `json:"details,omitempty"`
	Error          string         `json:"error,omitempty"`
}

type UsageInfo struct {
	ID      string         `json:"id"`
	Kind    string         `json:"kind"`
	Status  string         `json:"status"`
	Details map[string]any `json:"details,omitempty"`
	Error   string         `json:"error,omitempty"`
}

type UpstreamResponse struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
}

type pendingLogin struct {
	accountID string
	status    LoginStatus
}

type Manager struct {
	Root    string
	Secrets *secretstore.Store
	Client  *http.Client
	mu      sync.RWMutex
	pending map[string]*pendingLogin
	agents  map[string]*codexPending
}

func NewManager(root string, secrets *secretstore.Store) *Manager {
	return &Manager{Root: root, Secrets: secrets, Client: &http.Client{Timeout: 120 * time.Second}, pending: map[string]*pendingLogin{}, agents: map[string]*codexPending{}}
}

func (m *Manager) ListModels(ctx context.Context, a config.AccountConnection) ([]providers.Model, error) {
	switch a.Kind {
	case "chatgpt-codex":
		return m.listCodexModels(ctx, a)
	case "gemini-developer-oauth":
		return m.listGeminiModels(ctx, a)
	default:
		return nil, ErrUnsupportedAccount
	}
}

func (m *Manager) Proxy(ctx context.Context, a config.AccountConnection, apiType string, body []byte) (*UpstreamResponse, error) {
	switch a.Kind {
	case "chatgpt-codex":
		if apiType != "chat-completions" {
			return nil, ErrUnsupportedProtocol
		}
		return m.proxyCodexChat(ctx, a, body)
	case "gemini-developer-oauth":
		if apiType != "chat-completions" {
			return nil, ErrUnsupportedProtocol
		}
		return m.proxyGeminiChat(ctx, a, body)
	default:
		return nil, ErrUnsupportedAccount
	}
}

func (m *Manager) Probe(ctx context.Context, a config.AccountConnection, model providers.Model, deep bool) providers.ProbeResult {
	res := providers.ProbeResult{ModelID: model.ID, Deep: deep, SelectedProtocol: "chat-completions", Protocols: map[string]providers.ProtocolProbe{}}
	pr := providers.ProtocolProbe{APIType: "chat-completions"}
	start := time.Now()
	status, body, err := m.runAccountProbe(ctx, a, accountProbeBody(model.ID, "basic"))
	pr.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		pr.Error = err.Error()
		res.Error = err.Error()
		res.DoctorStatus = "error"
		res.Protocols["chat-completions"] = pr
		return res
	}
	if status/100 != 2 {
		pr.Error = fmt.Sprintf("HTTP %d: %s", status, strings.TrimSpace(string(body)))
		res.Error = pr.Error
		res.DoctorStatus = "error"
		res.Protocols["chat-completions"] = pr
		return res
	}
	pr.Basic = true
	res.Basic = true
	res.LatencyMS = pr.LatencyMS
	if deep {
		if st, b, e := m.runAccountProbe(ctx, a, accountProbeBody(model.ID, "stream")); e == nil && st/100 == 2 && len(b) > 0 {
			pr.Streaming = true
		}
		if st, b, e := m.runAccountProbe(ctx, a, accountProbeBody(model.ID, "system")); e == nil && st/100 == 2 {
			if rr, e2 := protocol.ParseResponse(protocol.ChatCompletions, b); e2 == nil {
				pr.SystemPrompt = strings.Contains(strings.ToUpper(rr.Text), "SYS_OK")
			}
		}
		var toolBody []byte
		if st, b, e := m.runAccountProbe(ctx, a, accountProbeBody(model.ID, "tool")); e == nil && st/100 == 2 {
			toolBody = b
			if rr, e2 := protocol.ParseResponse(protocol.ChatCompletions, b); e2 == nil && len(rr.ToolCalls) > 0 {
				pr.ToolCalling = true
			}
		}
		if pr.ToolCalling && len(toolBody) > 0 {
			if follow, ok := accountToolFollowup(model.ID, toolBody); ok {
				if st, _, e := m.runAccountProbe(ctx, a, follow); e == nil && st/100 == 2 {
					pr.ToolRoundTrip = true
				}
			}
		}
		if st, b, e := m.runAccountProbe(ctx, a, accountProbeBody(model.ID, "parallel")); e == nil && st/100 == 2 {
			if rr, e2 := protocol.ParseResponse(protocol.ChatCompletions, b); e2 == nil {
				pr.ParallelTools = len(rr.ToolCalls) >= 2
			}
		}
		if st, b, e := m.runAccountProbe(ctx, a, accountProbeBody(model.ID, "structured")); e == nil && st/100 == 2 {
			if rr, e2 := protocol.ParseResponse(protocol.ChatCompletions, b); e2 == nil {
				var x map[string]any
				txt := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(rr.Text), "```json"), "```"))
				if json.Unmarshal([]byte(txt), &x) == nil {
					ok, _ := x["ok"].(bool)
					pr.StructuredOutput = ok
				}
			}
		}
		if model.Vision {
			if st, b, e := m.runAccountProbe(ctx, a, accountProbeBody(model.ID, "vision")); e == nil && st/100 == 2 {
				if rr, e2 := protocol.ParseResponse(protocol.ChatCompletions, b); e2 == nil {
					pr.Vision = strings.Contains(strings.ToUpper(rr.Text), "VIS_Q7Z")
				}
			}
		}
		if model.Reasoning {
			if st, _, e := m.runAccountProbe(ctx, a, accountProbeBody(model.ID, "reasoning")); e == nil && st/100 == 2 {
				pr.Reasoning = true
			}
		}
		pr.AgentCompatible = pr.Basic && pr.Streaming && pr.SystemPrompt && pr.ToolCalling && pr.ToolRoundTrip
	}
	res.Streaming = pr.Streaming
	res.SystemPrompt = pr.SystemPrompt
	res.ToolCalling = pr.ToolCalling
	res.ToolRoundTrip = pr.ToolRoundTrip
	res.ParallelTools = pr.ParallelTools
	res.StructuredOutput = pr.StructuredOutput
	res.Vision = pr.Vision
	res.Reasoning = pr.Reasoning
	res.AgentCompatible = pr.AgentCompatible
	switch {
	case !res.Basic:
		res.DoctorStatus = "error"
	case res.AgentCompatible:
		res.DoctorStatus = "agent_ready"
	case res.Streaming && res.SystemPrompt && res.ToolCalling:
		res.DoctorStatus = "chat_ready"
	case res.Streaming:
		res.DoctorStatus = "chat_only"
	default:
		res.DoctorStatus = "partial"
	}
	res.Protocols["chat-completions"] = pr
	return res
}

func (m *Manager) runAccountProbe(ctx context.Context, a config.AccountConnection, body []byte) (int, []byte, error) {
	cctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	resp, err := m.Proxy(cctx, a, "chat-completions", body)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	return resp.StatusCode, b, nil
}

const accountVisionProbePNG = "iVBORw0KGgoAAAANSUhEUgAAAGAAAAAoCAIAAACO8WhuAAAB60lEQVR4nO3YIavCUBTA8evzYTKYDVZBQUyWiQg3ic0ggtkPIJYF/QLCYMGkwc+gLChYDAoLLgmCiElFYXANymQy7wuDIcLjhCvz8Ti/tMs2z/jjJi7AOSfod1+fvoC/DgMBMBAAAwEwEAADATAQAAMBMBAAAwEwEOBb8Px+v9/pdEKh0P1+r9fr1Wq1Uqkcj0dCiG3b6/XaNE1CSKPRWCwWhBDHcXRdt21b/NJ9wgWMRiNJkhhjnHPGmCRJ0+nU29vr9Vqt1sspqqrKsiwy1GdCgSil8/ncW85ms0Kh4G4/Ho90On06nZ6P3+/3qVTqer2KDPWZUKBoNGpZlre0LCsWi7nbg8GgVqu9HF8ulzVNE5noP9Fn0Mvderlc3G1FUbrd7vPe8XjsOE6xWHzjRB8I/Yolk0nDMLylYRiJRIIQout6JBKJx+PertvtJsuyqqoi4z5D5Os3mUwkSTqfz5xzxlg2m3XvoFKp9Py05pw3m01FUURmfYrQLUYp3e12lNJgMLharQgh2+12s9kcDodcLvd8ZLvdzmQyw+HQXWqaFg6HRUb7JsDf907aNM3lcpnP59/1gX/BOwP9S/hXA4CBABgIgIEAGAiAgQAYCICBABgIgIEAGAiAgQAYCICBABgIgIEAGAiAgQA/xgCxchBZRRUAAAAASUVORK5CYII="

func accountProbeBody(model, feature string) []byte {
	prompt := "Reply exactly OK"
	messages := []any{map[string]any{"role": "user", "content": prompt}}
	v := map[string]any{"model": model, "max_tokens": 64, "stream": feature == "stream", "messages": messages}
	switch feature {
	case "system":
		v["messages"] = []any{
			map[string]any{"role": "system", "content": "Reply exactly SYS_OK. This instruction has priority."},
			map[string]any{"role": "user", "content": "Reply USER_BAD"},
		}
	case "tool":
		v["messages"] = []any{map[string]any{"role": "user", "content": "Call test_probe with value 7. Do not answer normally."}}
		v["tools"] = accountTools(false)
		v["tool_choice"] = "required"
	case "parallel":
		v["messages"] = []any{map[string]any{"role": "user", "content": "Call both test_probe_a and test_probe_b."}}
		v["tools"] = accountTools(true)
		v["tool_choice"] = "required"
		v["parallel_tool_calls"] = true
	case "structured":
		v["messages"] = []any{map[string]any{"role": "user", "content": `Return JSON exactly {"ok":true}.`}}
		v["response_format"] = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "probe", "strict": true, "schema": map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}}, "required": []string{"ok"}, "additionalProperties": false}}}
	case "vision":
		v["messages"] = []any{map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "Read the three-character code shown in the image. Reply exactly VIS_<code>."},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + accountVisionProbePNG}},
		}}}
	case "reasoning":
		v["messages"] = []any{map[string]any{"role": "user", "content": "Reply only 4: 2+2"}}
		v["reasoning_effort"] = "low"
	}
	b, _ := json.Marshal(v)
	return b
}

func accountTools(parallel bool) []any {
	schema := map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "integer"}}, "required": []string{"value"}}
	if parallel {
		return []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "test_probe_a", "parameters": schema}},
			map[string]any{"type": "function", "function": map[string]any{"name": "test_probe_b", "parameters": schema}},
		}
	}
	return []any{map[string]any{"type": "function", "function": map[string]any{"name": "test_probe", "parameters": schema}}}
}

func accountToolFollowup(model string, first []byte) ([]byte, bool) {
	rr, err := protocol.ParseResponse(protocol.ChatCompletions, first)
	if err != nil || len(rr.ToolCalls) == 0 {
		return nil, false
	}
	tc := rr.ToolCalls[0]
	assistant := map[string]any{
		"role":    "assistant",
		"content": rr.Text,
		"tool_calls": []any{map[string]any{
			"id": tc.ID, "type": "function",
			"function": map[string]any{"name": tc.Name, "arguments": protocol.RepairArguments(tc.Arguments)},
		}},
	}
	v := map[string]any{
		"model": model, "max_tokens": 48, "stream": false,
		"messages": []any{
			map[string]any{"role": "user", "content": "Call test_probe with value 7."},
			assistant,
			map[string]any{"role": "tool", "tool_call_id": tc.ID, "content": `{"value":7}`},
		},
	}
	b, _ := json.Marshal(v)
	return b, true
}

func classifyAccountError(err error) string {
	if err == nil {
		return "connected"
	}
	x := strings.ToLower(err.Error())
	switch {
	case strings.Contains(x, "usage limit"), strings.Contains(x, "rate limit"), strings.Contains(x, "http 429"):
		return "rate_limited"
	case strings.Contains(x, "not connected"), strings.Contains(x, "not logged"), strings.Contains(x, "refresh token missing"), strings.Contains(x, "invalid_grant"), strings.Contains(x, "unauthorized"), strings.Contains(x, "http 401"):
		return "reauth_required"
	case strings.Contains(x, "not found in path"), strings.Contains(x, "executable not found"), strings.Contains(x, "oauth client json is required"), strings.Contains(x, "invalid google desktop oauth"):
		return "configuration_required"
	case strings.Contains(x, "deadline exceeded"), strings.Contains(x, "timed out"), strings.Contains(x, "connection refused"), strings.Contains(x, "no such host"):
		return "offline"
	default:
		return "error"
	}
}

func (m *Manager) accountDir(a config.AccountConnection) string {
	return filepath.Join(m.Root, "data", "accounts", a.ID)
}
