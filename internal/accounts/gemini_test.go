package accounts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omnibridge/internal/config"
	"omnibridge/internal/secretstore"
)

type rewriteTransport struct {
	base *url.URL
}

func (r rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = r.base.Scheme
	clone.URL.Host = r.base.Host
	return http.DefaultTransport.RoundTrip(clone)
}

func newGeminiTestManager(t *testing.T, serverURL string) (*Manager, config.AccountConnection) {
	t.Helper()
	root := t.TempDir()
	sec := secretstore.New(filepath.Join(root, "data", "secrets.json"))
	m := NewManager(root, sec)
	u, _ := url.Parse(serverURL)
	m.Client = &http.Client{Transport: rewriteTransport{base: u}, Timeout: 5 * time.Second}
	clientPath := filepath.Join(root, "client_secret.json")
	client := map[string]any{"installed": map[string]any{
		"client_id": "client-test", "client_secret": "secret-test", "project_id": "project-test",
		"auth_uri": "https://accounts.google.com/o/oauth2/auth", "token_uri": "https://oauth2.googleapis.com/token",
	}}
	b, _ := json.Marshal(client)
	if err := os.WriteFile(clientPath, b, 0600); err != nil {
		t.Fatal(err)
	}
	a := config.AccountConnection{ID: "gemini-test", Name: "Gemini test", Kind: "gemini-developer-oauth", Enabled: true, Settings: map[string]string{"client_json": clientPath}}
	return m, a
}

func TestGeminiOAuthLoginRefreshModelsAndProbe(t *testing.T) {
	var tokenCalls int
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		tokenCalls++
		_ = r.ParseForm()
		grant := r.Form.Get("grant_type")
		w.Header().Set("Content-Type", "application/json")
		if grant == "authorization_code" {
			io.WriteString(w, `{"access_token":"login-access","refresh_token":"refresh-1","token_type":"Bearer","expires_in":1,"scope":"scope"}`)
			return
		}
		if grant == "refresh_token" {
			io.WriteString(w, `{"access_token":"refreshed-access","expires_in":3600}`)
			return
		}
		http.Error(w, "bad grant", 400)
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer refreshed-access" {
			t.Fatalf("unexpected auth: %q", got)
		}
		if got := r.Header.Get("x-goog-user-project"); got != "project-test" {
			t.Fatalf("unexpected user project: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"models":[{"name":"models/gemini-test-model","displayName":"Gemini Test","inputTokenLimit":100000,"outputTokenLimit":8192,"supportedGenerationMethods":["generateContent"]}]}`)
	})
	mux.HandleFunc("/v1beta/openai/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer refreshed-access" {
			t.Fatalf("unexpected chat auth: %q", got)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "gemini-test-model" {
			t.Fatalf("qualified model was not stripped: %+v", body["model"])
		}
		if stream, _ := body["stream"].(bool); stream {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\ndata: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if tools, ok := body["tools"].([]any); ok && len(tools) > 0 {
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"g1","type":"function","function":{"name":"test_probe","arguments":"{\\"value\\":7}"}}]},"finish_reason":"tool_calls"}]}`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`)
	})

	m, a := newGeminiTestManager(t, server.URL)
	login, err := m.StartGeminiLogin(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	authURL, _ := url.Parse(login.AuthURL)
	redirect := authURL.Query().Get("redirect_uri")
	state := authURL.Query().Get("state")
	if redirect == "" || state == "" || authURL.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("incomplete PKCE auth URL: %s", login.AuthURL)
	}
	redirectURL, _ := url.Parse(redirect)
	if redirectURL.Hostname() != "127.0.0.1" || redirectURL.Path != "" {
		t.Fatalf("Gemini desktop OAuth must use a loopback IP redirect without a callback path: %s", redirect)
	}
	resp, err := http.Get(redirect + "?code=code-test&state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	deadline := time.Now().Add(2 * time.Second)
	for {
		st, ok := m.LoginStatus(login.AttemptID)
		if ok && st.Success && st.Status == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Gemini OAuth callback did not complete: %+v", st)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Force refresh so the refresh-token lifecycle is exercised too.
	raw, err := m.Secrets.Get("account:" + a.ID + ":oauth")
	if err != nil {
		t.Fatal(err)
	}
	var tok googleToken
	_ = json.Unmarshal([]byte(raw), &tok)
	tok.ExpiresAt = time.Now().Add(-time.Minute).Unix()
	b, _ := json.Marshal(tok)
	if err := m.Secrets.Put("account:"+a.ID+":oauth", string(b)); err != nil {
		t.Fatal(err)
	}

	info := m.geminiAccountInfo(context.Background(), a)
	if info.Status != "connected" {
		t.Fatalf("unexpected account info: %+v", info)
	}
	models, err := m.listGeminiModels(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "gemini-test/gemini-test-model" || !models[0].ToolCalling || !models[0].Vision {
		t.Fatalf("bad Gemini models: %+v", models)
	}
	probe := m.Probe(context.Background(), a, models[0], true)
	if !probe.Basic || !probe.Streaming || !probe.ToolCalling {
		t.Fatalf("Gemini deep probe failed: %+v", probe)
	}
	if tokenCalls < 2 {
		t.Fatalf("expected authorization-code + refresh token calls, got %d", tokenCalls)
	}
}

func TestAccountLifecycleClassification(t *testing.T) {
	cases := map[string]string{
		"Google OAuth refresh token missing": "reauth_required",
		"HTTP 429 rate limit":                "rate_limited",
		"Codex CLI not found in PATH":        "configuration_required",
		"connection refused":                 "offline",
		"unexpected failure":                 "error",
	}
	for msg, want := range cases {
		if got := classifyAccountError(assertErr(msg)); got != want {
			t.Fatalf("%q => %q, want %q", msg, got, want)
		}
	}
}

type assertErr string

func (e assertErr) Error() string { return string(e) }

var _ = strings.Builder{}

func TestGeminiPreflightAndOAuthEndpointValidation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	m, a := newGeminiTestManager(t, server.URL)
	pre := m.Preflight(context.Background(), a)
	if !pre.Ready || pre.Backend != "google-desktop-oauth" {
		t.Fatalf("bad Gemini preflight: %+v", pre)
	}
	if pre.Diagnostics["project_id"] != "project-test" {
		t.Fatalf("project id missing: %+v", pre)
	}

	root := t.TempDir()
	badPath := filepath.Join(root, "bad-client.json")
	bad := map[string]any{"installed": map[string]any{
		"client_id": "x", "project_id": "project-x",
		"auth_uri": "https://evil.example/oauth", "token_uri": "https://oauth2.googleapis.com/token",
	}}
	b, _ := json.Marshal(bad)
	if err := os.WriteFile(badPath, b, 0600); err != nil {
		t.Fatal(err)
	}
	badAccount := a
	badAccount.Settings = map[string]string{"client_json": badPath}
	if _, err := m.googleClient(badAccount); err == nil || !strings.Contains(err.Error(), "accounts.google.com") {
		t.Fatalf("non-Google authorization endpoint was accepted: %v", err)
	}
}
