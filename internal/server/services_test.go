package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omnibridge/internal/accounts"
	"omnibridge/internal/config"
	"omnibridge/internal/health"
	"omnibridge/internal/providers"
	"omnibridge/internal/registry"
	"omnibridge/internal/secretstore"
	"omnibridge/internal/stats"
	"omnibridge/internal/storage"
)

func newServicesTestServer(t *testing.T) (*Server, *config.Store, *storage.DB) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "omnibridge.db"))
	if err != nil {
		if strings.Contains(err.Error(), "requires cgo") {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
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
	t.Cleanup(am.Close)
	reg := registry.NewPersistent(filepath.Join(dir, "model-registry.json"), db, cfg, pm, am)
	st := stats.NewPersistent(filepath.Join(dir, "usage-stats.json"), db)
	hs := health.NewPersistent(filepath.Join(dir, "connection-health.json"), db)
	srv := New(cfg, sec, pm, am, reg, st, log.New(io.Discard, "", 0), hs)
	srv.SetRoot(dir)
	srv.SetStorage(db)
	return srv, cfg, db
}

func adminRequest(t *testing.T, c *http.Client, base, key, method, path string, body any) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, base+path, r)
	req.Header.Set("Authorization", "Bearer "+key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestStage10To14ServiceEndpoints(t *testing.T) {
	srv, cfg, _ := newServicesTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	key := cfg.Get().AdminAPIKey
	for _, path := range []string{"/bridge/quota-cost", "/bridge/context-guard", "/bridge/integrations", "/bridge/mcp", "/bridge/observability", "/bridge/remote", "/bridge/traces"} {
		resp := adminRequest(t, http.DefaultClient, ts.URL, key, http.MethodGet, path, nil)
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status=%d body=%s", path, resp.StatusCode, b)
		}
	}

	resp := adminRequest(t, http.DefaultClient, ts.URL, key, http.MethodPost, "/bridge/pricing", map[string]any{
		"provider": "mock", "model": "m", "input_usd_per_million": 1.25, "output_usd_per_million": 2.5, "confidence": "operator",
	})
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("pricing status=%d body=%s", resp.StatusCode, b)
	}
	resp.Body.Close()
	resp = adminRequest(t, http.DefaultClient, ts.URL, key, http.MethodPost, "/bridge/quota", map[string]any{
		"connection_id": "api:mock", "remaining_known": true, "remaining_percent": 8, "confidence": "operator",
	})
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("quota status=%d body=%s", resp.StatusCode, b)
	}
	resp.Body.Close()
	resp = adminRequest(t, http.DefaultClient, ts.URL, key, http.MethodGet, "/bridge/quota-cost", nil)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Contains(b, []byte(`"provider":"mock"`)) || !bytes.Contains(b, []byte(`"low_quota":true`)) {
		t.Fatalf("quota-cost summary missing persisted hints: %s", b)
	}

	resp = adminRequest(t, http.DefaultClient, ts.URL, key, http.MethodPost, "/bridge/context-guard/preview", map[string]any{
		"api_type": "chat-completions", "context_window": 64, "request": map[string]any{"model": "m", "messages": []any{map[string]any{"role": "user", "content": strings.Repeat("long context ", 80)}}},
	})
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !bytes.Contains(b, []byte(`"report"`)) {
		t.Fatalf("context preview status=%d body=%s", resp.StatusCode, b)
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/metrics", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("metrics without admin status=%d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = adminRequest(t, http.DefaultClient, ts.URL, key, http.MethodGet, "/metrics", nil)
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !bytes.Contains(b, []byte("omnibridge_requests_total")) {
		t.Fatalf("metrics auth status=%d body=%s", resp.StatusCode, b)
	}
}

func TestRemoteModeFailsClosed(t *testing.T) {
	srv, cfg, _ := newServicesTestServer(t)
	c := cfg.Get()
	c.Listen = "0.0.0.0:4142"
	if err := srv.validateRemoteBind(c); err == nil {
		t.Fatal("non-loopback listen must fail while remote mode is disabled")
	}
	c.Remote.Enabled = true
	c.Remote.AllowedCIDRs = []string{"10.0.0.0/8"}
	if err := srv.validateRemoteBind(c); err == nil {
		t.Fatal("remote mode without TLS must fail")
	}
	cert := filepath.Join(t.TempDir(), "cert.pem")
	key := filepath.Join(filepath.Dir(cert), "key.pem")
	if err := os.WriteFile(cert, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	c.Remote.TLSCertPath = cert
	c.Remote.TLSKeyPath = key
	if err := srv.validateRemoteBind(c); err != nil {
		t.Fatalf("valid fail-closed remote config rejected: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://omnibridge/health", nil)
	req.RemoteAddr = "203.0.113.10:1234"
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("unallowed remote client status=%d body=%s", rr.Code, rr.Body.String())
	}
}
