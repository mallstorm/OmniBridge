package accounts

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"omnibridge/internal/config"
	"omnibridge/internal/providers"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type googleClientFile struct {
	Installed struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		ProjectID    string `json:"project_id"`
		AuthURI      string `json:"auth_uri"`
		TokenURI     string `json:"token_uri"`
	} `json:"installed"`
}
type googleToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresAt    int64  `json:"expires_at"`
	Scope        string `json:"scope"`
}

func randB64(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
func effectiveGoogleAuthURI(c googleClientFile) string {
	if strings.TrimSpace(c.Installed.AuthURI) != "" {
		return strings.TrimSpace(c.Installed.AuthURI)
	}
	return "https://accounts.google.com/o/oauth2/auth"
}

func effectiveGoogleTokenURI(c googleClientFile) string {
	if strings.TrimSpace(c.Installed.TokenURI) != "" {
		return strings.TrimSpace(c.Installed.TokenURI)
	}
	return "https://oauth2.googleapis.com/token"
}

func validateGoogleOAuthEndpoint(raw, purpose string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return fmt.Errorf("invalid Google OAuth %s endpoint", purpose)
	}
	host := strings.ToLower(u.Hostname())
	switch purpose {
	case "authorization":
		if host != "accounts.google.com" {
			return fmt.Errorf("Google OAuth authorization endpoint must use accounts.google.com")
		}
	case "token":
		if host != "oauth2.googleapis.com" {
			return fmt.Errorf("Google OAuth token endpoint must use oauth2.googleapis.com")
		}
	}
	return nil
}

func (m *Manager) googleClient(a config.AccountConnection) (googleClientFile, error) {
	var c googleClientFile
	p := strings.TrimSpace(a.Settings["client_json"])
	if p == "" {
		return c, errors.New("Google Desktop OAuth client JSON is required")
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return c, fmt.Errorf("read Google Desktop OAuth JSON: %w", e)
	}
	if e = json.Unmarshal(b, &c); e != nil {
		return c, fmt.Errorf("parse Google Desktop OAuth JSON: %w", e)
	}
	if strings.TrimSpace(c.Installed.ClientID) == "" {
		return c, errors.New("invalid Google Desktop OAuth JSON: installed.client_id is missing")
	}
	if strings.TrimSpace(c.Installed.ProjectID) == "" {
		return c, errors.New("invalid Google Desktop OAuth JSON: installed.project_id is missing")
	}
	if err := validateGoogleOAuthEndpoint(effectiveGoogleAuthURI(c), "authorization"); err != nil {
		return c, err
	}
	if err := validateGoogleOAuthEndpoint(effectiveGoogleTokenURI(c), "token"); err != nil {
		return c, err
	}
	return c, nil
}
func (m *Manager) StartGeminiLogin(ctx context.Context, a config.AccountConnection) (LoginStart, error) {
	c, e := m.googleClient(a)
	if e != nil {
		return LoginStart{}, e
	}
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return LoginStart{}, e
	}
	state := randB64(24)
	ver := randB64(48)
	sum := sha256.Sum256([]byte(ver))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	redirect := "http://127.0.0.1:" + fmt.Sprint(ln.Addr().(*net.TCPAddr).Port)
	auth := effectiveGoogleAuthURI(c)
	u, _ := url.Parse(auth)
	q := u.Query()
	q.Set("client_id", c.Installed.ClientID)
	q.Set("redirect_uri", redirect)
	q.Set("response_type", "code")
	q.Set("scope", "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/generative-language.retriever")
	q.Set("access_type", "offline")
	q.Set("prompt", "consent")
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()
	attempt := "google-" + randB64(12)
	m.mu.Lock()
	m.pending[attempt] = &pendingLogin{accountID: a.ID, status: LoginStatus{AccountID: a.ID, AttemptID: attempt, Status: "pending", Backend: "google-desktop-oauth"}}
	m.mu.Unlock()
	go m.finishGeminiLogin(a, c, ln, redirect, state, ver, attempt)
	return LoginStart{AccountID: a.ID, AttemptID: attempt, AuthURL: u.String(), Status: "pending", Backend: "google-desktop-oauth"}, nil
}
func (m *Manager) finishGeminiLogin(a config.AccountConnection, c googleClientFile, ln net.Listener, redirect, state, ver, attempt string) {
	defer ln.Close()
	srv := &http.Server{}
	done := make(chan struct{})
	var doneOnce sync.Once
	finish := func() { doneOnce.Do(func() { close(done) }) }
	srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("state") != state {
			http.Error(w, "Invalid state", 400)
			return
		}
		if oauthErr := strings.TrimSpace(r.URL.Query().Get("error")); oauthErr != "" {
			detail := strings.TrimSpace(r.URL.Query().Get("error_description"))
			if detail == "" {
				detail = oauthErr
			}
			e := fmt.Errorf("Google OAuth: %s", detail)
			m.setLoginError(attempt, e)
			http.Error(w, e.Error(), http.StatusBadRequest)
			finish()
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			e := errors.New("Google OAuth callback did not contain an authorization code")
			m.setLoginError(attempt, e)
			http.Error(w, e.Error(), http.StatusBadRequest)
			finish()
			return
		}
		form := url.Values{"code": {code}, "client_id": {c.Installed.ClientID}, "client_secret": {c.Installed.ClientSecret}, "redirect_uri": {redirect}, "grant_type": {"authorization_code"}, "code_verifier": {ver}}
		tokenCtx, tokenCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer tokenCancel()
		req, reqErr := http.NewRequestWithContext(tokenCtx, http.MethodPost, effectiveGoogleTokenURI(c), strings.NewReader(form.Encode()))
		if reqErr != nil {
			m.setLoginError(attempt, reqErr)
			http.Error(w, reqErr.Error(), http.StatusInternalServerError)
			finish()
			return
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, e := m.Client.Do(req)
		if e != nil {
			m.setLoginError(attempt, e)
			http.Error(w, e.Error(), 500)
			finish()
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if resp.StatusCode/100 != 2 {
			e = fmt.Errorf("Google token HTTP %d: %s", resp.StatusCode, string(b))
			m.setLoginError(attempt, e)
			http.Error(w, e.Error(), 500)
			finish()
			return
		}
		var x struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			TokenType    string `json:"token_type"`
			ExpiresIn    int64  `json:"expires_in"`
			Scope        string `json:"scope"`
		}
		if err := json.Unmarshal(b, &x); err != nil {
			e = fmt.Errorf("invalid Google token response: %w", err)
			m.setLoginError(attempt, e)
			http.Error(w, e.Error(), http.StatusBadGateway)
			finish()
			return
		}
		if strings.TrimSpace(x.AccessToken) == "" {
			e = errors.New("Google OAuth token response did not contain an access token")
			m.setLoginError(attempt, e)
			http.Error(w, e.Error(), http.StatusBadGateway)
			finish()
			return
		}
		if strings.TrimSpace(x.RefreshToken) == "" {
			e = errors.New("Google OAuth token response did not contain a refresh token; revoke the app grant and sign in again")
			m.setLoginError(attempt, e)
			http.Error(w, e.Error(), http.StatusBadGateway)
			finish()
			return
		}
		tok := googleToken{AccessToken: x.AccessToken, RefreshToken: x.RefreshToken, TokenType: x.TokenType, ExpiresAt: time.Now().Unix() + x.ExpiresIn - 60, Scope: x.Scope}
		tb, _ := json.Marshal(tok)
		_ = m.Secrets.Put("account:"+a.ID+":oauth", string(tb))
		m.mu.Lock()
		if p := m.pending[attempt]; p != nil {
			p.status = LoginStatus{AccountID: a.ID, AttemptID: attempt, Status: "completed", Success: true, Backend: "google-desktop-oauth"}
		}
		m.mu.Unlock()
		fmt.Fprintln(w, "OmniBridge: Google OAuth completed. You can close this tab and return to OmniBridge.")
		finish()
	})
	go srv.Serve(ln)
	select {
	case <-done:
	case <-time.After(5 * time.Minute):
		m.setLoginError(attempt, errors.New("Google login timed out"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
func (m *Manager) setLoginError(id string, e error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p := m.pending[id]; p != nil {
		backend := p.status.Backend
		p.status = LoginStatus{AccountID: p.accountID, AttemptID: id, Status: "failed", Error: e.Error(), Backend: backend}
	}
}
func (m *Manager) geminiToken(ctx context.Context, a config.AccountConnection) (googleToken, googleClientFile, error) {
	var t googleToken
	c, e := m.googleClient(a)
	if e != nil {
		return t, c, e
	}
	raw, e := m.Secrets.Get("account:" + a.ID + ":oauth")
	if e != nil {
		return t, c, errors.New("Google account is not connected")
	}
	if json.Unmarshal([]byte(raw), &t) != nil {
		return t, c, errors.New("invalid Google OAuth token")
	}
	if t.ExpiresAt > time.Now().Unix()+30 {
		return t, c, nil
	}
	if t.RefreshToken == "" {
		return t, c, errors.New("Google OAuth refresh token missing")
	}
	form := url.Values{"client_id": {c.Installed.ClientID}, "client_secret": {c.Installed.ClientSecret}, "refresh_token": {t.RefreshToken}, "grant_type": {"refresh_token"}}
	refreshCtx, refreshCancel := context.WithTimeout(ctx, 30*time.Second)
	defer refreshCancel()
	req, _ := http.NewRequestWithContext(refreshCtx, "POST", effectiveGoogleTokenURI(c), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, e := m.Client.Do(req)
	if e != nil {
		return t, c, e
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return t, c, fmt.Errorf("Google refresh HTTP %d: %s", resp.StatusCode, string(b))
	}
	var x struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(b, &x); err != nil {
		return t, c, fmt.Errorf("invalid Google refresh response: %w", err)
	}
	if strings.TrimSpace(x.AccessToken) == "" {
		return t, c, errors.New("Google OAuth refresh response did not contain an access token")
	}
	t.AccessToken = x.AccessToken
	t.ExpiresAt = time.Now().Unix() + x.ExpiresIn - 60
	tb, _ := json.Marshal(t)
	_ = m.Secrets.Put("account:"+a.ID+":oauth", string(tb))
	return t, c, nil
}
func (m *Manager) listGeminiModels(ctx context.Context, a config.AccountConnection) ([]providers.Model, error) {
	t, c, e := m.geminiToken(ctx, a)
	if e != nil {
		return nil, e
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://generativelanguage.googleapis.com/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+t.AccessToken)
	if c.Installed.ProjectID != "" {
		req.Header.Set("x-goog-user-project", c.Installed.ProjectID)
	}
	resp, e := m.Client.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("Gemini models HTTP %d: %s", resp.StatusCode, string(b))
	}
	var v struct {
		Models []struct {
			Name                       string   `json:"name"`
			DisplayName                string   `json:"displayName"`
			InputTokenLimit            int      `json:"inputTokenLimit"`
			OutputTokenLimit           int      `json:"outputTokenLimit"`
			SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	_ = json.Unmarshal(b, &v)
	out := []providers.Model{}
	for _, x := range v.Models {
		native := strings.TrimPrefix(x.Name, "models/")
		gen := false
		for _, g := range x.SupportedGenerationMethods {
			if strings.Contains(strings.ToLower(g), "generate") {
				gen = true
			}
		}
		if !gen {
			continue
		}
		out = append(out, providers.Model{ID: a.ID + "/" + native, NativeID: native, ProviderID: a.ID, ProviderName: a.Name, Name: firstNonEmpty(x.DisplayName, native), APIType: "chat-completions", Protocols: []string{"chat-completions"}, PreferredProtocol: "chat-completions", Type: "chat", ToolCalling: true, Streaming: true, Vision: true, ContextWindow: x.InputTokenLimit, MaxOutputTokens: x.OutputTokenLimit, Status: "discovered", Source: "account"})
	}
	return out, nil
}
func (m *Manager) proxyGeminiChat(ctx context.Context, a config.AccountConnection, body []byte) (*UpstreamResponse, error) {
	t, c, e := m.geminiToken(ctx, a)
	if e != nil {
		return nil, e
	}
	var v map[string]any
	if e = json.Unmarshal(body, &v); e != nil {
		return nil, e
	}
	if s, ok := v["model"].(string); ok {
		_, n := nativeFromQualified(s)
		v["model"] = n
	}
	b, _ := json.Marshal(v)
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+t.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	if c.Installed.ProjectID != "" {
		req.Header.Set("x-goog-user-project", c.Installed.ProjectID)
	}
	resp, e := clientForStreamingBody(m.Client, b).Do(req)
	if e != nil {
		return nil, e
	}
	return &UpstreamResponse{StatusCode: resp.StatusCode, Header: resp.Header, Body: resp.Body}, nil
}
func (m *Manager) geminiAccountInfo(ctx context.Context, a config.AccountConnection) AccountInfo {
	_, _, e := m.geminiToken(ctx, a)
	if e != nil {
		return AccountInfo{ID: a.ID, Kind: a.Kind, Status: classifyAccountError(e), Error: e.Error()}
	}
	return AccountInfo{ID: a.ID, Kind: a.Kind, Status: "connected", Details: map[string]any{"auth": "Google OAuth", "quota": "Gemini Developer API / Google Cloud project"}}
}

var _ = filepath.Separator
