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
	if rr.Code != http.StatusOK || !bytes.Contains(rr.Body.Bytes(), []byte(`"enabled":false`)) || !bytes.Contains(rr.Body.Bytes(), []byte(`"team/smart"`)) {
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

func TestNonTeamDataRequestFallsThroughWithBodyPreserved(t *testing.T) {
	s, _ := teamTestServer(t)
	body := []byte(`{"model":"auto/smart","messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "http://localhost/v1/chat/completions", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	if s.dispatchTeamAdmin(rr, req) {
		t.Fatal("ordinary RC8 data request was intercepted by Team Routing")
	}
	restored, err := io.ReadAll(req.Body)
	if err != nil || !bytes.Equal(restored, body) {
		t.Fatalf("request body was touched without Team execution header: err=%v got=%s", err, restored)
	}
}

func TestTeamModelWithoutExecutionHeaderFallsThroughUntouched(t *testing.T) {
	s, _ := teamTestServer(t)
	body := []byte(`{"model":"team/smart","stream":false,"messages":[{"role":"user","content":"summarize this"}]}`)
	req := httptest.NewRequest(http.MethodPost, "http://localhost/v1/chat/completions", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	if s.dispatchTeamAdmin(rr, req) {
		t.Fatal("team model was executed without explicit per-request header")
	}
	restored, _ := io.ReadAll(req.Body)
	if !bytes.Equal(restored, body) {
		t.Fatalf("team body changed without opt-in: %s", restored)
	}
}

func TestTeamDataRequiresDataKeyAndExecutionOptIn(t *testing.T) {
	s, _ := teamTestServer(t)
	body := []byte(`{"model":"team/smart","stream":false,"messages":[{"role":"user","content":"summarize this"}]}`)
	req := httptest.NewRequest(http.MethodPost, "http://localhost/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("X-OmniBridge-Team-Execute", "true")
	rr := httptest.NewRecorder()
	if !s.dispatchTeamAdmin(rr, req) || rr.Code != http.StatusUnauthorized {
		t.Fatalf("team data auth was not enforced before body dispatch: status=%d body=%s", rr.Code, rr.Body.String())
	}

	dataKey := s.Config.Get().DataAPIKey
	req = httptest.NewRequest(http.MethodPost, "http://localhost/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("X-OmniBridge-Team-Execute", "true")
	req.Header.Set("Authorization", "Bearer "+dataKey)
	rr = httptest.NewRecorder()
	if !s.dispatchTeamAdmin(rr, req) || rr.Code != http.StatusConflict || !bytes.Contains(rr.Body.Bytes(), []byte("team_execution_disabled")) {
		t.Fatalf("execution did not fail closed by default: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestTeamReviewParsing(t *testing.T) {
	approve := []byte(`{"choices":[{"message":{"content":"{\"verdict\":\"approve\",\"summary\":\"looks good\"}"}}]}`)
	got := parseTeamReview(approve)
	if got.Verdict != "approve" || got.Summary != "looks good" {
		t.Fatalf("review parse failed: %+v", got)
	}
	concern := []byte(`{"choices":[{"message":{"content":"I have concerns about correctness"}}]}`)
	if got := parseTeamReview(concern); got.Verdict != "concerns" {
		t.Fatalf("concern fallback parse failed: %+v", got)
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

	teamBody := []byte(`{"model":"team/smart","stream":false,"messages":[{"role":"user","content":"hello"}]}`)
	req = httptest.NewRequest(http.MethodPost, "http://localhost/v1/chat/completions", bytes.NewReader(teamBody))
	req.RemoteAddr = "203.0.113.9:1234"
	req.Header.Set("X-OmniBridge-Team-Execute", "true")
	req.Header.Set("Authorization", "Bearer "+s.Config.Get().DataAPIKey)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden || fallbackCalled {
		t.Fatalf("remote policy bypassed for team data: status=%d fallback=%v", rr.Code, fallbackCalled)
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
