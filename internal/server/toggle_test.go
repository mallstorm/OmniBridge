package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"omnibridge/internal/config"
)

func TestProviderEnableDisableEndpoint(t *testing.T) {
	s := newTestRouterServer(t)
	dir := t.TempDir()
	cfg, _ := config.New(filepath.Join(dir, "config.json"))
	if err := cfg.UpsertProvider(config.Provider{ID: "p1", Name: "P1", Kind: "custom", BaseURL: "http://127.0.0.1:1/v1", AuthType: "none", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	s.Config = cfg
	t.Cleanup(s.refreshes.Wait)
	adminKey := s.Config.Get().AdminAPIKey
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /bridge/providers/{id}/enabled", s.withAdmin(s.setProviderEnabled))

	call := func(body string, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/bridge/providers/p1/enabled", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	if rec := call(`{"enabled":false}`, "wrong"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated toggle: got %d", rec.Code)
	}
	if rec := call(`{"enabled":false}`, adminKey); rec.Code != http.StatusOK {
		t.Fatalf("disable: got %d body=%s", rec.Code, rec.Body.String())
	}
	for _, p := range s.Config.Get().Providers {
		if p.ID == "p1" && p.Enabled {
			t.Fatal("provider still enabled after disable")
		}
	}
	var out map[string]any
	rec := call(`{"enabled":true}`, adminKey)
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out["enabled"] != true {
		t.Fatalf("enable response: %v %s", err, rec.Body.String())
	}
}
