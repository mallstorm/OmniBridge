package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"omnibridge/internal/accounts"
	"omnibridge/internal/config"
	"omnibridge/internal/health"
	"omnibridge/internal/providers"
	"omnibridge/internal/registry"
	"omnibridge/internal/secretstore"
	"omnibridge/internal/stats"
	"omnibridge/internal/storage"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOpenAICompatibleEndToEnd(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "bad auth", 401)
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []any{map[string]any{"id": "model-a", "object": "model", "owned_by": "mock"}}})
		case "/v1/chat/completions":
			b, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(b), `"model":"model-a"`) {
				http.Error(w, "qualified id leaked", 400)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"id": "x", "object": "chat.completion", "model": "model-a", "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "OK"}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 2, "completion_tokens": 1, "total_tokens": 3}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	dir := t.TempDir()
	cfg, err := config.New(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	_ = sec.Put("provider:mock", "secret")
	p := config.NormalizeProvider(config.Provider{ID: "mock", Name: "Mock", Kind: "openai-compatible", BaseURL: upstream.URL + "/v1", APIType: "chat-completions", AuthType: "bearer", CredentialRef: "provider:mock", Enabled: true})
	if err := cfg.UpsertProvider(p); err != nil {
		t.Fatal(err)
	}
	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	reg := registry.New(filepath.Join(dir, "models.json"), cfg, pm, am)
	reg.RefreshAll(context.Background())
	st := stats.New(filepath.Join(dir, "stats.json"))
	srv := New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	c := cfg.Get()
	req, _ := http.NewRequest("GET", ts.URL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+c.DataAPIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("models status=%d", resp.StatusCode)
	}
	mb, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var catalog struct {
		Object string `json:"object"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(mb, &catalog); err != nil {
		t.Fatalf("models JSON is not OpenAI-compatible: %v body=%s", err, mb)
	}
	if catalog.Object != "list" {
		t.Fatalf("models object=%q body=%s", catalog.Object, mb)
	}
	foundModel := false
	for _, item := range catalog.Data {
		if item.ID == "mock/model-a" {
			foundModel = true
			break
		}
	}
	if !foundModel {
		t.Fatalf("Cline-compatible model id missing: %s", mb)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/v1/chat/completions", strings.NewReader(`{"model":"mock/model-a","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+c.DataAPIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	rb, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("chat status=%d body=%s", resp.StatusCode, rb)
	}
	if !strings.Contains(string(rb), "OK") {
		t.Fatalf("body=%s", rb)
	}
	if st.Snapshot().Total.TotalTokens != 3 {
		t.Fatalf("tokens=%d", st.Snapshot().Total.TotalTokens)
	}
	if err := cfg.SetRouting(config.RoutingConfig{Aliases: map[string]string{"coding-fast": "mock/model-a"}, Priorities: map[string][]string{}, Fallbacks: map[string][]string{}, MaxRetries: 0}); err != nil {
		t.Fatal(err)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/v1/chat/completions", strings.NewReader(`{"model":"coding-fast","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+c.DataAPIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	rb, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(rb), "OK") {
		t.Fatalf("alias route status=%d body=%s", resp.StatusCode, rb)
	}
	_ = os.RemoveAll(dir)
}

func TestDataPlaneAcceptsMessagesAPIKeyHeader(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.New(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	reg := registry.New(filepath.Join(dir, "models.json"), cfg, pm, am)
	st := stats.New(filepath.Join(dir, "stats.json"))
	srv := New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/models", nil)
	req.Header.Set("x-api-key", cfg.Get().DataAPIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("x-api-key data-plane auth status=%d", resp.StatusCode)
	}
}

func TestAddCustomProviderLearnsSameOriginAuth(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("x-api-key") != "custom-secret" {
			http.Error(w, "bad auth", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []any{map[string]any{"id": "custom-chat"}}})
	}))
	defer upstream.Close()

	dir := t.TempDir()
	cfg, err := config.New(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	reg := registry.New(filepath.Join(dir, "models.json"), cfg, pm, am)
	st := stats.New(filepath.Join(dir, "stats.json"))
	srv := New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	payload := fmt.Sprintf(`{"name":"Custom","kind":"openai-compatible","base_url":%q,"api_key":"custom-secret"}`, upstream.URL)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/bridge/providers", strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+cfg.Get().AdminAPIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add status=%d body=%s", resp.StatusCode, b)
	}
	providersCfg := cfg.Get().Providers
	if len(providersCfg) != 1 {
		t.Fatalf("providers=%d", len(providersCfg))
	}
	if providersCfg[0].AuthType != "x-api-key" || providersCfg[0].BaseURL != upstream.URL+"/v1" {
		t.Fatalf("learned provider=%+v", providersCfg[0])
	}
}

func TestAccountCatalogIsSeparateFromAPIProviders(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.New(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	reg := registry.New(filepath.Join(dir, "models.json"), cfg, pm, am)
	st := stats.New(filepath.Join(dir, "stats.json"))
	ts := httptest.NewServer(New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0)).Handler())
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/bridge/account-catalog", nil)
	req.Header.Set("Authorization", "Bearer "+cfg.Get().AdminAPIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), "chatgpt-codex") || !strings.Contains(string(b), "gemini-developer-oauth") {
		t.Fatalf("catalog status=%d body=%s", resp.StatusCode, b)
	}
	for _, forbidden := range []string{"qwen-account", "deepseek-account", "claude-subscription", "gemini-cli-subscription"} {
		if strings.Contains(string(b), forbidden) {
			t.Fatalf("unsupported account connector %q must not be exposed: %s", forbidden, b)
		}
	}
	if len(cfg.Get().Providers) != 0 || len(cfg.Get().Accounts) != 0 {
		t.Fatal("catalog read must not create credentials")
	}
}

func TestLocalScanReportsOnlyRuntimesWithModels(t *testing.T) {
	dir := t.TempDir()
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	pm := providers.NewManager(sec)
	pm.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"object":"list","data":[]}`
		if r.URL.Host == "127.0.0.1:11434" {
			body = `{"object":"list","data":[{"id":"llama-local","object":"model"}]}`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})}
	srv := &Server{Providers: pm}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/bridge/local-scan", nil)
	srv.localScan(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("local scan status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got []struct {
		Kind    string `json:"kind"`
		Name    string `json:"name"`
		BaseURL string `json:"base_url"`
		Models  int    `json:"models"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != "ollama" || got[0].Name != "Ollama" || got[0].Models != 1 {
		t.Fatalf("local scan=%+v", got)
	}
}

func TestUniversalTranslationChatToAnthropicMessages(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "anth-secret" {
			http.Error(w, "bad auth", 401)
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "claude-test", "display_name": "Claude Test", "type": "model"}}})
		case "/v1/messages":
			b, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(b), `"model":"claude-test"`) || !strings.Contains(string(b), `"messages"`) {
				http.Error(w, "bad translated body", 400)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "msg1", "type": "message", "model": "claude-test", "role": "assistant", "content": []any{map[string]any{"type": "text", "text": "translated-ok"}}, "stop_reason": "end_turn", "usage": map[string]any{"input_tokens": 5, "output_tokens": 2}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	dir := t.TempDir()
	cfg, _ := config.New(filepath.Join(dir, "config.json"))
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	_ = sec.Put("provider:anth", "anth-secret")
	p := config.NormalizeProvider(config.Provider{ID: "anth", Name: "Anth", Kind: "anthropic", BaseURL: upstream.URL + "/v1", ModelsURL: upstream.URL + "/v1/models", CredentialRef: "provider:anth", Enabled: true})
	if err := cfg.UpsertProvider(p); err != nil {
		t.Fatal(err)
	}
	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	reg := registry.New(filepath.Join(dir, "models.json"), cfg, pm, am)
	reg.RefreshAll(context.Background())
	st := stats.New(filepath.Join(dir, "stats.json"))
	ts := httptest.NewServer(New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0)).Handler())
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(`{"model":"anth/claude-test","messages":[{"role":"system","content":"sys"},{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+cfg.Get().DataAPIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), "translated-ok") || !strings.Contains(string(b), "chat.completion") {
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
}

func TestUniversalTranslationMessagesToChat(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "chat-only"}}})
			return
		}
		if r.URL.Path == "/v1/chat/completions" {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "c1", "model": "chat-only", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "hello-from-chat"}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 2, "completion_tokens": 3, "total_tokens": 5}})
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()
	dir := t.TempDir()
	cfg, _ := config.New(filepath.Join(dir, "config.json"))
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	p := config.NormalizeProvider(config.Provider{ID: "mock", Name: "Mock", Kind: "custom", BaseURL: upstream.URL + "/v1", ModelsURL: upstream.URL + "/v1/models", AuthType: "none", Enabled: true, Protocols: []string{"chat-completions"}, PreferredProtocol: "chat-completions"})
	if err := cfg.UpsertProvider(p); err != nil {
		t.Fatal(err)
	}
	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	reg := registry.New(filepath.Join(dir, "models.json"), cfg, pm, am)
	reg.RefreshAll(context.Background())
	st := stats.New(filepath.Join(dir, "stats.json"))
	ts := httptest.NewServer(New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0)).Handler())
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/messages", strings.NewReader(`{"model":"mock/chat-only","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("x-api-key", cfg.Get().DataAPIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), "hello-from-chat") || !strings.Contains(string(b), `"type":"message"`) {
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
}

func TestEmbeddingsProxyQualifiedProvider(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			http.NotFound(w, r)
			return
		}
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), `"model":"embed-model"`) {
			http.Error(w, "bad model", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []any{map[string]any{"object": "embedding", "embedding": []float64{0.1, 0.2}, "index": 0}}, "model": "embed-model", "usage": map[string]any{"prompt_tokens": 4, "total_tokens": 4}})
	}))
	defer upstream.Close()
	dir := t.TempDir()
	cfg, _ := config.New(filepath.Join(dir, "config.json"))
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	p := config.NormalizeProvider(config.Provider{ID: "mock", Name: "Mock", Kind: "custom", BaseURL: upstream.URL + "/v1", AuthType: "none", Enabled: true})
	if err := cfg.UpsertProvider(p); err != nil {
		t.Fatal(err)
	}
	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	reg := registry.New(filepath.Join(dir, "models.json"), cfg, pm, am)
	st := stats.New(filepath.Join(dir, "stats.json"))
	ts := httptest.NewServer(New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0)).Handler())
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/embeddings", strings.NewReader(`{"model":"mock/embed-model","input":"hello"}`))
	req.Header.Set("Authorization", "Bearer "+cfg.Get().DataAPIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), "embedding") {
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
}

func TestCrossProtocolStreamingReturnsCompatibleSSE(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "claude-stream"}}})
		case "/v1/messages":
			var v map[string]any
			_ = json.NewDecoder(r.Body).Decode(&v)
			if stream, _ := v["stream"].(bool); !stream {
				t.Errorf("cross-protocol upstream must remain streaming")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fl, _ := w.(http.Flusher)
			_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m-stream\",\"model\":\"claude-stream\",\"usage\":{\"input_tokens\":1}}}\n\n")
			if fl != nil {
				fl.Flush()
			}
			_, _ = io.WriteString(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
			_, _ = io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"stream-ok\"}}\n\n")
			_, _ = io.WriteString(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n")
			_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			if fl != nil {
				fl.Flush()
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	dir := t.TempDir()
	cfg, _ := config.New(filepath.Join(dir, "config.json"))
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	_ = sec.Put("provider:a", "secret")
	p := config.NormalizeProvider(config.Provider{ID: "a", Name: "A", Kind: "anthropic", BaseURL: upstream.URL + "/v1", ModelsURL: upstream.URL + "/v1/models", CredentialRef: "provider:a", Enabled: true})
	_ = cfg.UpsertProvider(p)
	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	reg := registry.New(filepath.Join(dir, "models.json"), cfg, pm, am)
	reg.RefreshAll(context.Background())
	st := stats.New(filepath.Join(dir, "stats.json"))
	ts := httptest.NewServer(New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0)).Handler())
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(`{"model":"a/claude-stream","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+cfg.Get().DataAPIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") || !strings.Contains(string(b), "stream-ok") || !strings.Contains(string(b), "[DONE]") {
		t.Fatalf("status=%d ct=%s body=%s", resp.StatusCode, resp.Header.Get("Content-Type"), b)
	}
}

func TestAgentBridgeRepairsMalformedToolArguments(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "agent"}}})
			return
		}
		if r.URL.Path == "/v1/chat/completions" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"lookup","arguments":"{\\"value\\":7}"}}]},"finish_reason":"tool_calls"}]}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()
	dir := t.TempDir()
	cfg, _ := config.New(filepath.Join(dir, "config.json"))
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	p := config.NormalizeProvider(config.Provider{ID: "mock", Name: "Mock", Kind: "custom", BaseURL: upstream.URL + "/v1", ModelsURL: upstream.URL + "/v1/models", AuthType: "none", Enabled: true, Protocols: []string{"chat-completions"}, PreferredProtocol: "chat-completions"})
	_ = cfg.UpsertProvider(p)
	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	reg := registry.New(filepath.Join(dir, "models.json"), cfg, pm, am)
	reg.RefreshAll(context.Background())
	st := stats.New(filepath.Join(dir, "stats.json"))
	ts := httptest.NewServer(New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0)).Handler())
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(`{"model":"mock/agent","messages":[{"role":"user","content":"tool"}]}`))
	req.Header.Set("Authorization", "Bearer "+cfg.Get().DataAPIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !json.Valid(b) {
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
	if !strings.Contains(string(b), `{\"value\":7}`) {
		t.Fatalf("arguments not normalized: %s", b)
	}
}

func TestRouter2FastestAndLocalPolicies(t *testing.T) {
	mk := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/models":
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": name}}})
			case "/v1/chat/completions":
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": name}, "finish_reason": "stop"}}})
			default:
				http.NotFound(w, r)
			}
		}))
	}
	local := mk("local-model")
	defer local.Close()
	cloud := mk("cloud-model")
	defer cloud.Close()
	dir := t.TempDir()
	cfg, _ := config.New(filepath.Join(dir, "config.json"))
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	for _, p := range []config.Provider{
		{ID: "local", Name: "Local", Kind: "custom", BaseURL: local.URL + "/v1", ModelsURL: local.URL + "/v1/models", AuthType: "none", Enabled: true, Protocols: []string{"chat-completions"}},
		{ID: "cloud", Name: "Cloud", Kind: "custom", BaseURL: strings.Replace(cloud.URL, "127.0.0.1", "example.invalid", 1) + "/v1", ModelsURL: cloud.URL + "/v1/models", AuthType: "none", Enabled: true, Protocols: []string{"chat-completions"}},
	} {
		if err := cfg.UpsertProvider(p); err != nil {
			t.Fatal(err)
		}
	}
	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	reg := registry.New(filepath.Join(dir, "models.json"), cfg, pm, am)
	reg.RefreshAll(context.Background())
	st := stats.New(filepath.Join(dir, "stats.json"))
	st.Record("local", "local/local-model", false, 0, 0, 0, 40)
	st.Record("cloud", "cloud/cloud-model", false, 0, 0, 0, 10)
	srv := New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0))
	if err := cfg.SetRouting(config.RoutingConfig{
		Aliases: map[string]string{}, Priorities: map[string][]string{}, Fallbacks: map[string][]string{}, MaxRetries: 2,
		Policies: map[string]config.RoutingPolicy{
			"fast":        {Mode: "fastest", Candidates: []string{"local/local-model", "cloud/cloud-model"}},
			"local-route": {Mode: "local_only", Candidates: []string{"local/local-model", "cloud/cloud-model"}},
		}, CircuitBreakerThreshold: 3, CircuitBreakerCooldownSeconds: 30,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := srv.routingCandidates("fast")
	if err != nil || len(got) < 2 || got[0].ID != "cloud/cloud-model" {
		t.Fatalf("fastest=%+v err=%v", got, err)
	}
	got, err = srv.routingCandidates("local-route")
	if err != nil || len(got) != 1 || got[0].ID != "local/local-model" {
		t.Fatalf("local policy=%+v err=%v", got, err)
	}
}

func TestRouter2CircuitBreakerSkipsRepeatedBadCandidate(t *testing.T) {
	badCalls := 0
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "bad"}}})
			return
		}
		badCalls++
		http.Error(w, "capacity", http.StatusServiceUnavailable)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "good"}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "OK"}, "finish_reason": "stop"}}})
	}))
	defer good.Close()
	dir := t.TempDir()
	cfg, _ := config.New(filepath.Join(dir, "config.json"))
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	for _, p := range []config.Provider{
		{ID: "badp", Name: "Bad", Kind: "custom", BaseURL: bad.URL + "/v1", ModelsURL: bad.URL + "/v1/models", AuthType: "none", Enabled: true, Protocols: []string{"chat-completions"}},
		{ID: "goodp", Name: "Good", Kind: "custom", BaseURL: good.URL + "/v1", ModelsURL: good.URL + "/v1/models", AuthType: "none", Enabled: true, Protocols: []string{"chat-completions"}},
	} {
		if err := cfg.UpsertProvider(p); err != nil {
			t.Fatal(err)
		}
	}
	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	reg := registry.New(filepath.Join(dir, "models.json"), cfg, pm, am)
	reg.RefreshAll(context.Background())
	st := stats.New(filepath.Join(dir, "stats.json"))
	srv := New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0))
	if err := cfg.SetRouting(config.RoutingConfig{Aliases: map[string]string{}, Priorities: map[string][]string{}, Fallbacks: map[string][]string{}, Policies: map[string]config.RoutingPolicy{"route": {Mode: "priority", Candidates: []string{"badp/bad", "goodp/good"}}}, MaxRetries: 2, CircuitBreakerThreshold: 1, CircuitBreakerCooldownSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	for i := 0; i < 2; i++ {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(`{"model":"route","messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("Authorization", "Bearer "+cfg.Get().DataAPIKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.Contains(string(b), "OK") {
			t.Fatalf("iteration=%d status=%d body=%s", i, resp.StatusCode, b)
		}
	}
	if badCalls != 1 {
		t.Fatalf("circuit breaker should skip bad model after first failure, calls=%d", badCalls)
	}
}

func TestObservability2PeriodEndpointIsActuallyPeriodSpecific(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := config.New(filepath.Join(dir, "config.json"))
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	reg := registry.New(filepath.Join(dir, "models.json"), cfg, pm, am)
	st := stats.New(filepath.Join(dir, "stats.json"))
	st.Record("p1", "p1/m1", false, 2, 3, 5, 20)
	st.Record("p1", "p1/m1", false, 2, 3, 5, 40)
	hs := health.New(filepath.Join(dir, "health.json"))
	ts := httptest.NewServer(New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0), hs).Handler())
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/bridge/statistics?period=24h", nil)
	req.Header.Set("Authorization", "Bearer "+cfg.Get().AdminAPIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got struct {
		Period string `json:"period"`
		Total  struct {
			Requests int64   `json:"requests"`
			Average  float64 `json:"average_latency_ms"`
			P95      int64   `json:"p95_latency_ms"`
		} `json:"total"`
		Providers map[string]struct {
			Requests int64 `json:"requests"`
		} `json:"providers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Period != "24h" || got.Total.Requests != 2 || got.Total.Average != 30 || got.Total.P95 != 40 || got.Providers["p1"].Requests != 2 {
		t.Fatalf("bad observability response: %+v", got)
	}
}

func TestAccountReliabilityPersistsRefreshTelemetry(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := config.New(filepath.Join(dir, "config.json"))
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	reg := registry.New(filepath.Join(dir, "models.json"), cfg, pm, am)
	st := stats.New(filepath.Join(dir, "stats.json"))
	hs := health.New(filepath.Join(dir, "health.json"))
	srv := New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0), hs)
	a := config.AccountConnection{ID: "gemini-test", Name: "Gemini", Kind: "gemini-developer-oauth", Enabled: true, Settings: map[string]string{}}
	if err := cfg.UpsertAccount(a); err != nil {
		t.Fatal(err)
	}
	info := srv.refreshAccountState(context.Background(), cfg.Get().Accounts[0])
	if info.Status != "configuration_required" {
		t.Fatalf("status=%+v", info)
	}
	got := cfg.Get().Accounts[0]
	if got.LastChecked.IsZero() || got.RefreshFailures != 1 || got.LastErrorKind != "configuration_required" {
		t.Fatalf("telemetry=%+v", got)
	}
	if events := hs.ForID("gemini-test", 10); len(events) != 1 || events[0].Status != "configuration_required" {
		t.Fatalf("health events=%+v", events)
	}
}

func TestTrayDashboardStatusAndGatewayPauseResume(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.New(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	if err := sec.Put("provider:giga", "top-secret-basic-key"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.UpsertProvider(config.NormalizeProvider(config.Provider{ID: "giga", Name: "GigaChat Personal", Kind: "gigachat", CredentialRef: "provider:giga", Enabled: true})); err != nil {
		t.Fatal(err)
	}
	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	reg := registry.New(filepath.Join(dir, "models.json"), cfg, pm, am)
	st := stats.New(filepath.Join(dir, "stats.json"))
	st.Record("giga", "giga/GigaChat", false, 12, 7, 19, 140)
	srv := New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	admin := func(method, path string) (*http.Response, []byte) {
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+cfg.Get().AdminAPIKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return resp, b
	}

	resp, b := admin(http.MethodGet, "/bridge/tray")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tray status=%d body=%s", resp.StatusCode, b)
	}
	text := string(b)
	for _, required := range []string{`"api_enabled":1`, `"GigaChat Personal"`, `"statistics_24h"`, `"oauth_token_cached":false`} {
		if !strings.Contains(text, required) {
			t.Fatalf("tray missing %s: %s", required, text)
		}
	}
	if strings.Contains(text, "top-secret-basic-key") || strings.Contains(text, cfg.Get().AdminAPIKey) || strings.Contains(text, cfg.Get().DataAPIKey) {
		t.Fatalf("tray leaked a secret: %s", text)
	}

	resp, b = admin(http.MethodPost, "/bridge/tray/stop")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), `"paused":true`) {
		t.Fatalf("stop status=%d body=%s", resp.StatusCode, b)
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+cfg.Get().DataAPIKey)
	dataResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	pausedBody, _ := io.ReadAll(dataResp.Body)
	_ = dataResp.Body.Close()
	if dataResp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(pausedBody), "gateway_paused") {
		t.Fatalf("paused data plane status=%d body=%s", dataResp.StatusCode, pausedBody)
	}
	resp, _ = admin(http.MethodGet, "/bridge/tray")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin tray must remain available while paused: %d", resp.StatusCode)
	}

	resp, b = admin(http.MethodPost, "/bridge/tray/start")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), `"paused":false`) {
		t.Fatalf("start status=%d body=%s", resp.StatusCode, b)
	}
	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+cfg.Get().DataAPIKey)
	dataResp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = dataResp.Body.Close()
	if dataResp.StatusCode != http.StatusOK {
		t.Fatalf("resumed data plane status=%d", dataResp.StatusCode)
	}
}

func TestTrayRefreshIsAsynchronous(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := config.New(filepath.Join(dir, "config.json"))
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	reg := registry.New(filepath.Join(dir, "models.json"), cfg, pm, am)
	st := stats.New(filepath.Join(dir, "stats.json"))
	srv := New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/bridge/tray/refresh", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+cfg.Get().AdminAPIKey)
	started := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted || !strings.Contains(string(b), `"running":true`) {
		t.Fatalf("refresh status=%d body=%s", resp.StatusCode, b)
	}
	if time.Since(started) > time.Second {
		t.Fatal("Tray refresh must return immediately instead of blocking the UI")
	}
	// The endpoint is intentionally asynchronous; wait only for test cleanup so
	// background registry writes cannot race t.TempDir removal.
	deadline := time.Now().Add(3 * time.Second)
	for srv.trayRefreshing.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if srv.trayRefreshing.Load() {
		t.Fatal("Tray refresh background job did not settle")
	}
}

func TestStorageStatusAndCheckpointEndpoints(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "omnibridge.db"))
	if err != nil {
		if strings.Contains(err.Error(), "requires cgo") {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	defer db.Close()
	cfg, err := config.NewPersistent(filepath.Join(dir, "config.json"), db)
	if err != nil {
		t.Fatal(err)
	}
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	if err := cfg.EnsureProtectedBridgeKeys(sec); err != nil {
		t.Fatal(err)
	}
	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	defer am.Close()
	reg := registry.NewPersistent(filepath.Join(dir, "model-registry.json"), db, cfg, pm, am)
	st := stats.NewPersistent(filepath.Join(dir, "usage-stats.json"), db)
	hs := health.NewPersistent(filepath.Join(dir, "connection-health.json"), db)
	srv := New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0), hs)
	srv.SetStorage(db)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	for _, tc := range []struct{ method, path string }{{"GET", "/bridge/storage/status"}, {"POST", "/bridge/storage/checkpoint"}} {
		req, _ := http.NewRequest(tc.method, ts.URL+tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+cfg.Get().AdminAPIKey)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%s %s status=%d body=%s", tc.method, tc.path, resp.StatusCode, body)
		}
		if !strings.Contains(string(body), `"ok":true`) {
			t.Fatalf("unexpected body: %s", body)
		}
	}
}

func TestProviderEngineStatusReportsDataDrivenCatalog(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.New(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	reg := registry.New(filepath.Join(dir, "models.json"), cfg, pm, am)
	st := stats.New(filepath.Join(dir, "stats.json"))
	ts := httptest.NewServer(New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0)).Handler())
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/bridge/provider-engine", nil)
	req.Header.Set("Authorization", "Bearer "+cfg.Get().AdminAPIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
	var v struct {
		Definitions int            `json:"definitions"`
		DataDriven  bool           `json:"data_driven"`
		ByExecutor  map[string]int `json:"by_executor"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	if v.Definitions < 80 || !v.DataDriven || v.ByExecutor["openai-compatible"] < 50 {
		t.Fatalf("unexpected engine status: %+v", v)
	}
}

func TestResilience3ConnectionPoolFallbackAndPersistentStatus(t *testing.T) {
	var badCalls, goodCalls int
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "shared-model"}}})
		case "/v1/chat/completions":
			badCalls++
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":{"message":"temporary upstream failure"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "shared-model"}}})
		case "/v1/chat/completions":
			goodCalls++
			json.NewEncoder(w).Encode(map[string]any{"id": "ok", "object": "chat.completion", "model": "shared-model", "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "OK"}, "finish_reason": "stop"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer good.Close()

	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "omnibridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg, err := config.NewPersistent(filepath.Join(dir, "config.json"), db)
	if err != nil {
		t.Fatal(err)
	}
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	for _, p := range []config.Provider{
		{ID: "a-bad", Name: "Pool bad", Kind: "custom", BaseURL: bad.URL + "/v1", ModelsURL: bad.URL + "/v1/models", AuthType: "none", Enabled: true, Protocols: []string{"chat-completions"}, PreferredProtocol: "chat-completions"},
		{ID: "b-good", Name: "Pool good", Kind: "custom", BaseURL: good.URL + "/v1", ModelsURL: good.URL + "/v1/models", AuthType: "none", Enabled: true, Protocols: []string{"chat-completions"}, PreferredProtocol: "chat-completions"},
	} {
		if err := cfg.UpsertProvider(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := cfg.SetRouting(config.RoutingConfig{Aliases: map[string]string{}, Priorities: map[string][]string{}, Fallbacks: map[string][]string{}, Policies: map[string]config.RoutingPolicy{}, MaxRetries: 2, CircuitBreakerThreshold: 1, CircuitBreakerCooldownSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	reg := registry.NewPersistent(filepath.Join(dir, "models.json"), db, cfg, pm, am)
	reg.RefreshAll(context.Background())
	st := stats.NewPersistent(filepath.Join(dir, "stats.json"), db)
	srv := New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0))
	srv.SetStorage(db)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	for i := 0; i < 2; i++ {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(`{"model":"shared-model","messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("Authorization", "Bearer "+cfg.Get().DataAPIKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.Contains(string(b), "OK") {
			t.Fatalf("iteration=%d status=%d body=%s", i, resp.StatusCode, b)
		}
	}
	if badCalls != 1 || goodCalls != 2 {
		t.Fatalf("pool fallback calls bad=%d good=%d", badCalls, goodCalls)
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/bridge/resilience", nil)
	req.Header.Set("Authorization", "Bearer "+cfg.Get().AdminAPIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var status struct {
		Persistent      bool                          `json:"persistent"`
		Breakers        []struct{ Key, State string } `json:"breakers"`
		ConnectionPools []struct {
			Pool string
			Open int
		} `json:"connection_pools"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if !status.Persistent {
		t.Fatal("resilience should report persistent=true")
	}
	foundOpen := false
	for _, b := range status.Breakers {
		if b.Key == "connection:api:a-bad" && b.State == "OPEN" {
			foundOpen = true
		}
	}
	if !foundOpen {
		t.Fatalf("open connection breaker not reported: %+v", status.Breakers)
	}
	counts, err := db.Counts()
	if err != nil {
		t.Fatal(err)
	}
	if counts["circuit_breakers"] < 1 || counts["resilience_events"] < 1 {
		t.Fatalf("persistence counts=%+v", counts)
	}
}

func TestResilience3InvalidRequestDoesNotPoisonProvider(t *testing.T) {
	calls := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "m"}}})
		case "/v1/chat/completions":
			calls++
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid temperature"}}`))
		}
	}))
	defer up.Close()
	dir := t.TempDir()
	cfg, _ := config.New(filepath.Join(dir, "config.json"))
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	if err := cfg.UpsertProvider(config.Provider{ID: "p", Name: "P", Kind: "custom", BaseURL: up.URL + "/v1", ModelsURL: up.URL + "/v1/models", AuthType: "none", Enabled: true, Protocols: []string{"chat-completions"}, PreferredProtocol: "chat-completions"}); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetRouting(config.RoutingConfig{Aliases: map[string]string{}, Priorities: map[string][]string{}, Fallbacks: map[string][]string{}, Policies: map[string]config.RoutingPolicy{}, MaxRetries: 2, CircuitBreakerThreshold: 1, CircuitBreakerCooldownSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	reg := registry.New(filepath.Join(dir, "models.json"), cfg, pm, am)
	reg.RefreshAll(context.Background())
	st := stats.New(filepath.Join(dir, "stats.json"))
	srv := New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	for i := 0; i < 2; i++ {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"x"}]}`))
		req.Header.Set("Authorization", "Bearer "+cfg.Get().DataAPIKey)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Fatalf("status=%d", resp.StatusCode)
		}
	}
	if calls != 2 {
		t.Fatalf("invalid request poisoned provider, calls=%d", calls)
	}
	for _, b := range srv.Resilience.Snapshots() {
		if b.State != "CLOSED" || b.FailureCount != 0 {
			t.Fatalf("invalid request poisoned breaker: %+v", b)
		}
	}
}

func TestRouter3VirtualModelsAndDiagnostics(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "m"}}})
		case "/v1/chat/completions":
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ok"}, "finish_reason": "stop"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	dir := t.TempDir()
	cfg, _ := config.New(filepath.Join(dir, "config.json"))
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	if err := cfg.UpsertProvider(config.Provider{ID: "p", Name: "P", Kind: "custom", BaseURL: upstream.URL + "/v1", ModelsURL: upstream.URL + "/v1/models", AuthType: "none", Enabled: true, Protocols: []string{"chat-completions"}}); err != nil {
		t.Fatal(err)
	}
	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	reg := registry.New(filepath.Join(dir, "models.json"), cfg, pm, am)
	reg.RefreshAll(context.Background())
	reg.UpdateProbe("p/m", providers.ProbeResult{ModelID: "p/m", Basic: true, DoctorStatus: "chat_only"})
	st := stats.New(filepath.Join(dir, "stats.json"))
	srv := New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+cfg.Get().DataAPIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), `"auto/coding"`) || !strings.Contains(string(b), `"auto/smart"`) {
		t.Fatalf("virtual models missing: %s", b)
	}

	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/bridge/router/diagnostics?model=auto/smart", nil)
	req.Header.Set("Authorization", "Bearer "+cfg.Get().AdminAPIKey)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"ordered_candidates"`) || !strings.Contains(string(b), `"scores"`) {
		t.Fatalf("diagnostics status=%d body=%s", resp.StatusCode, b)
	}
}

func TestRouter3SessionAffinityAndRelease(t *testing.T) {
	e := newTestRouterServer(t)
	if e.Router == nil {
		t.Fatal("router engine missing")
	}
	e.Router.Pin("auto/smart", "session", "a", time.Minute)
	// Directly validate that a healthy pinned candidate is promoted, then released.
	models := []providers.Model{{ID: "a", ProviderID: "pa", NativeID: "a"}, {ID: "b", ProviderID: "pb", NativeID: "b"}}
	ordered := e.orderRouterModels("auto/smart", config.RoutingPolicy{Mode: "priority"}, "smart", "session", models)
	if len(ordered) < 2 || ordered[0].ID != "a" {
		t.Fatalf("affinity order=%+v", ordered)
	}
	e.Router.Release("auto/smart", "session", "a")
	if e.Router.Snapshot().AffinityBindings != 0 {
		t.Fatal("affinity release failed")
	}
}

func newTestRouterServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	cfg, _ := config.New(filepath.Join(dir, "config.json"))
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	reg := registry.New(filepath.Join(dir, "models.json"), cfg, pm, am)
	st := stats.New(filepath.Join(dir, "stats.json"))
	return New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0))
}

func TestDoctorAwareRouterEvaluateFiltersByVerifiedCapabilities(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "tool-model"}, map[string]any{"id": "plain-model"}}})
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()
	dir := t.TempDir()
	cfg, _ := config.New(filepath.Join(dir, "config.json"))
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	if err := cfg.UpsertProvider(config.Provider{ID: "p", Name: "P", Kind: "custom", BaseURL: upstream.URL + "/v1", ModelsURL: upstream.URL + "/v1/models", AuthType: "none", Enabled: true, Protocols: []string{"chat-completions"}}); err != nil {
		t.Fatal(err)
	}
	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	reg := registry.New(filepath.Join(dir, "models.json"), cfg, pm, am)
	reg.RefreshAll(context.Background())
	toolProbe := providers.ProbeResult{ModelID: "p/tool-model", Deep: true, Basic: true, Streaming: true, SystemPrompt: true, ToolCalling: true, ToolRoundTrip: true, ParallelTools: true, StructuredOutput: true, IdentityVerified: true, UpstreamModelID: "tool-model", AgentCompatible: true, DoctorStatus: "agent_ready", Protocols: map[string]providers.ProtocolProbe{"chat-completions": {APIType: "chat-completions", Basic: true, Streaming: true, SystemPrompt: true, ToolCalling: true, ToolRoundTrip: true, ParallelTools: true, StructuredOutput: true, IdentityVerified: true, UpstreamModelID: "tool-model", AgentCompatible: true}}}
	plainProbe := providers.ProbeResult{ModelID: "p/plain-model", Deep: true, Basic: true, Streaming: true, SystemPrompt: true, DoctorStatus: "chat_only", Protocols: map[string]providers.ProtocolProbe{"chat-completions": {APIType: "chat-completions", Basic: true, Streaming: true, SystemPrompt: true}}}
	reg.UpdateProbe("p/tool-model", toolProbe)
	reg.UpdateProbe("p/plain-model", plainProbe)
	st := stats.New(filepath.Join(dir, "stats.json"))
	srv := New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body := `{"model":"auto/smart","messages":[{"role":"user","content":"use the tool"}],"tools":[{"type":"function","function":{"name":"read_file","parameters":{"type":"object"}}}],"tool_choice":"required"}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/bridge/router/evaluate?api_type=chat-completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+cfg.Get().AdminAPIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("evaluate status=%d body=%s", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), `p/tool-model`) || strings.Contains(string(raw), `p/plain-model`) {
		t.Fatalf("doctor-aware filtering failed: %s", raw)
	}
	if !strings.Contains(string(raw), `"doctor"`) || !strings.Contains(string(raw), `"requirements"`) {
		t.Fatalf("doctor score/requirements missing: %s", raw)
	}
}

func TestProtocolEngineStatusEndpoint(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.New(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	reg := registry.New(filepath.Join(dir, "models.json"), cfg, pm, am)
	st := stats.New(filepath.Join(dir, "stats.json"))
	ts := httptest.NewServer(New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0)).Handler())
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/bridge/protocol-engine", nil)
	req.Header.Set("Authorization", "Bearer "+cfg.Get().AdminAPIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), `"version":"3.1"`) || !strings.Contains(string(b), `"canonical_stream_contract":true`) {
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
}

func TestCrossProtocolStreamingFlushesBeforeUpstreamCompletes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "claude-live"}}})
		case "/v1/messages":
			w.Header().Set("Content-Type", "text/event-stream")
			fl, _ := w.(http.Flusher)
			_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m-live\",\"model\":\"claude-live\"}}\n\n")
			_, _ = io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"first-token\"}}\n\n")
			if fl != nil {
				fl.Flush()
			}
			time.Sleep(250 * time.Millisecond)
			_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			if fl != nil {
				fl.Flush()
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	dir := t.TempDir()
	cfg, _ := config.New(filepath.Join(dir, "config.json"))
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	_ = sec.Put("provider:a", "secret")
	p := config.NormalizeProvider(config.Provider{ID: "a", Name: "A", Kind: "anthropic", BaseURL: upstream.URL + "/v1", ModelsURL: upstream.URL + "/v1/models", CredentialRef: "provider:a", Enabled: true})
	_ = cfg.UpsertProvider(p)
	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	reg := registry.New(filepath.Join(dir, "models.json"), cfg, pm, am)
	reg.RefreshAll(context.Background())
	st := stats.New(filepath.Join(dir, "stats.json"))
	ts := httptest.NewServer(New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0)).Handler())
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(`{"model":"a/claude-live","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+cfg.Get().DataAPIKey)
	req.Header.Set("Content-Type", "application/json")
	started := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	for {
		line, er := br.ReadString('\n')
		if strings.Contains(line, "first-token") {
			break
		}
		if er != nil {
			t.Fatalf("before first token: %v", er)
		}
	}
	if elapsed := time.Since(started); elapsed >= 200*time.Millisecond {
		t.Fatalf("first translated token was buffered: %v", elapsed)
	}
}

func TestVSCodeExportIncludesVirtualAgentModels(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "tool-model"}}})
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	dir := t.TempDir()
	cfg, err := config.New(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	if err := cfg.UpsertProvider(config.Provider{ID: "p", Name: "Provider", Kind: "custom", BaseURL: upstream.URL + "/v1", ModelsURL: upstream.URL + "/v1/models", AuthType: "none", Enabled: true, Protocols: []string{"chat-completions"}}); err != nil {
		t.Fatal(err)
	}
	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	reg := registry.New(filepath.Join(dir, "models.json"), cfg, pm, am)
	reg.RefreshAll(context.Background())
	reg.UpdateProbe("p/tool-model", providers.ProbeResult{
		ModelID:          "p/tool-model",
		Deep:             true,
		Basic:            true,
		Streaming:        true,
		SystemPrompt:     true,
		ToolCalling:      true,
		ToolRoundTrip:    true,
		ParallelTools:    true,
		StructuredOutput: true,
		Vision:           true,
		Reasoning:        true,
		IdentityVerified: true,
		UpstreamModelID:  "tool-model",
		AgentCompatible:  true,
		DoctorStatus:     "agent_ready",
		Protocols: map[string]providers.ProtocolProbe{
			"chat-completions": {APIType: "chat-completions", Basic: true, Streaming: true, SystemPrompt: true, ToolCalling: true, ToolRoundTrip: true, ParallelTools: true, StructuredOutput: true, Vision: true, Reasoning: true, IdentityVerified: true, UpstreamModelID: "tool-model", AgentCompatible: true},
		},
	})
	st := stats.New(filepath.Join(dir, "stats.json"))
	srv := New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/bridge/vscode/export", nil)
	req.Header.Set("Authorization", "Bearer "+cfg.Get().AdminAPIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("vscode export status=%d body=%s", resp.StatusCode, raw)
	}
	text := string(raw)
	for _, want := range []string{`"version":"3.0-verified"`, `"id":"auto/agent"`, `"id":"auto/coding"`, `"id":"p/tool-model"`, `"toolCalling":true`, `"apiType":"chat-completions"`, `"recommended_auth_header":"Authorization"`, `"recommended_auth_scheme":"Bearer"`, `"explicit_auth_headers":true`, `"bridge_key_fingerprint":"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("vscode export missing %s: %s", want, text)
		}
	}
	var payload struct {
		ModelsTotal       int `json:"models_total"`
		AgentPickerModels int `json:"agent_picker_models"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ModelsTotal < 3 || payload.AgentPickerModels < 1 {
		t.Fatalf("unexpected export counters: %+v", payload)
	}
}

func TestVSCodeExportFiltersYandexNonChatModels(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []any{
					map[string]any{"id": "gpt://b1g-folder/yandexgpt-5.1"},
					map[string]any{"id": "gpt://b1g-folder/deepseek-v4-flash/latest"},
					map[string]any{"id": "gpt://b1g-folder/speech-realtime-deepseek-v4-flash"},
					map[string]any{"id": "gpt://b1g-folder/tts-x"},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	dir := t.TempDir()
	cfg, err := config.New(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	_ = sec.Put("provider:yandex-test", "AQVN-test")
	if err := cfg.UpsertProvider(config.Provider{
		ID: "yandex-test", Name: "Yandex AI Studio", Kind: "yandex", ProjectID: "b1g-folder",
		BaseURL: upstream.URL + "/v1", ModelsURL: upstream.URL + "/v1/models",
		CredentialRef: "provider:yandex-test", Enabled: true,
		PreferredProtocol: "responses", Protocols: []string{"responses", "chat-completions"},
	}); err != nil {
		t.Fatal(err)
	}
	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	reg := registry.New(filepath.Join(dir, "models.json"), cfg, pm, am)
	reg.RefreshAll(context.Background())
	for _, id := range []string{"yandex-test/gpt://b1g-folder/yandexgpt-5.1", "yandex-test/gpt://b1g-folder/deepseek-v4-flash/latest"} {
		reg.UpdateProbe(id, providers.ProbeResult{ModelID: id, Basic: true, DoctorStatus: "chat_only"})
	}
	st := stats.New(filepath.Join(dir, "stats.json"))
	srv := New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/bridge/vscode/export", nil)
	req.Header.Set("Authorization", "Bearer "+cfg.Get().AdminAPIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	text := string(raw)
	if !strings.Contains(text, `"yandex-test/gpt://b1g-folder/yandexgpt-5.1"`) {
		t.Fatalf("yandexgpt-5.1 expected in export: %s", text)
	}
	if !strings.Contains(text, `"yandex-test/gpt://b1g-folder/deepseek-v4-flash/latest"`) {
		t.Fatalf("deepseek expected in export: %s", text)
	}
	var exported struct {
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &exported); err != nil {
		t.Fatalf("decode VS Code export: %v", err)
	}
	for _, model := range exported.Models {
		for _, forbidden := range []string{"speech-realtime-deepseek", "tts-x"} {
			if strings.Contains(model.ID, forbidden) {
				t.Fatalf("non-chat model %s unexpectedly exported: %s", forbidden, text)
			}
		}
	}
}

func TestAuthOKAcceptsGoodBridgeKeyAlongsideStaleBearer(t *testing.T) {
	key := "bridge-secret"
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/v1/models", nil)
	r.Header.Set("Authorization", "Bearer stale-vscode-secret")
	r.Header.Set("x-bridge-key", key)
	if !authOK(r, key) {
		t.Fatal("valid explicit bridge header must win even when VS Code also sends a stale Bearer secret")
	}
}

func TestAuthOKAcceptsCaseInsensitiveBearerAndBridgeHeader(t *testing.T) {
	key := "bridge-secret"
	for _, tc := range []struct {
		name   string
		header string
		value  string
	}{
		{"bearer-upper", "Authorization", "Bearer " + key},
		{"bearer-lower", "Authorization", "bearer " + key},
		{"bridge-header", "x-bridge-key", key},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/v1/models", nil)
			r.Header.Set(tc.header, tc.value)
			if !authOK(r, key) {
				t.Fatal("expected auth to pass")
			}
		})
	}
}

func TestAuthOKRejectsEmptyAndIncorrectKeys(t *testing.T) {
	key := "bridge-secret"
	for _, value := range []string{"", "bridge-secre", "bridge-secret-extra"} {
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/v1/models", nil)
		if value != "" {
			r.Header.Set("Authorization", "Bearer "+value)
		}
		if authOK(r, key) {
			t.Fatalf("unexpected auth success for %q", value)
		}
	}
}

func TestYandexNativeURIRequestResolvesAndProxies(t *testing.T) {
	var gotModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Api-Key AQVN-test" {
			http.Error(w, "bad auth", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("OpenAI-Project") != "b1g-folder" {
			http.Error(w, "bad project", http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []any{map[string]any{"id": "gpt://b1g-folder/yandexgpt/latest"}}})
		case "/v1/responses":
			b, _ := io.ReadAll(r.Body)
			var bodyMap map[string]any
			_ = json.Unmarshal(b, &bodyMap)
			gotModel, _ = bodyMap["model"].(string)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "resp_1", "object": "response", "model": "gpt://b1g-folder/yandexgpt/latest",
				"output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "OK"}}}},
				"usage":  map[string]any{"input_tokens": 2, "output_tokens": 1, "total_tokens": 3},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	dir := t.TempDir()
	cfg, err := config.New(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	sec := secretstore.New(filepath.Join(dir, "secrets.json"))
	_ = sec.Put("provider:yandex-test", "AQVN-test")
	p := config.NormalizeProvider(config.Provider{
		ID: "yandex-test", Name: "Yandex AI Studio", Kind: "yandex", ProjectID: "b1g-folder",
		BaseURL: upstream.URL + "/v1", ModelsURL: upstream.URL + "/v1/models", CredentialRef: "provider:yandex-test", Enabled: true,
	})
	if err := cfg.UpsertProvider(p); err != nil {
		t.Fatal(err)
	}
	pm := providers.NewManager(sec)
	am := accounts.NewManager(dir, sec)
	reg := registry.New(filepath.Join(dir, "models.json"), cfg, pm, am)
	reg.RefreshAll(context.Background())
	st := stats.New(filepath.Join(dir, "stats.json"))
	srv := New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	c := cfg.Get()

	// The exported VS Code entry sends the qualified id.
	req, _ := http.NewRequest("POST", ts.URL+"/v1/responses", strings.NewReader(`{"model":"yandex-test/gpt://b1g-folder/yandexgpt/latest","input":"hi"}`))
	req.Header.Set("Authorization", "Bearer "+c.DataAPIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	rb, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("qualified route status=%d body=%s", resp.StatusCode, rb)
	}
	if gotModel != "gpt://b1g-folder/yandexgpt/latest" {
		t.Fatalf("upstream model=%q", gotModel)
	}

	// Legacy BYOK VS Code entries and direct clients send the raw native URI.
	gotModel = ""
	req, _ = http.NewRequest("POST", ts.URL+"/v1/responses", strings.NewReader(`{"model":"gpt://b1g-folder/yandexgpt/latest","input":"hi"}`))
	req.Header.Set("Authorization", "Bearer "+c.DataAPIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	rb, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("native route status=%d body=%s", resp.StatusCode, rb)
	}
	if gotModel != "gpt://b1g-folder/yandexgpt/latest" {
		t.Fatalf("native upstream model=%q", gotModel)
	}
}
