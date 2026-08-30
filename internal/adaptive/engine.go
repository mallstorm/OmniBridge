package adaptive

import (
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"omnibridge/internal/config"
	"omnibridge/internal/storage"
)

const Version = "1.0"

type Outcome struct {
	TraceID, Route, ProviderID, ConnectionID, ModelID, NativeModelID string
	Success, RateLimited, Anomaly                                    bool
	LatencyMS, TTFTMS                                                int64
	SemanticQuality                                                  *float64
	At                                                               time.Time
}

type Quality struct {
	ProviderID    string    `json:"provider_id"`
	ConnectionID  string    `json:"connection_id"`
	ModelID       string    `json:"model_id"`
	NativeModelID string    `json:"native_model_id,omitempty"`
	SuccessEWMA   float64   `json:"success_ewma"`
	LatencyEWMA   float64   `json:"latency_ewma_ms,omitempty"`
	TTFTEWMA      float64   `json:"ttft_ewma_ms,omitempty"`
	SemanticEWMA  *float64  `json:"semantic_ewma,omitempty"`
	Samples       uint64    `json:"samples"`
	RateLimits    uint64    `json:"rate_limits"`
	Anomalies     uint64    `json:"anomalies"`
	Confidence    float64   `json:"confidence"`
	Score         float64   `json:"score"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type CandidateDecision struct {
	ModelID       string             `json:"model_id"`
	ProviderID    string             `json:"provider_id,omitempty"`
	NativeModelID string             `json:"native_model_id,omitempty"`
	Score         float64            `json:"score,omitempty"`
	Components    map[string]float64 `json:"components,omitempty"`
	Status        string             `json:"status"`
	Reasons       []string           `json:"reasons,omitempty"`
}

type Decision struct {
	TraceID        string              `json:"trace_id"`
	At             time.Time           `json:"at"`
	Route          string              `json:"route"`
	Strategy       string              `json:"strategy"`
	RequestedModel string              `json:"requested_model"`
	SelectedModel  string              `json:"selected_model,omitempty"`
	ProviderID     string              `json:"provider_id,omitempty"`
	NativeModelID  string              `json:"native_model_id,omitempty"`
	Attempts       int                 `json:"attempts"`
	Fallback       bool                `json:"fallback"`
	LatencyMS      int64               `json:"latency_ms,omitempty"`
	TTFTMS         int64               `json:"ttft_ms,omitempty"`
	CacheHit       bool                `json:"cache_hit,omitempty"`
	DedupHit       bool                `json:"dedup_hit,omitempty"`
	ContextMode    string              `json:"context_mode,omitempty"`
	Candidates     []CandidateDecision `json:"candidates"`
}

type Engine struct {
	mu        sync.RWMutex
	cfg       config.AdaptiveConfig
	db        *storage.DB
	quality   map[string]Quality
	decisions map[string]Decision
	order     []string
	events    chan Outcome
	dropped   atomic.Uint64
}

func New(db *storage.DB, cfg config.AdaptiveConfig) *Engine {
	if cfg.EventQueueSize < 16 {
		cfg.EventQueueSize = 512
	}
	e := &Engine{cfg: cfg, db: db, quality: map[string]Quality{}, decisions: map[string]Decision{}, events: make(chan Outcome, cfg.EventQueueSize)}
	go e.run()
	return e
}

func (e *Engine) UpdateConfig(cfg config.AdaptiveConfig) { e.mu.Lock(); e.cfg = cfg; e.mu.Unlock() }

func key(provider, connection, model string) string {
	return strings.ToLower(strings.TrimSpace(provider)) + "\x00" + strings.ToLower(strings.TrimSpace(connection)) + "\x00" + strings.ToLower(strings.TrimSpace(model))
}

func ewma(old, value, alpha float64, first bool) float64 {
	if first {
		return value
	}
	return alpha*value + (1-alpha)*old
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (e *Engine) run() {
	for event := range e.events {
		e.apply(event)
	}
}

func (e *Engine) Observe(event Outcome) bool {
	e.mu.RLock()
	enabled := e.cfg.Enabled
	e.mu.RUnlock()
	if !enabled {
		return false
	}
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	select {
	case e.events <- event:
		return true
	default:
		e.dropped.Add(1)
		return false
	}
}

func (e *Engine) apply(event Outcome) {
	e.mu.Lock()
	k := key(event.ProviderID, event.ConnectionID, event.ModelID)
	q := e.quality[k]
	first := q.Samples == 0
	if first {
		q.SuccessEWMA = .5
	}
	q.ProviderID, q.ConnectionID, q.ModelID, q.NativeModelID = event.ProviderID, event.ConnectionID, event.ModelID, event.NativeModelID
	success := 0.0
	if event.Success {
		success = 1
	}
	q.SuccessEWMA = ewma(q.SuccessEWMA, success, e.cfg.SuccessAlpha, false)
	if event.LatencyMS > 0 {
		q.LatencyEWMA = ewma(q.LatencyEWMA, float64(event.LatencyMS), e.cfg.LatencyAlpha, first)
	}
	if event.TTFTMS > 0 {
		q.TTFTEWMA = ewma(q.TTFTEWMA, float64(event.TTFTMS), e.cfg.LatencyAlpha, first)
	}
	if event.SemanticQuality != nil {
		v := clamp(*event.SemanticQuality, 0, 1)
		if q.SemanticEWMA == nil {
			q.SemanticEWMA = &v
		} else {
			n := ewma(*q.SemanticEWMA, v, e.cfg.SuccessAlpha, false)
			q.SemanticEWMA = &n
		}
	}
	q.Samples++
	if event.RateLimited {
		q.RateLimits++
	}
	if event.Anomaly {
		q.Anomalies++
	}
	full := e.cfg.FullConfidenceAt
	if full < 1 {
		full = 50
	}
	q.Confidence = clamp(float64(q.Samples)/float64(full), 0, 1)
	latencyScore := .5
	if q.LatencyEWMA > 0 {
		latencyScore = 1 / (1 + q.LatencyEWMA/2000)
	}
	base := .72*q.SuccessEWMA + .28*latencyScore
	if q.SemanticEWMA != nil {
		base = .62*q.SuccessEWMA + .18*latencyScore + .20*(*q.SemanticEWMA)
	}
	penalty := math.Min(.25, float64(q.RateLimits+q.Anomalies)/math.Max(10, float64(q.Samples))*0.5)
	observed := clamp(base-penalty, 0, 1)
	q.Score = .5*(1-q.Confidence) + observed*q.Confidence
	q.UpdatedAt = event.At.UTC()
	e.quality[k] = q
	e.mu.Unlock()
	if e.db != nil {
		_ = e.db.SaveRoutingQuality(q.ProviderID, q.ConnectionID, q.ModelID, q.UpdatedAt, q)
	}
}

func (e *Engine) Quality(provider, connection, model string) Quality {
	e.mu.RLock()
	q, ok := e.quality[key(provider, connection, model)]
	e.mu.RUnlock()
	if !ok {
		return Quality{ProviderID: provider, ConnectionID: connection, ModelID: model, SuccessEWMA: .5, Confidence: 0, Score: .5}
	}
	return q
}

func (e *Engine) Snapshot() map[string]any {
	e.mu.RLock()
	enabled := e.cfg.Enabled
	out := make([]Quality, 0, len(e.quality))
	for _, q := range e.quality {
		out = append(out, q)
	}
	e.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ModelID < out[j].ModelID
	})
	return map[string]any{"version": Version, "enabled": enabled, "quality": out, "dropped_events": e.dropped.Load()}
}

func (e *Engine) RecordDecision(d Decision) {
	if strings.TrimSpace(d.TraceID) == "" {
		return
	}
	if d.At.IsZero() {
		d.At = time.Now().UTC()
	}
	e.mu.Lock()
	e.decisions[d.TraceID] = d
	e.order = append(e.order, d.TraceID)
	max := e.cfg.MaxDecisions
	if max < 10 {
		max = 500
	}
	cutoff := time.Now().Add(-time.Duration(e.cfg.DecisionTTLMinutes) * time.Minute)
	for len(e.order) > 0 && (len(e.order) > max || e.decisions[e.order[0]].At.Before(cutoff)) {
		delete(e.decisions, e.order[0])
		e.order = e.order[1:]
	}
	e.mu.Unlock()
	if e.db != nil {
		_ = e.db.SaveRouteDecision(d.TraceID, d.At, d)
	}
}

func (e *Engine) Explain(traceID string) (Decision, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	d, ok := e.decisions[traceID]
	return d, ok
}

func (e *Engine) Recent(limit int) []Decision {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if limit > len(e.order) {
		limit = len(e.order)
	}
	out := make([]Decision, 0, limit)
	for i := len(e.order) - 1; i >= len(e.order)-limit; i-- {
		out = append(out, e.decisions[e.order[i]])
	}
	return out
}
