package health

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omnibridge/internal/storage"
)

func TestPersistentHealthRoundTripWithoutJSONMirror(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "omnibridge.db"))
	if err != nil {
		if strings.Contains(err.Error(), "requires cgo") {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	path := filepath.Join(dir, "connection-health.json")
	s := NewPersistent(path, db)
	s.Record(Event{ID: "p1", Source: "api", Status: "ready", Models: 2, Ready: 2, Latency: 35})
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	s2 := NewPersistent(path, db)
	got := s2.Recent(10)
	if len(got) != 1 || got[0].ID != "p1" || got[0].Ready != 2 {
		t.Fatalf("bad sqlite health roundtrip: %+v", got)
	}
	counts, err := db.Counts()
	if err != nil {
		t.Fatal(err)
	}
	if counts["connection_health"] != 1 {
		t.Fatalf("health events=%d", counts["connection_health"])
	}
	_ = db.Close()
}
