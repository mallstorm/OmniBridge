package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"omnibridge/internal/config"
	"omnibridge/internal/secretstore"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGigaToolTranslationRoundTrip(t *testing.T) {
	in := map[string]any{
		"model":    "giga/GigaChat-3-Ultra",
		"messages": []any{map[string]any{"role": "user", "content": "read x"}},
		"tools": []any{map[string]any{"type": "function", "function": map[string]any{
			"name": "read_file", "description": "read", "parameters": map[string]any{
				"type": "object", "properties": map[string]any{"newCode": map[string]any{"anyOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}}},
			},
		}}},
		"tool_choice": "auto",
	}
	b, _ := json.Marshal(in)
	out, reverse, err := translateOpenAIToGiga(b)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["model"] != "GigaChat-3-Ultra" {
		t.Fatalf("model=%v", got["model"])
	}
	funcs := got["functions"].([]any)
	fn := funcs[0].(map[string]any)
	alias := fn["name"].(string)
	if strings.ContainsAny(alias, "_0123456789-") {
		t.Fatalf("unsafe alias %q", alias)
	}
	if reverse[alias] != "read_file" {
		t.Fatalf("reverse alias failed")
	}
	params := fn["parameters"].(map[string]any)
	props := params["properties"].(map[string]any)
	nc := props["newCode"].(map[string]any)
	if _, ok := nc["anyOf"]; ok {
		t.Fatal("anyOf not sanitized")
	}
	if nc["type"] != "string" {
		t.Fatalf("sanitized type=%v", nc["type"])
	}

	state := "123e4567-e89b-12d3-a456-426614174000"
	upstream := map[string]any{"choices": []any{map[string]any{"finish_reason": "function_call", "message": map[string]any{"role": "assistant", "content": "", "functions_state_id": state, "function_call": map[string]any{"name": alias, "arguments": map[string]any{"path": "x"}}}}}}
	ub, _ := json.Marshal(upstream)
	rb, err := translateGigaJSON(ub, reverse)
	if err != nil {
		t.Fatal(err)
	}
	var rr map[string]any
	_ = json.Unmarshal(rb, &rr)
	choice := rr["choices"].([]any)[0].(map[string]any)
	if choice["finish_reason"] != "tool_calls" {
		t.Fatalf("finish=%v", choice["finish_reason"])
	}
	msg := choice["message"].(map[string]any)
	tc := msg["tool_calls"].([]any)[0].(map[string]any)
	f := tc["function"].(map[string]any)
	if f["name"] != "read_file" {
		t.Fatalf("name=%v", f["name"])
	}
	id := tc["id"].(string)
	st, al, ok := decodeGigaToolID(id)
	if !ok || st != state || al != alias {
		t.Fatalf("id round trip failed %q %q %v", st, al, ok)
	}
}

func TestMergeDeveloperAndSystem(t *testing.T) {
	b := []byte(`{"model":"giga/GigaChat","messages":[{"role":"system","content":"A"},{"role":"developer","content":"B"},{"role":"user","content":"C"}]}`)
	out, _, err := translateOpenAIToGiga(b)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	_ = json.Unmarshal(out, &v)
	msgs := v["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages=%d", len(msgs))
	}
	first := msgs[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "A\n\nB" {
		t.Fatalf("first=%v", first)
	}
}

func TestGigaModelsRetriesUnauthorizedOnlyOnce(t *testing.T) {
	var oauthCalls, modelCalls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth":
			oauthCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token", "expires_at": 4102444800000})
		case "/v1/models":
			modelCalls.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()
	dir := t.TempDir()
	secrets := secretstore.New(filepath.Join(dir, "secrets.json"))
	if err := secrets.Put("provider:giga", "basic-key"); err != nil {
		t.Fatal(err)
	}
	m := NewManager(secrets)
	p := config.Provider{ID: "giga", Name: "GigaChat", Kind: "gigachat", BaseURL: up.URL + "/v1", ModelsURL: up.URL + "/v1/models", CredentialRef: "provider:giga", Enabled: true, Quirks: map[string]string{"gigachat_oauth_url": up.URL + "/oauth"}}
	if _, err := m.listGigaModels(context.Background(), p); err == nil || !strings.Contains(err.Error(), "after token refresh") {
		t.Fatalf("expected bounded 401 error, got %v", err)
	}
	if oauthCalls.Load() != 2 || modelCalls.Load() != 2 {
		t.Fatalf("unbounded or missing retry: oauth=%d models=%d", oauthCalls.Load(), modelCalls.Load())
	}
}

func TestGigaProxyRetriesUnauthorizedOnlyOnce(t *testing.T) {
	var oauthCalls, chatCalls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth":
			oauthCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token", "expires_at": 4102444800000})
		case "/v1/chat/completions":
			chatCalls.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()
	dir := t.TempDir()
	secrets := secretstore.New(filepath.Join(dir, "secrets.json"))
	if err := secrets.Put("provider:giga", "basic-key"); err != nil {
		t.Fatal(err)
	}
	m := NewManager(secrets)
	p := config.Provider{ID: "giga", Name: "GigaChat", Kind: "gigachat", BaseURL: up.URL + "/v1", CredentialRef: "provider:giga", Enabled: true, Quirks: map[string]string{"gigachat_oauth_url": up.URL + "/oauth"}}
	body := []byte(`{"model":"giga/GigaChat","messages":[{"role":"user","content":"hi"}]}`)
	if _, err := m.proxyGigaChat(context.Background(), p, body); err == nil || !strings.Contains(err.Error(), "after token refresh") {
		t.Fatalf("expected bounded 401 error, got %v", err)
	}
	if oauthCalls.Load() != 2 || chatCalls.Load() != 2 {
		t.Fatalf("unbounded or missing retry: oauth=%d chat=%d", oauthCalls.Load(), chatCalls.Load())
	}
}

func TestGigaTokenRefreshSingleflight(t *testing.T) {
	var oauthCalls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		oauthCalls.Add(1)
		time.Sleep(30 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "shared-token", "expires_at": 4102444800000})
	}))
	defer up.Close()
	dir := t.TempDir()
	secrets := secretstore.New(filepath.Join(dir, "secrets.json"))
	if err := secrets.Put("provider:giga", "basic-key"); err != nil {
		t.Fatal(err)
	}
	m := NewManager(secrets)
	p := config.Provider{ID: "giga", Kind: "gigachat", CredentialRef: "provider:giga", Scope: "GIGACHAT_API_PERS", Quirks: map[string]string{"gigachat_oauth_url": up.URL}}
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := m.gigaAccessToken(context.Background(), p)
			if err == nil && token != "shared-token" {
				err = fmt.Errorf("token=%q", token)
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if oauthCalls.Load() != 1 {
		t.Fatalf("refresh stampede: calls=%d", oauthCalls.Load())
	}
}
