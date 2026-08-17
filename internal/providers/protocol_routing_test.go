package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"omnibridge/internal/config"
	"omnibridge/internal/secretstore"
)

func TestProtocolSpecificBaseAndAuth(t *testing.T) {
	var gotPath, gotKey, gotModel string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-api-key")
		b, _ := io.ReadAll(r.Body)
		var v map[string]any
		_ = json.Unmarshal(b, &v)
		gotModel, _ = v["model"].(string)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "msg", "type": "message", "content": []any{}})
	}))
	defer ts.Close()

	secrets := secretstore.New(t.TempDir() + "/secrets.json")
	if err := secrets.Put("provider:p", "secret"); err != nil {
		t.Fatal(err)
	}
	m := NewManager(secrets)
	p := config.Provider{
		ID: "p", Name: "P", Kind: "custom", BaseURL: "https://unused.invalid/v1", AuthType: "bearer", CredentialRef: "provider:p", Enabled: true,
		Protocols: []string{"messages", "chat-completions"}, PreferredProtocol: "messages",
		ProtocolBaseURLs:  map[string]string{"messages": ts.URL + "/anthropic/v1"},
		ProtocolAuthTypes: map[string]string{"messages": "x-api-key"},
	}
	body := []byte(`{"model":"p/native-model","messages":[{"role":"user","content":"hi"}],"max_tokens":8}`)
	resp, err := m.Proxy(context.Background(), p, "messages", body)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if gotPath != "/anthropic/v1/messages" || gotKey != "secret" || gotModel != "native-model" {
		t.Fatalf("path=%q key=%q model=%q", gotPath, gotKey, gotModel)
	}
}
