package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"omnibridge/internal/config"
	"strings"
)

// gigaStreamTranslator preserves provider-specific function state across SSE
// chunks. GigaChat may emit functions_state_id/name in an earlier delta and
// arguments in later deltas; translating every JSON event independently would
// otherwise generate unstable/empty OpenAI tool-call IDs.
type gigaStreamTranslator struct {
	choices map[string]*gigaStreamChoiceState
}

type gigaStreamChoiceState struct {
	state string
	alias string
	orig  string
}

func newGigaStreamTranslator() *gigaStreamTranslator {
	return &gigaStreamTranslator{choices: map[string]*gigaStreamChoiceState{}}
}

func (t *gigaStreamTranslator) choice(ch map[string]any) *gigaStreamChoiceState {
	key := fmt.Sprint(ch["index"])
	if key == "<nil>" || key == "" {
		key = "0"
	}
	if t.choices[key] == nil {
		t.choices[key] = &gigaStreamChoiceState{}
	}
	return t.choices[key]
}

func (t *gigaStreamTranslator) Translate(data []byte, reverse map[string]string) ([]byte, error) {
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
		st := t.choice(ch)
		if fr, ok := ch["finish_reason"].(string); ok && fr == "function_call" {
			ch["finish_reason"] = "tool_calls"
		}
		for _, field := range []string{"message", "delta"} {
			msg, ok := ch[field].(map[string]any)
			if !ok {
				continue
			}
			if state, _ := msg["functions_state_id"].(string); state != "" {
				st.state = state
			}
			delete(msg, "functions_state_id")

			fc, ok := msg["function_call"].(map[string]any)
			if !ok {
				continue
			}
			alias, _ := fc["name"].(string)
			if alias != "" {
				st.alias = alias
				st.orig = reverse[alias]
				if st.orig == "" {
					st.orig = alias
				}
			} else {
				alias = st.alias
			}
			orig := st.orig
			if orig == "" && alias != "" {
				orig = reverse[alias]
				if orig == "" {
					orig = alias
				}
			}

			argstr := ""
			if args, exists := fc["arguments"]; exists && args != nil {
				switch a := args.(type) {
				case string:
					argstr = a
				default:
					bb, _ := json.Marshal(a)
					argstr = string(bb)
				}
			}
			if alias != "" || argstr != "" {
				msg["tool_calls"] = []any{map[string]any{
					"id":   encodeGigaToolID(st.state, alias),
					"type": "function",
					"function": map[string]any{
						"name":      orig,
						"arguments": argstr,
					},
				}}
			}
			delete(msg, "function_call")
		}
	}
	return json.Marshal(root)
}

// normalizeGigaAgentToolResults makes all OpenAI tool-result contents a JSON
// string before the legacy GigaChat request translator maps them to role
// `function`. This handles array/object content in addition to plain strings.
func normalizeGigaAgentToolResults(body []byte) []byte {
	var req map[string]any
	if json.Unmarshal(body, &req) != nil {
		return body
	}
	msgs, ok := req["messages"].([]any)
	if !ok {
		return body
	}
	changed := false
	for _, raw := range msgs {
		msg, ok := raw.(map[string]any)
		if !ok || msg["role"] != "tool" {
			continue
		}
		content, exists := msg["content"]
		if !exists {
			continue
		}
		switch v := content.(type) {
		case string:
			if json.Valid([]byte(v)) {
				continue
			}
			bb, _ := json.Marshal(map[string]any{"result": v})
			msg["content"] = string(bb)
			changed = true
		default:
			bb, err := json.Marshal(v)
			if err == nil {
				msg["content"] = string(bb)
				changed = true
			}
		}
	}
	if !changed {
		return body
	}
	out, err := json.Marshal(req)
	if err != nil {
		return body
	}
	return out
}

func gigaRequestStreaming(body []byte) bool {
	var meta map[string]any
	return json.Unmarshal(body, &meta) == nil && func() bool {
		v, _ := meta["stream"].(bool)
		return v
	}()
}

// proxyGigaChatAgentSafe preserves the mature non-streaming implementation and
// upgrades only the agent-sensitive request normalization / SSE translation.
func (m *Manager) proxyGigaChatAgentSafe(ctx context.Context, p config.Provider, body []byte) (*UpstreamResponse, error) {
	body = normalizeGigaAgentToolResults(body)
	if !gigaRequestStreaming(body) {
		return m.proxyGigaChat(ctx, p, body)
	}

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
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		m.gigaTokens.mu.Lock()
		delete(m.gigaTokens.byProvider, p.ID)
		m.gigaTokens.mu.Unlock()
		return m.proxyGigaChatAgentSafe(ctx, p, body)
	}

	pr, pw := io.Pipe()
	go func() {
		defer resp.Body.Close()
		defer pw.Close()
		translator := newGigaStreamTranslator()
		s := bufio.NewScanner(resp.Body)
		s.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
		for s.Scan() {
			line := s.Text()
			if strings.HasPrefix(line, "data:") {
				payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if payload != "" && payload != "[DONE]" {
					if tr, er := translator.Translate([]byte(payload), reverse); er == nil {
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
