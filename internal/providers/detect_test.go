package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"omnibridge/internal/secretstore"
	"path/filepath"
	"testing"
	"time"
)

func TestDetectOpenAICompatible(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer abc" {
			http.Error(w, "no", 401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []any{map[string]any{"id": "m"}}})
	}))
	defer ts.Close()
	m := NewManager(secretstore.New(t.TempDir() + "/s.json"))
	got, err := m.DetectOpenAICompatible(context.Background(), ts.URL, "abc")
	if err != nil {
		t.Fatal(err)
	}
	if got.BaseURL != ts.URL+"/v1" || got.AuthType != "bearer" || got.Shape != "openai-model-list" {
		t.Fatalf("got %+v", got)
	}
}

func TestDetectOpenAICompatibleProtocolMatrix(t *testing.T) {
	var sawAuthOnUnexpectedHost bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "" && r.Header.Get("Authorization") != "" {
			sawAuthOnUnexpectedHost = true
		}
		if r.Header.Get("Authorization") != "Bearer abc" {
			http.Error(w, "bad auth", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []any{map[string]any{"id": "agent-model"}}})
		case "/v1/responses":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "r", "status": "completed", "output": []any{}})
		case "/v1/chat/completions":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "c", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "OK"}}}})
		case "/v1/messages":
			http.Error(w, "messages unsupported", http.StatusNotFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	m := NewManager(secretstore.New(t.TempDir() + "/s.json"))
	got, err := m.DetectOpenAICompatible(context.Background(), ts.URL, "abc")
	if err != nil {
		t.Fatal(err)
	}
	if got.PreferredProtocol != "responses" {
		t.Fatalf("preferred = %q, want responses; result=%+v", got.PreferredProtocol, got)
	}
	if len(got.Protocols) != 2 || got.Protocols[0] != "responses" || got.Protocols[1] != "chat-completions" {
		t.Fatalf("protocols = %#v", got.Protocols)
	}
	if sawAuthOnUnexpectedHost {
		t.Fatal("credential escaped same-origin detection")
	}
}

func TestDetectKeylessLocalReturnsNoneAuth(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"models": []any{map[string]any{"id": "local-model"}}})
		case "/v1/chat/completions":
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "OK"}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()
	m := NewManager(secretstore.New(filepath.Join(t.TempDir(), "secrets.json")))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d, err := m.DetectOpenAICompatible(ctx, up.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if d.AuthType != "none" {
		t.Fatalf("auth=%q", d.AuthType)
	}
}
