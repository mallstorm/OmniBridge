package server

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"omnibridge/internal/config"
)

func teamTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	cfg, err := config.New(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	return &Server{Config: cfg, Logger: log.New(io.Discard, "", 0)}, cfg.Get().AdminAPIKey
}

func TestDispatchTeamAdminRequiresAdminKey(t *testing.T) {
	s, _ := teamTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "http://localhost/bridge/orchestration", nil)
	rr := httptest.NewRecorder()
	if !s.dispatchTeamAdmin(rr, req) {
		t.Fatal("team route not claimed")
	}
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestDispatchTeamAdminStatusDisabledByDefault(t *testing.T) {
	s, admin := teamTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "http://localhost/bridge/orchestration", nil)
	req.Header.Set("Authorization", "Bearer "+admin)
	rr := httptest.NewRecorder()
	if !s.dispatchTeamAdmin(rr, req) {
		t.Fatal("team route not claimed")
	}
	if rr.Code != http.StatusOK || !bytes.Contains(rr.Body.Bytes(), []byte(`"enabled":false`)) {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestDispatchTeamAdminRejectsWrongMethodAndFallsThroughUnknownPath(t *testing.T) {
	s, admin := teamTestServer(t)
	req := httptest.NewRequest(http.MethodDelete, "http://localhost/bridge/orchestration", nil)
	req.Header.Set("Authorization", "Bearer "+admin)
	rr := httptest.NewRecorder()
	if !s.dispatchTeamAdmin(rr, req) || rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("wrong method was not rejected: status=%d", rr.Code)
	}
	unknown := httptest.NewRequest(http.MethodGet, "http://localhost/bridge/orchestration-extra", nil)
	if s.dispatchTeamAdmin(httptest.NewRecorder(), unknown) {
		t.Fatal("unrelated path was intercepted")
	}
}

func TestRemoteGuardAppliesBeforeTeamDispatcher(t *testing.T) {
	s, admin := teamTestServer(t)
	fallbackCalled := false
	h := s.remoteGuard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { fallbackCalled = true }))

	req := httptest.NewRequest(http.MethodGet, "http://localhost/bridge/orchestration", nil)
	req.RemoteAddr = "203.0.113.9:1234"
	req.Header.Set("Authorization", "Bearer "+admin)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden || fallbackCalled {
		t.Fatalf("remote policy bypassed: status=%d fallback=%v", rr.Code, fallbackCalled)
	}

	req = httptest.NewRequest(http.MethodGet, "http://localhost/bridge/orchestration", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Authorization", "Bearer "+admin)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || fallbackCalled {
		t.Fatalf("loopback team route not handled safely: status=%d fallback=%v body=%s", rr.Code, fallbackCalled, rr.Body.String())
	}
}
