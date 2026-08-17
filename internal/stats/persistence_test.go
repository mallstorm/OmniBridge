package stats

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omnibridge/internal/storage"
)

func TestPersistentStatsRoundTripWithoutJSONMirror(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "omnibridge.db"))
	if err != nil {
		if strings.Contains(err.Error(), "requires cgo") {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	path := filepath.Join(dir, "usage-stats.json")
	s := NewPersistent(path, db)
	s.Record("p1", "p1/m1", false, 10, 5, 15, 40)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	s2 := NewPersistent(path, db)
	snap := s2.Snapshot()
	if snap.Total.Requests != 1 || snap.Total.InputTokens != 10 || snap.Total.OutputTokens != 5 || snap.Total.TotalTokens != 15 {
		t.Fatalf("bad sqlite stats roundtrip: %+v", snap.Total)
	}
	counts, err := db.Counts()
	if err != nil {
		t.Fatal(err)
	}
	if counts["usage_events"] != 1 {
		t.Fatalf("usage_events=%d", counts["usage_events"])
	}
	_ = db.Close()
}
