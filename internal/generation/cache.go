package generation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"omnibridge/internal/config"
)

const Version = "1.0"

type Entry struct {
	Signature       string    `json:"signature"`
	NamespaceHash   string    `json:"namespace_hash"`
	ProviderID      string    `json:"provider_id"`
	ModelID         string    `json:"model_id"`
	NativeModelID   string    `json:"native_model_id"`
	IdentityVersion string    `json:"identity_version"`
	ContentType     string    `json:"content_type"`
	Body            []byte    `json:"-"`
	CreatedAt       time.Time `json:"created_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	Hits            uint64    `json:"hits"`
}

type flight struct {
	done  chan struct{}
	entry Entry
	ok    bool
}

type Manager struct {
	mu                       sync.Mutex
	cfg                      config.ExactCacheConfig
	entries                  map[string]Entry
	order                    []string
	flights                  map[string]*flight
	completed                map[string]Entry
	hits, misses, dedupWaits atomic.Uint64
}

func New(cfg config.ExactCacheConfig) *Manager {
	return &Manager{cfg: cfg, entries: map[string]Entry{}, flights: map[string]*flight{}, completed: map[string]Entry{}}
}
func (m *Manager) UpdateConfig(cfg config.ExactCacheConfig) {
	m.mu.Lock()
	m.cfg = cfg
	m.pruneLocked(time.Now())
	m.mu.Unlock()
}

func Namespace(raw string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	return hex.EncodeToString(sum[:12])
}
func IdentityVersion(modelID, native string, checked time.Time) string {
	return Namespace(modelID + "\x00" + native + "\x00" + checked.UTC().Format(time.RFC3339Nano))
}

func Cacheable(raw []byte) bool {
	var v map[string]any
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	if stream, _ := v["stream"].(bool); stream {
		return false
	}
	if tools, ok := v["tools"].([]any); ok && len(tools) > 0 {
		return false
	}
	// Exact caching is intentionally opt-in at the request level as well as in
	// config: a missing temperature must not be treated as temperature=0 because
	// OpenAI-compatible APIs commonly default it to a non-deterministic value.
	rawTemp, exists := v["temperature"]
	if !exists {
		return false
	}
	temp, ok := rawTemp.(float64)
	if !ok {
		return false
	}
	return temp == 0
}

func Signature(namespace, provider, model, native, identity string, raw []byte) string {
	var v any
	normalized := raw
	if json.Unmarshal(raw, &v) == nil {
		if b, e := json.Marshal(v); e == nil {
			normalized = b
		}
	}
	h := sha256.New()
	for _, s := range []string{namespace, provider, model, native, identity} {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	h.Write(normalized)
	return hex.EncodeToString(h.Sum(nil))
}

func (m *Manager) pruneLocked(now time.Time) {
	keep := m.order[:0]
	for _, k := range m.order {
		if e, ok := m.entries[k]; ok && now.Before(e.ExpiresAt) {
			keep = append(keep, k)
		} else {
			delete(m.entries, k)
		}
	}
	m.order = keep
	max := m.cfg.MaxEntries
	if max < 1 {
		max = 256
	}
	for len(m.order) > max {
		delete(m.entries, m.order[0])
		m.order = m.order[1:]
	}
}

func (m *Manager) Get(signature string) (Entry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked(time.Now())
	e, ok := m.entries[signature]
	if !ok {
		m.misses.Add(1)
		return Entry{}, false
	}
	e.Hits++
	m.entries[signature] = e
	m.hits.Add(1)
	return clone(e), true
}
func clone(e Entry) Entry { e.Body = append([]byte(nil), e.Body...); return e }

func (m *Manager) Put(e Entry) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.cfg.Enabled {
		return false
	}
	max := m.cfg.MaxEntryBytes
	if max < 1024 {
		max = 2 << 20
	}
	if len(e.Body) > max {
		return false
	}
	now := time.Now().UTC()
	e.CreatedAt = now
	ttl := m.cfg.TTLSeconds
	if ttl < 1 {
		ttl = 120
	}
	e.ExpiresAt = now.Add(time.Duration(ttl) * time.Second)
	if _, ok := m.entries[e.Signature]; !ok {
		m.order = append(m.order, e.Signature)
	}
	m.entries[e.Signature] = clone(e)
	m.pruneLocked(now)
	return true
}

// Join returns a waiter for an identical deterministic request. The first caller
// becomes leader; followers never execute upstream while the leader is active.
func (m *Manager) Join(signature string) (<-chan struct{}, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.cfg.Deduplicate {
		return nil, true
	}
	if f := m.flights[signature]; f != nil {
		m.dedupWaits.Add(1)
		return f.done, false
	}
	f := &flight{done: make(chan struct{})}
	m.flights[signature] = f
	return f.done, true
}
func (m *Manager) Complete(signature string, e Entry, ok bool) {
	m.mu.Lock()
	f := m.flights[signature]
	if f != nil {
		f.entry = clone(e)
		f.ok = ok
		if ok {
			e.ExpiresAt = time.Now().Add(5 * time.Second)
			m.completed[signature] = clone(e)
		}
		delete(m.flights, signature)
		close(f.done)
	}
	m.mu.Unlock()
}
func (m *Manager) WaitResult(signature string) (Entry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.entries[signature]; ok {
		return clone(e), true
	}
	e, ok := m.completed[signature]
	if !ok || time.Now().After(e.ExpiresAt) {
		delete(m.completed, signature)
		return Entry{}, false
	}
	return clone(e), true
}
func (m *Manager) Clear() { m.mu.Lock(); m.entries = map[string]Entry{}; m.order = nil; m.mu.Unlock() }
func (m *Manager) Snapshot() map[string]any {
	m.mu.Lock()
	m.pruneLocked(time.Now())
	list := make([]Entry, 0, len(m.entries))
	for _, e := range m.entries {
		e.Body = nil
		list = append(list, e)
	}
	active := len(m.flights)
	cfg := m.cfg
	m.mu.Unlock()
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.After(list[j].CreatedAt) })
	return map[string]any{"version": Version, "config": cfg, "entries": len(list), "active_deduplications": active, "hits": m.hits.Load(), "misses": m.misses.Load(), "dedup_waits": m.dedupWaits.Load(), "recent": list}
}
