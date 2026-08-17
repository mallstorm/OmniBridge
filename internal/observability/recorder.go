package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"omnibridge/internal/storage"
)

const Version = "1.0"

type Trace struct {
	ID            string         `json:"trace_id"`
	StartedAt     time.Time      `json:"started_at"`
	DurationMS    int64          `json:"duration_ms"`
	Method        string         `json:"method"`
	Path          string         `json:"path"`
	Model         string         `json:"model,omitempty"`
	SelectedModel string         `json:"selected_model,omitempty"`
	Provider      string         `json:"provider,omitempty"`
	Status        string         `json:"status"`
	ErrorKind     string         `json:"error_kind,omitempty"`
	Attempts      int            `json:"attempts,omitempty"`
	Streaming     bool           `json:"streaming,omitempty"`
	CrossProtocol bool           `json:"cross_protocol,omitempty"`
	ContextAction string         `json:"context_action,omitempty"`
	CostUSD       float64        `json:"cost_usd,omitempty"`
	CostKnown     bool           `json:"cost_known,omitempty"`
	Attributes    map[string]any `json:"attributes,omitempty"`
}

type pathMetric struct {
	Requests  uint64 `json:"requests"`
	Errors    uint64 `json:"errors"`
	LatencyMS uint64 `json:"latency_ms"`
}
type Snapshot struct {
	Version      string                `json:"version"`
	StartedAt    time.Time             `json:"started_at"`
	Requests     uint64                `json:"requests"`
	Errors       uint64                `json:"errors"`
	Active       int64                 `json:"active"`
	LatencyMS    uint64                `json:"latency_ms"`
	AvgLatencyMS float64               `json:"avg_latency_ms"`
	P50LatencyMS int64                 `json:"p50_latency_ms"`
	P95LatencyMS int64                 `json:"p95_latency_ms"`
	Paths        map[string]pathMetric `json:"paths"`
}

type Recorder struct {
	db             *storage.DB
	started        time.Time
	requests       atomic.Uint64
	errors         atomic.Uint64
	active         atomic.Int64
	latency        atomic.Uint64
	mu             sync.Mutex
	paths          map[string]pathMetric
	recent         []Trace
	maxRecent      int
	latencySamples []int64
	maxSamples     int
}

func New(db *storage.DB) *Recorder {
	return &Recorder{db: db, started: time.Now().UTC(), paths: map[string]pathMetric{}, maxRecent: 200, maxSamples: 2048}
}

type traceKey struct{}

func newID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("tr-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func (r *Recorder) Begin(req *http.Request) (context.Context, *Trace) {
	id := strings.TrimSpace(req.Header.Get("X-OmniBridge-Trace-ID"))
	if id == "" || len(id) > 128 {
		id = newID()
	}
	t := &Trace{ID: id, StartedAt: time.Now().UTC(), Method: req.Method, Path: req.URL.Path, Status: "running", Attributes: map[string]any{}}
	r.active.Add(1)
	return context.WithValue(req.Context(), traceKey{}, t), t
}
func FromContext(ctx context.Context) *Trace { t, _ := ctx.Value(traceKey{}).(*Trace); return t }

func (r *Recorder) Finish(t *Trace, statusCode int) {
	if t == nil {
		return
	}
	t.DurationMS = time.Since(t.StartedAt).Milliseconds()
	if statusCode >= 400 {
		t.Status = "error"
	} else {
		t.Status = "ok"
	}
	r.requests.Add(1)
	if statusCode >= 400 {
		r.errors.Add(1)
	}
	if t.DurationMS > 0 {
		r.latency.Add(uint64(t.DurationMS))
	}
	r.active.Add(-1)
	r.mu.Lock()
	pm := r.paths[t.Path]
	pm.Requests++
	if statusCode >= 400 {
		pm.Errors++
	}
	if t.DurationMS > 0 {
		pm.LatencyMS += uint64(t.DurationMS)
	}
	r.paths[t.Path] = pm
	r.recent = append(r.recent, *t)
	if len(r.recent) > r.maxRecent {
		r.recent = append([]Trace(nil), r.recent[len(r.recent)-r.maxRecent:]...)
	}
	if t.DurationMS >= 0 {
		r.latencySamples = append(r.latencySamples, t.DurationMS)
		if len(r.latencySamples) > r.maxSamples {
			r.latencySamples = append([]int64(nil), r.latencySamples[len(r.latencySamples)-r.maxSamples:]...)
		}
	}
	r.mu.Unlock()
	if r.db != nil {
		b, _ := json.Marshal(map[string]any{"streaming": t.Streaming, "cross_protocol": t.CrossProtocol, "context_action": t.ContextAction, "cost_usd": t.CostUSD, "cost_known": t.CostKnown, "attributes": t.Attributes})
		_ = r.db.AppendTrace(t.ID, t.StartedAt, t.DurationMS, t.Method, t.Path, t.Model, t.SelectedModel, t.Provider, t.Status, t.ErrorKind, t.Attempts, b)
	}
}

func percentile(samples []int64, q float64) int64 {
	if len(samples) == 0 {
		return 0
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	idx := int(float64(len(samples)-1)*q + 0.5)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(samples) {
		idx = len(samples) - 1
	}
	return samples[idx]
}
func (r *Recorder) Snapshot() Snapshot {
	r.mu.Lock()
	paths := make(map[string]pathMetric, len(r.paths))
	for k, v := range r.paths {
		paths[k] = v
	}
	samples := append([]int64(nil), r.latencySamples...)
	r.mu.Unlock()
	requests := r.requests.Load()
	latency := r.latency.Load()
	avg := 0.0
	if requests > 0 {
		avg = float64(latency) / float64(requests)
	}
	return Snapshot{Version: Version, StartedAt: r.started, Requests: requests, Errors: r.errors.Load(), Active: r.active.Load(), LatencyMS: latency, AvgLatencyMS: avg, P50LatencyMS: percentile(append([]int64(nil), samples...), 0.50), P95LatencyMS: percentile(samples, 0.95), Paths: paths}
}
func (r *Recorder) Recent(limit int) []Trace {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if limit > len(r.recent) {
		limit = len(r.recent)
	}
	out := append([]Trace(nil), r.recent[len(r.recent)-limit:]...)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (r *Recorder) Prometheus() string {
	s := r.Snapshot()
	var b strings.Builder
	fmt.Fprintf(&b, "# HELP omnibridge_requests_total Total HTTP requests.\n# TYPE omnibridge_requests_total counter\nomnibridge_requests_total %d\n", s.Requests)
	fmt.Fprintf(&b, "# HELP omnibridge_errors_total Total HTTP responses with status >=400.\n# TYPE omnibridge_errors_total counter\nomnibridge_errors_total %d\n", s.Errors)
	fmt.Fprintf(&b, "# TYPE omnibridge_active_requests gauge\nomnibridge_active_requests %d\n", s.Active)
	fmt.Fprintf(&b, "# TYPE omnibridge_request_latency_ms_total counter\nomnibridge_request_latency_ms_total %d\n", s.LatencyMS)
	fmt.Fprintf(&b, "# TYPE omnibridge_request_latency_ms_avg gauge\nomnibridge_request_latency_ms_avg %.3f\n", s.AvgLatencyMS)
	fmt.Fprintf(&b, "# TYPE omnibridge_request_latency_ms_p50 gauge\nomnibridge_request_latency_ms_p50 %d\n", s.P50LatencyMS)
	fmt.Fprintf(&b, "# TYPE omnibridge_request_latency_ms_p95 gauge\nomnibridge_request_latency_ms_p95 %d\n", s.P95LatencyMS)
	keys := make([]string, 0, len(s.Paths))
	for k := range s.Paths {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		p := s.Paths[k]
		safe := strings.ReplaceAll(strings.ReplaceAll(k, "\\", "_"), "\"", "_")
		fmt.Fprintf(&b, "omnibridge_path_requests_total{path=\"%s\"} %d\n", safe, p.Requests)
	}
	return b.String()
}
