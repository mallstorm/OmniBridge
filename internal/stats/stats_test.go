package stats

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLast24hSeparatesRollingWindow(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "stats.json"))
	s.Record("p", "m", false, 10, 20, 30, 100)
	s.mu.Lock()
	s.s.Events = append(s.s.Events, Event{At: time.Now().Add(-25 * time.Hour), Provider: "old", Model: "old", Failed: true, InputTokens: 100, OutputTokens: 100, TotalTokens: 200, LatencyMS: 500})
	s.mu.Unlock()
	c := s.Last24h()
	if c.Requests != 1 || c.InputTokens != 10 || c.OutputTokens != 20 || c.TotalTokens != 30 || c.LatencyMS != 100 || c.Errors != 0 {
		t.Fatalf("unexpected rolling counter: %+v", c)
	}
}

func TestNamedPeriods(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "stats.json"))
	s.Record("p", "m", false, 4, 5, 9, 30)
	for _, period := range []string{"session", "24h", "7d", "30d", "all"} {
		if got := s.Period(period); got.Requests != 1 || got.TotalTokens != 9 {
			t.Fatalf("period %s = %+v", period, got)
		}
	}
}

func TestPeriodDetailsHasSpecificBreakdownsAndP95(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "stats.json"))
	s.Record("p1", "m1", false, 1, 2, 3, 10)
	s.Record("p1", "m1", false, 1, 2, 3, 20)
	s.Record("p2", "m2", true, 1, 0, 1, 100)
	d := s.PeriodDetails("24h")
	if d.Total.Requests != 3 || d.Providers["p1"].Requests != 2 || d.Models["m2"].Errors != 1 {
		t.Fatalf("bad details: %+v", d)
	}
	if d.Total.P95LatencyMS != 100 || d.Providers["p1"].AverageLatencyMS != 15 {
		t.Fatalf("bad latency metrics: %+v", d)
	}
}

func TestSessionPeriodDoesNotReusePersistedAllTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")
	s := New(path)
	s.Record("p", "old", false, 1, 1, 2, 10)
	s2 := New(path)
	if got := s2.Period("session"); got.Requests != 0 {
		t.Fatalf("new process session should be empty, got %+v", got)
	}
	if got := s2.Period("all"); got.Requests != 1 {
		t.Fatalf("all time should persist, got %+v", got)
	}
}

func TestCachedInputTokensAreTracked(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "stats.json"))
	s.RecordCached("yandex", "yandex/model", false, 1000, 700, 100, 1100, 25)
	got := s.Period("24h")
	if got.InputTokens != 1000 || got.CachedInputTokens != 700 {
		t.Fatalf("cached usage not tracked: %+v", got)
	}
}
