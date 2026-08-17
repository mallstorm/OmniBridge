package resilience

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"omnibridge/internal/storage"
)

type State string

const (
	Closed   State = "CLOSED"
	Degraded State = "DEGRADED"
	Open     State = "OPEN"
	HalfOpen State = "HALF_OPEN"
)

type FailureKind string

const (
	FailureRateLimit        FailureKind = "rate_limit"
	FailureQuotaExhausted   FailureKind = "quota_exhausted"
	FailureTimeout          FailureKind = "timeout"
	FailureNetwork          FailureKind = "network"
	FailureProvider5xx      FailureKind = "provider_5xx"
	FailureAuthentication   FailureKind = "authentication"
	FailureModelUnavailable FailureKind = "model_unavailable"
	FailureInvalidRequest   FailureKind = "invalid_request"
	FailureClientCancelled  FailureKind = "client_cancelled"
	FailureUnknown          FailureKind = "unknown"
)

type Config struct {
	FailureThreshold     int
	DegradationThreshold int
	BaseCooldown         time.Duration
	MaxCooldown          time.Duration
	HalfOpenProbes       int
}

type Transition struct {
	At           time.Time   `json:"at"`
	From         State       `json:"from"`
	To           State       `json:"to"`
	FailureKind  FailureKind `json:"failure_kind,omitempty"`
	Reason       string      `json:"reason,omitempty"`
	RetryAfterMS int64       `json:"retry_after_ms,omitempty"`
}

type Snapshot struct {
	Key               string              `json:"key"`
	State             State               `json:"state"`
	FailureCount      int                 `json:"failure_count"`
	SuccessCount      int                 `json:"success_count"`
	KindFailureCounts map[FailureKind]int `json:"kind_failure_counts,omitempty"`
	LastFailureKind   FailureKind         `json:"last_failure_kind,omitempty"`
	LastFailureAt     time.Time           `json:"last_failure_at,omitempty"`
	LastSuccessAt     time.Time           `json:"last_success_at,omitempty"`
	OpenedAt          time.Time           `json:"opened_at,omitempty"`
	OpenUntil         time.Time           `json:"open_until,omitempty"`
	OpenCycleCount    int                 `json:"open_cycle_count"`
	HalfOpenRemaining int                 `json:"half_open_remaining"`
	TransitionHistory []Transition        `json:"transition_history,omitempty"`
	UpdatedAt         time.Time           `json:"updated_at"`
}

type Decision struct {
	Allowed      bool          `json:"allowed"`
	State        State         `json:"state"`
	RetryAfter   time.Duration `json:"-"`
	RetryAfterMS int64         `json:"retry_after_ms,omitempty"`
	Probe        bool          `json:"probe,omitempty"`
}

type Manager struct {
	mu       sync.Mutex
	cfg      Config
	db       *storage.DB
	breakers map[string]*Snapshot
	now      func() time.Time
}

func New(cfg Config, db *storage.DB) *Manager {
	cfg = normalizeConfig(cfg)
	m := &Manager{cfg: cfg, db: db, breakers: map[string]*Snapshot{}, now: time.Now}
	m.load()
	return m
}

func normalizeConfig(c Config) Config {
	if c.FailureThreshold <= 0 {
		c.FailureThreshold = 3
	}
	if c.DegradationThreshold <= 0 {
		c.DegradationThreshold = int(math.Ceil(float64(c.FailureThreshold) * 0.6))
		if c.DegradationThreshold < 1 {
			c.DegradationThreshold = 1
		}
	}
	if c.DegradationThreshold >= c.FailureThreshold && c.FailureThreshold > 1 {
		c.DegradationThreshold = c.FailureThreshold - 1
	}
	if c.BaseCooldown <= 0 {
		c.BaseCooldown = 30 * time.Second
	}
	if c.MaxCooldown <= 0 {
		c.MaxCooldown = 30 * time.Minute
	}
	if c.MaxCooldown < c.BaseCooldown {
		c.MaxCooldown = c.BaseCooldown
	}
	if c.HalfOpenProbes <= 0 {
		c.HalfOpenProbes = 1
	}
	return c
}

func (m *Manager) Reconfigure(c Config) {
	m.mu.Lock()
	m.cfg = normalizeConfig(c)
	m.mu.Unlock()
}

func (m *Manager) load() {
	if m.db == nil {
		return
	}
	rows, err := m.db.LoadCircuitBreakers()
	if err != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, row := range rows {
		var s Snapshot
		if json.Unmarshal(row.Payload, &s) != nil {
			continue
		}
		if s.Key == "" {
			s.Key = row.Key
		}
		if s.State == "" {
			s.State = State(row.State)
		}
		if s.KindFailureCounts == nil {
			s.KindFailureCounts = map[FailureKind]int{}
		}
		m.breakers[s.Key] = &s
	}
}

func (m *Manager) persistLocked(s *Snapshot, tr *Transition) {
	if m.db == nil || s == nil {
		return
	}
	b, err := json.Marshal(s)
	if err == nil {
		_ = m.db.SaveCircuitBreaker(s.Key, string(s.State), s.UpdatedAt, b)
	}
	if tr != nil {
		_ = m.db.AppendResilienceEvent(s.Key, tr.At, string(tr.From), string(tr.To), string(tr.FailureKind), tr.Reason, tr.RetryAfterMS)
	}
}

func (m *Manager) ensureLocked(key string) *Snapshot {
	s := m.breakers[key]
	if s == nil {
		s = &Snapshot{Key: key, State: Closed, KindFailureCounts: map[FailureKind]int{}, UpdatedAt: m.now().UTC()}
		m.breakers[key] = s
	}
	if s.KindFailureCounts == nil {
		s.KindFailureCounts = map[FailureKind]int{}
	}
	return s
}

func (m *Manager) transitionLocked(s *Snapshot, to State, kind FailureKind, reason string, retry time.Duration) {
	if s.State == to {
		return
	}
	now := m.now().UTC()
	tr := Transition{At: now, From: s.State, To: to, FailureKind: kind, Reason: reason}
	if retry > 0 {
		tr.RetryAfterMS = retry.Milliseconds()
	}
	s.State = to
	s.UpdatedAt = now
	s.TransitionHistory = append(s.TransitionHistory, tr)
	if len(s.TransitionHistory) > 24 {
		s.TransitionHistory = append([]Transition(nil), s.TransitionHistory[len(s.TransitionHistory)-24:]...)
	}
	m.persistLocked(s, &tr)
}

func (m *Manager) effectiveCooldownLocked(s *Snapshot, override time.Duration) time.Duration {
	if override > 0 {
		if override > m.cfg.MaxCooldown {
			return m.cfg.MaxCooldown
		}
		return override
	}
	factor := 1
	if s.OpenCycleCount > 1 {
		shift := s.OpenCycleCount - 1
		if shift > 10 {
			shift = 10
		}
		factor = 1 << shift
	}
	d := time.Duration(factor) * m.cfg.BaseCooldown
	if d > m.cfg.MaxCooldown {
		d = m.cfg.MaxCooldown
	}
	return d
}

func ignoredFailure(kind FailureKind) bool {
	return kind == FailureClientCancelled || kind == FailureInvalidRequest
}

func immediateOpen(kind FailureKind) bool {
	return kind == FailureQuotaExhausted || kind == FailureAuthentication || kind == FailureModelUnavailable
}

// Blocked checks whether a key may participate in routing without consuming a HALF_OPEN probe.
func (m *Manager) Blocked(key string) Decision {
	key = strings.TrimSpace(key)
	if key == "" {
		return Decision{Allowed: true, State: Closed}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.breakers[key]
	if s == nil {
		return Decision{Allowed: true, State: Closed}
	}
	now := m.now().UTC()
	if s.State == Open && !s.OpenUntil.IsZero() && !now.Before(s.OpenUntil) {
		s.HalfOpenRemaining = m.cfg.HalfOpenProbes
		s.OpenUntil = time.Time{}
		m.transitionLocked(s, HalfOpen, s.LastFailureKind, "cooldown_elapsed_probe", 0)
	}
	if s.State == Open {
		retry := s.OpenUntil.Sub(now)
		if retry < 0 {
			retry = 0
		}
		return Decision{Allowed: false, State: Open, RetryAfter: retry, RetryAfterMS: retry.Milliseconds()}
	}
	if s.State == HalfOpen && s.HalfOpenRemaining <= 0 {
		return Decision{Allowed: false, State: HalfOpen}
	}
	return Decision{Allowed: true, State: s.State, Probe: s.State == HalfOpen}
}

// Acquire reserves a HALF_OPEN probe if needed. CLOSED/DEGRADED requests pass without reservation.
func (m *Manager) Acquire(key string) Decision {
	key = strings.TrimSpace(key)
	if key == "" {
		return Decision{Allowed: true, State: Closed}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.breakers[key]
	if s == nil {
		return Decision{Allowed: true, State: Closed}
	}
	now := m.now().UTC()
	if s.State == Open && !s.OpenUntil.IsZero() && !now.Before(s.OpenUntil) {
		s.HalfOpenRemaining = m.cfg.HalfOpenProbes
		s.OpenUntil = time.Time{}
		m.transitionLocked(s, HalfOpen, s.LastFailureKind, "cooldown_elapsed_probe", 0)
	}
	if s.State == Open {
		retry := s.OpenUntil.Sub(now)
		if retry < 0 {
			retry = 0
		}
		return Decision{Allowed: false, State: Open, RetryAfter: retry, RetryAfterMS: retry.Milliseconds()}
	}
	if s.State == HalfOpen {
		if s.HalfOpenRemaining <= 0 {
			return Decision{Allowed: false, State: HalfOpen}
		}
		s.HalfOpenRemaining--
		s.UpdatedAt = now
		m.persistLocked(s, nil)
		return Decision{Allowed: true, State: HalfOpen, Probe: true}
	}
	return Decision{Allowed: true, State: s.State}
}

func (m *Manager) RecordFailure(key string, kind FailureKind, reason string, cooldownOverride time.Duration) {
	key = strings.TrimSpace(key)
	if key == "" || ignoredFailure(kind) {
		return
	}
	if kind == "" {
		kind = FailureUnknown
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.ensureLocked(key)
	now := m.now().UTC()
	s.FailureCount++
	s.KindFailureCounts[kind]++
	s.LastFailureKind = kind
	s.LastFailureAt = now
	s.UpdatedAt = now

	shouldOpen := immediateOpen(kind) || s.FailureCount >= m.cfg.FailureThreshold || s.State == HalfOpen
	if shouldOpen {
		s.OpenCycleCount++
		cd := m.effectiveCooldownLocked(s, cooldownOverride)
		s.OpenedAt = now
		s.OpenUntil = now.Add(cd)
		s.HalfOpenRemaining = 0
		m.transitionLocked(s, Open, kind, reason, cd)
		m.persistLocked(s, nil)
		return
	}
	if s.FailureCount >= m.cfg.DegradationThreshold && s.State == Closed {
		m.transitionLocked(s, Degraded, kind, reason, 0)
	}
	m.persistLocked(s, nil)
}

func (m *Manager) RecordSuccess(key string) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.breakers[key]
	if s == nil {
		return
	}
	now := m.now().UTC()
	s.SuccessCount++
	s.LastSuccessAt = now
	s.UpdatedAt = now
	if s.State != Closed || s.FailureCount > 0 {
		from := s.State
		s.State = Closed
		s.FailureCount = 0
		s.KindFailureCounts = map[FailureKind]int{}
		s.OpenUntil = time.Time{}
		s.OpenedAt = time.Time{}
		s.HalfOpenRemaining = 0
		s.OpenCycleCount = 0
		tr := Transition{At: now, From: from, To: Closed, Reason: "successful_upstream_response"}
		s.TransitionHistory = append(s.TransitionHistory, tr)
		if len(s.TransitionHistory) > 24 {
			s.TransitionHistory = append([]Transition(nil), s.TransitionHistory[len(s.TransitionHistory)-24:]...)
		}
		m.persistLocked(s, &tr)
		return
	}
	m.persistLocked(s, nil)
}

func (m *Manager) Reset(key string) error {
	key = strings.TrimSpace(key)
	m.mu.Lock()
	defer m.mu.Unlock()
	if key == "" {
		m.breakers = map[string]*Snapshot{}
		if m.db != nil {
			return m.db.DeleteCircuitBreakers("")
		}
		return nil
	}
	delete(m.breakers, key)
	if m.db != nil {
		return m.db.DeleteCircuitBreakers(key)
	}
	return nil
}

func (m *Manager) Snapshot(key string) (Snapshot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.breakers[key]
	if s == nil {
		return Snapshot{}, false
	}
	b, _ := json.Marshal(s)
	var out Snapshot
	_ = json.Unmarshal(b, &out)
	return out, true
}

func (m *Manager) Snapshots() []Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Snapshot, 0, len(m.breakers))
	for _, s := range m.breakers {
		b, _ := json.Marshal(s)
		var c Snapshot
		_ = json.Unmarshal(b, &c)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func (m *Manager) StateFor(key string) State {
	d := m.Blocked(key)
	return d.State
}

func (m *Manager) HealthRank(key string) int {
	switch m.StateFor(key) {
	case Closed:
		return 0
	case Degraded:
		return 1
	case HalfOpen:
		return 2
	case Open:
		return 3
	default:
		return 4
	}
}

func ConnectionKey(source, id string) string {
	source = strings.ToLower(strings.TrimSpace(source))
	if source == "" {
		source = "api"
	}
	return "connection:" + source + ":" + strings.TrimSpace(id)
}

func ModelKey(modelID string) string { return "model:" + strings.TrimSpace(modelID) }

func ValidateState(s State) error {
	switch s {
	case Closed, Degraded, Open, HalfOpen:
		return nil
	default:
		return fmt.Errorf("invalid resilience state %q", s)
	}
}

var ErrProbeBusy = errors.New("half-open recovery probe already in flight")
