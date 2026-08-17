package health

import (
	"path/filepath"
	"testing"
	"time"
)

func TestHealthHistoryPersistsAndFilters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "health.json")
	s := New(path)
	s.Record(Event{ID: "p1", Source: "api", Status: "ready"})
	s.Record(Event{ID: "p2", Source: "account", Status: "error", Error: "x"})
	s2 := New(path)
	if got := s2.ForID("p1", 10); len(got) != 1 || got[0].Status != "ready" {
		t.Fatalf("bad history: %+v", got)
	}
	s2.mu.Lock()
	s2.e = append([]Event{{At: time.Now().Add(-8 * 24 * time.Hour), ID: "old", Status: "error"}}, s2.e...)
	s2.mu.Unlock()
	s2.Record(Event{ID: "p1", Source: "api", Status: "ready"})
	if got := s2.ForID("old", 10); len(got) != 0 {
		t.Fatalf("old event retained: %+v", got)
	}
}
