package stats

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"omnibridge/internal/storage"
)

type Counter struct {
	Requests          int64 `json:"requests"`
	Errors            int64 `json:"errors"`
	InputTokens       int64 `json:"input_tokens"`
	CachedInputTokens int64 `json:"cached_input_tokens,omitempty"`
	OutputTokens      int64 `json:"output_tokens"`
	TotalTokens       int64 `json:"total_tokens"`
	LatencyMS         int64 `json:"latency_ms"`
}

type Metrics struct {
	Counter
	AverageLatencyMS float64 `json:"average_latency_ms"`
	P95LatencyMS     int64   `json:"p95_latency_ms"`
	SuccessRate      float64 `json:"success_rate"`
}

type Event struct {
	At                time.Time `json:"at"`
	Provider          string    `json:"provider,omitempty"`
	Model             string    `json:"model,omitempty"`
	Failed            bool      `json:"failed"`
	InputTokens       int64     `json:"input_tokens"`
	CachedInputTokens int64     `json:"cached_input_tokens,omitempty"`
	OutputTokens      int64     `json:"output_tokens"`
	TotalTokens       int64     `json:"total_tokens"`
	LatencyMS         int64     `json:"latency_ms"`
}

type Snapshot struct {
	StartedAt time.Time          `json:"started_at"`
	Total     Counter            `json:"total"`
	Providers map[string]Counter `json:"providers"`
	Models    map[string]Counter `json:"models"`
	Events    []Event            `json:"events,omitempty"`
}

type PeriodSnapshot struct {
	Period    string             `json:"period"`
	From      time.Time          `json:"from,omitempty"`
	To        time.Time          `json:"to"`
	StartedAt time.Time          `json:"started_at"`
	Total     Metrics            `json:"total"`
	Providers map[string]Metrics `json:"providers"`
	Models    map[string]Metrics `json:"models"`
}

type Store struct {
	path           string
	db             *storage.DB
	mu             sync.Mutex
	s              Snapshot
	sessionStarted time.Time
	seq            uint64
	lastPersist    time.Time
	pendingPersist int
}

func New(path string) *Store {
	return newStore(path, nil)
}

func NewPersistent(path string, db *storage.DB) *Store {
	return newStore(path, db)
}

func newStore(path string, db *storage.DB) *Store {
	s := &Store{path: path, db: db, sessionStarted: time.Now()}
	_ = s.load()
	if s.s.StartedAt.IsZero() {
		s.s.StartedAt = time.Now()
	}
	if s.s.Providers == nil {
		s.s.Providers = map[string]Counter{}
	}
	if s.s.Models == nil {
		s.s.Models = map[string]Counter{}
	}
	return s
}
func (s *Store) load() error {
	var b []byte
	var err error
	if s.db != nil {
		if persisted, ok, dbErr := s.db.GetDocument("usage-stats.json"); dbErr != nil {
			return dbErr
		} else if ok {
			b = persisted
		}
	}
	if len(b) == 0 {
		b, err = os.ReadFile(s.path)
		if err != nil {
			return err
		}
	}
	return json.Unmarshal(b, &s.s)
}
func (s *Store) saveLocked() error {
	return s.persistLocked(nil)
}

func (s *Store) persistLocked(event *storage.UsageEventRecord) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.s, "", "  ")
	if err != nil {
		return err
	}
	if s.db != nil {
		if err := s.db.PersistUsage(b, event); err != nil {
			return err
		}
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
func add(c Counter, err bool, in, cached, out, total, lat int64) Counter {
	c.Requests++
	if err {
		c.Errors++
	}
	c.InputTokens += in
	c.CachedInputTokens += cached
	c.OutputTokens += out
	c.TotalTokens += total
	c.LatencyMS += lat
	return c
}
func (s *Store) Record(provider, model string, failed bool, in, out, total, lat int64) {
	s.RecordCached(provider, model, failed, in, 0, out, total, lat)
}

func (s *Store) RecordCached(provider, model string, failed bool, in, cached, out, total, lat int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.seq++
	s.s.Total = add(s.s.Total, failed, in, cached, out, total, lat)
	s.s.Providers[provider] = add(s.s.Providers[provider], failed, in, cached, out, total, lat)
	s.s.Models[model] = add(s.s.Models[model], failed, in, cached, out, total, lat)
	s.s.Events = append(s.s.Events, Event{At: now, Provider: provider, Model: model, Failed: failed, InputTokens: in, CachedInputTokens: cached, OutputTokens: out, TotalTokens: total, LatencyMS: lat})
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	first := 0
	for first < len(s.s.Events) && s.s.Events[first].At.Before(cutoff) {
		first++
	}
	if first > 0 {
		s.s.Events = append([]Event(nil), s.s.Events[first:]...)
	}
	if len(s.s.Events) > 50000 {
		s.s.Events = append([]Event(nil), s.s.Events[len(s.s.Events)-50000:]...)
	}
	event := &storage.UsageEventRecord{ID: fmt.Sprintf("runtime:%d:%d", now.UnixNano(), s.seq), At: now, Provider: provider, Model: model, Failed: failed, InputTokens: in, OutputTokens: out, TotalTokens: total, LatencyMS: lat}
	if s.db != nil {
		_ = s.db.AppendUsageEvent(event.ID, event.At, event.Provider, event.Model, event.Failed, event.InputTokens, event.OutputTokens, event.TotalTokens, event.LatencyMS)
	}
	s.pendingPersist++
	if s.db == nil || s.lastPersist.IsZero() || s.pendingPersist >= 32 || now.Sub(s.lastPersist) >= 2*time.Second {
		if err := s.persistLocked(nil); err == nil {
			s.lastPersist = now
			s.pendingPersist = 0
		}
	}
}

// Flush persists the coalesced JSON/SQLite snapshot. Individual usage events
// are appended immediately; batching only avoids rewriting the full history on
// every request under sustained load.
func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pendingPersist == 0 {
		return nil
	}
	if err := s.persistLocked(nil); err != nil {
		return err
	}
	s.lastPersist = time.Now()
	s.pendingPersist = 0
	return nil
}

func (s *Store) Last24h() Counter { return s.Window(24 * time.Hour) }

func (s *Store) Window(d time.Duration) Counter {
	return s.WindowSince(time.Now().Add(-d))
}

func (s *Store) WindowSince(cutoff time.Time) Counter {
	s.mu.Lock()
	defer s.mu.Unlock()
	var c Counter
	for _, e := range s.s.Events {
		if e.At.Before(cutoff) {
			continue
		}
		c = add(c, e.Failed, e.InputTokens, e.CachedInputTokens, e.OutputTokens, e.TotalTokens, e.LatencyMS)
	}
	return c
}

func (s *Store) Period(name string) Counter {
	s.mu.Lock()
	sessionStart := s.sessionStarted
	s.mu.Unlock()
	switch name {
	case "24h":
		return s.Window(24 * time.Hour)
	case "7d":
		return s.Window(7 * 24 * time.Hour)
	case "30d":
		return s.Window(30 * 24 * time.Hour)
	case "session":
		return s.WindowSince(sessionStart)
	case "all", "all_time", "":
		return s.Snapshot().Total
	default:
		return Counter{}
	}
}

func metricsFrom(c Counter, latencies []int64) Metrics {
	m := Metrics{Counter: c}
	if c.Requests > 0 {
		m.AverageLatencyMS = math.Round((float64(c.LatencyMS)/float64(c.Requests))*100) / 100
		m.SuccessRate = math.Round((float64(c.Requests-c.Errors)/float64(c.Requests))*10000) / 100
	}
	if len(latencies) > 0 {
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		idx := int(math.Ceil(float64(len(latencies))*0.95)) - 1
		if idx < 0 {
			idx = 0
		}
		if idx >= len(latencies) {
			idx = len(latencies) - 1
		}
		m.P95LatencyMS = latencies[idx]
	}
	return m
}

func (s *Store) PeriodDetails(name string) PeriodSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	from := time.Time{}
	switch name {
	case "session":
		from = s.sessionStarted
	case "24h":
		from = now.Add(-24 * time.Hour)
	case "7d":
		from = now.Add(-7 * 24 * time.Hour)
	case "30d":
		from = now.Add(-30 * 24 * time.Hour)
	case "all", "all_time", "":
		name = "all"
	default:
		return PeriodSnapshot{Period: name, To: now, StartedAt: s.s.StartedAt, Providers: map[string]Metrics{}, Models: map[string]Metrics{}}
	}
	total := Counter{}
	providerCounters := map[string]Counter{}
	modelCounters := map[string]Counter{}
	totalLat := []int64{}
	providerLat := map[string][]int64{}
	modelLat := map[string][]int64{}
	for _, e := range s.s.Events {
		if !from.IsZero() && e.At.Before(from) {
			continue
		}
		total = add(total, e.Failed, e.InputTokens, e.CachedInputTokens, e.OutputTokens, e.TotalTokens, e.LatencyMS)
		providerCounters[e.Provider] = add(providerCounters[e.Provider], e.Failed, e.InputTokens, e.CachedInputTokens, e.OutputTokens, e.TotalTokens, e.LatencyMS)
		modelCounters[e.Model] = add(modelCounters[e.Model], e.Failed, e.InputTokens, e.CachedInputTokens, e.OutputTokens, e.TotalTokens, e.LatencyMS)
		totalLat = append(totalLat, e.LatencyMS)
		providerLat[e.Provider] = append(providerLat[e.Provider], e.LatencyMS)
		modelLat[e.Model] = append(modelLat[e.Model], e.LatencyMS)
	}
	// Preserve exact all-time counters even when detailed events have rolled off.
	if from.IsZero() {
		total = s.s.Total
		providerCounters = s.s.Providers
		modelCounters = s.s.Models
	}
	providersOut := make(map[string]Metrics, len(providerCounters))
	for k, c := range providerCounters {
		providersOut[k] = metricsFrom(c, append([]int64(nil), providerLat[k]...))
	}
	modelsOut := make(map[string]Metrics, len(modelCounters))
	for k, c := range modelCounters {
		modelsOut[k] = metricsFrom(c, append([]int64(nil), modelLat[k]...))
	}
	return PeriodSnapshot{Period: name, From: from, To: now, StartedAt: s.s.StartedAt, Total: metricsFrom(total, totalLat), Providers: providersOut, Models: modelsOut}
}

func (s *Store) ModelMetrics(model, period string) Metrics {
	return s.PeriodDetails(period).Models[model]
}

func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := json.Marshal(s.s)
	var x Snapshot
	_ = json.Unmarshal(b, &x)
	return x
}
