package health

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"omnibridge/internal/storage"
)

type Event struct {
	At      time.Time `json:"at"`
	ID      string    `json:"id"`
	Source  string    `json:"source"`
	Status  string    `json:"status"`
	Models  int       `json:"models,omitempty"`
	Ready   int       `json:"ready_models,omitempty"`
	Latency int64     `json:"latency_ms,omitempty"`
	Error   string    `json:"error,omitempty"`
}

type Store struct {
	path string
	db   *storage.DB
	mu   sync.Mutex
	e    []Event
	seq  uint64
}

func New(path string) *Store {
	return newStore(path, nil)
}

func NewPersistent(path string, db *storage.DB) *Store {
	return newStore(path, db)
}

func newStore(path string, db *storage.DB) *Store {
	s := &Store{path: path, db: db}
	_ = s.load()
	return s
}

func (s *Store) load() error {
	var b []byte
	var err error
	if s.db != nil {
		if persisted, ok, dbErr := s.db.GetDocument("connection-health.json"); dbErr != nil {
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
	return json.Unmarshal(b, &s.e)
}

func (s *Store) saveLocked() error {
	return s.persistLocked(nil)
}

func (s *Store) persistLocked(event *storage.HealthEventRecord) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.e, "", "  ")
	if err != nil {
		return err
	}
	if s.db != nil {
		if err := s.db.PersistHealth(b, event); err != nil {
			return err
		}
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) Record(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.At.IsZero() {
		e.At = time.Now()
	}
	s.seq++
	s.e = append(s.e, e)
	cut := time.Now().Add(-7 * 24 * time.Hour)
	first := 0
	for first < len(s.e) && s.e[first].At.Before(cut) {
		first++
	}
	if first > 0 {
		s.e = append([]Event(nil), s.e[first:]...)
	}
	if len(s.e) > 10000 {
		s.e = append([]Event(nil), s.e[len(s.e)-10000:]...)
	}
	_ = s.persistLocked(&storage.HealthEventRecord{EventID: fmt.Sprintf("runtime:%d:%d", e.At.UnixNano(), s.seq), At: e.At, ConnectionID: e.ID, Source: e.Source, Status: e.Status, Models: e.Models, Ready: e.Ready, LatencyMS: e.Latency, Error: e.Error})
}

func (s *Store) Recent(limit int) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	start := len(s.e) - limit
	if start < 0 {
		start = 0
	}
	out := append([]Event(nil), s.e[start:]...)
	return out
}

func (s *Store) ForID(id string, limit int) []Event {
	all := s.Recent(1000)
	out := make([]Event, 0, limit)
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].ID != id {
			continue
		}
		out = append(out, all[i])
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}
