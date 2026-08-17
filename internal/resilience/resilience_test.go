package resilience

import (
	"path/filepath"
	"testing"
	"time"

	"omnibridge/internal/storage"
)

func TestLifecycleDegradedOpenHalfOpenRecovery(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "omnibridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	m := New(Config{FailureThreshold: 3, DegradationThreshold: 2, BaseCooldown: time.Second, MaxCooldown: 10 * time.Second, HalfOpenProbes: 1}, db)
	m.now = func() time.Time { return now }
	key := ConnectionKey("api", "p1")
	m.RecordFailure(key, FailureTimeout, "one", 0)
	if s, _ := m.Snapshot(key); s.State != Closed {
		t.Fatalf("state=%s", s.State)
	}
	m.RecordFailure(key, FailureTimeout, "two", 0)
	if s, _ := m.Snapshot(key); s.State != Degraded {
		t.Fatalf("state=%s", s.State)
	}
	m.RecordFailure(key, FailureTimeout, "three", 0)
	s, _ := m.Snapshot(key)
	if s.State != Open || s.OpenUntil.IsZero() {
		t.Fatalf("open=%+v", s)
	}
	if d := m.Blocked(key); d.Allowed {
		t.Fatal("open breaker allowed")
	}
	now = now.Add(2 * time.Second)
	if d := m.Blocked(key); !d.Allowed || d.State != HalfOpen {
		t.Fatalf("half open decision=%+v", d)
	}
	if d := m.Acquire(key); !d.Allowed || !d.Probe {
		t.Fatalf("probe=%+v", d)
	}
	if d := m.Acquire(key); d.Allowed {
		t.Fatalf("second probe should be blocked: %+v", d)
	}
	m.RecordSuccess(key)
	if s, _ := m.Snapshot(key); s.State != Closed || s.FailureCount != 0 {
		t.Fatalf("recovery=%+v", s)
	}
}

func TestImmediateOpenAndIgnoredFailures(t *testing.T) {
	m := New(Config{FailureThreshold: 5, BaseCooldown: time.Second}, nil)
	m.RecordFailure("x", FailureClientCancelled, "cancel", 0)
	if _, ok := m.Snapshot("x"); ok {
		t.Fatal("client cancellation must not create breaker state")
	}
	m.RecordFailure("x", FailureInvalidRequest, "400", 0)
	if _, ok := m.Snapshot("x"); ok {
		t.Fatal("invalid request must not poison upstream")
	}
	m.RecordFailure("x", FailureAuthentication, "401", 0)
	if s, _ := m.Snapshot("x"); s.State != Open {
		t.Fatalf("auth state=%+v", s)
	}
}

func TestPersistenceSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "omnibridge.db")
	db, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	m := New(Config{FailureThreshold: 1, BaseCooldown: time.Minute}, db)
	m.RecordFailure("connection:api:p1", FailureProvider5xx, "502", 0)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db2, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	m2 := New(Config{FailureThreshold: 1, BaseCooldown: time.Minute}, db2)
	s, ok := m2.Snapshot("connection:api:p1")
	if !ok || s.State != Open || s.FailureCount != 1 {
		t.Fatalf("persisted=%+v ok=%v", s, ok)
	}
}

func TestAdaptiveCooldownEscalatesOnFailedProbe(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	m := New(Config{FailureThreshold: 1, BaseCooldown: time.Second, MaxCooldown: 8 * time.Second}, nil)
	m.now = func() time.Time { return now }
	m.RecordFailure("x", FailureProvider5xx, "first", 0)
	first, _ := m.Snapshot("x")
	d1 := first.OpenUntil.Sub(now)
	now = first.OpenUntil.Add(time.Millisecond)
	_ = m.Acquire("x")
	m.RecordFailure("x", FailureProvider5xx, "probe failed", 0)
	second, _ := m.Snapshot("x")
	d2 := second.OpenUntil.Sub(now)
	if d2 <= d1 {
		t.Fatalf("cooldown did not escalate: first=%s second=%s", d1, d2)
	}
}

func TestFailureClassification(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		kind   FailureKind
		retry  bool
	}{
		{"rate", 429, `{"error":{"message":"too many requests"}}`, FailureRateLimit, true},
		{"quota", 429, `{"error":{"code":"insufficient_quota"}}`, FailureQuotaExhausted, true},
		{"auth", 401, `{"error":"bad key"}`, FailureAuthentication, false},
		{"timeout", 504, ``, FailureTimeout, true},
		{"5xx", 502, ``, FailureProvider5xx, true},
		{"model", 404, `{"error":{"message":"model not found"}}`, FailureModelUnavailable, true},
		{"invalid", 400, `{"error":{"message":"bad temperature"}}`, FailureInvalidRequest, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.status, []byte(tc.body), nil, nil, nil)
			if got.Kind != tc.kind || got.Retryable != tc.retry {
				t.Fatalf("got=%+v", got)
			}
		})
	}
}
