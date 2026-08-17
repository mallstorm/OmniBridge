package router

import (
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// Candidate is the routing-only projection of a registry model. The router package
// deliberately does not import providers/stats/resilience so it stays reusable.
type Candidate struct {
	ID               string  `json:"id"`
	ProviderID       string  `json:"provider_id,omitempty"`
	NativeID         string  `json:"native_id,omitempty"`
	Source           string  `json:"source,omitempty"`
	Local            bool    `json:"local,omitempty"`
	AgentReady       bool    `json:"agent_ready,omitempty"`
	ToolCalling      bool    `json:"tool_calling,omitempty"`
	ParallelTools    bool    `json:"parallel_tools,omitempty"`
	StructuredOutput bool    `json:"structured_output,omitempty"`
	SystemPrompt     bool    `json:"system_prompt,omitempty"`
	Reasoning        bool    `json:"reasoning,omitempty"`
	Vision           bool    `json:"vision,omitempty"`
	Streaming        bool    `json:"streaming,omitempty"`
	ContextWindow    int     `json:"context_window,omitempty"`
	MaxOutputTokens  int     `json:"max_output_tokens,omitempty"`
	DoctorVerified   bool    `json:"doctor_verified,omitempty"`
	DoctorScore      float64 `json:"doctor_score,omitempty"`
	AgentScore       float64 `json:"agent_score,omitempty"`
	CodingScore      float64 `json:"coding_score,omitempty"`
	ReliabilityScore float64 `json:"doctor_reliability_score,omitempty"`
	HealthRank       int     `json:"health_rank"`
	Requests24h      int64   `json:"requests_24h"`
	SuccessRate      float64 `json:"success_rate,omitempty"`
	AverageLatencyMS float64 `json:"average_latency_ms,omitempty"`
	P95LatencyMS     int64   `json:"p95_latency_ms,omitempty"`
	QuotaRemaining   float64 `json:"quota_remaining,omitempty"`
	QuotaKnown       bool    `json:"quota_known,omitempty"`
	CostPerMTokens   float64 `json:"cost_per_m_tokens,omitempty"`
	CostKnown        bool    `json:"cost_known,omitempty"`
}

type Options struct {
	RouteKey          string
	Mode              string
	Profile           string
	SessionKey        string
	SessionStickiness bool
	SessionTTL        time.Duration
	Weights           map[string]float64
	Requirements      Requirements
}

type Score struct {
	CandidateID string             `json:"candidate_id"`
	Total       float64            `json:"total"`
	Components  map[string]float64 `json:"components"`
}

type binding struct {
	ModelID   string
	ExpiresAt time.Time
}

// Snapshot is safe to expose through the local admin API. It contains no prompt or secret data.
type Snapshot struct {
	Version          string            `json:"version"`
	StrategyCalls    map[string]uint64 `json:"strategy_calls"`
	AffinityBindings int               `json:"affinity_bindings"`
}

type Engine struct {
	mu              sync.Mutex
	seq             uint64
	calls           map[string]uint64
	positions       map[string]uint64
	weightedCurrent map[string]map[string]float64
	bindings        map[string]binding
}

func New() *Engine {
	return &Engine{calls: map[string]uint64{}, positions: map[string]uint64{}, weightedCurrent: map[string]map[string]float64{}, bindings: map[string]binding{}}
}

func normalizeMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return "priority"
	}
	return mode
}

func (e *Engine) next(mode, routeKey string) uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seq++
	e.calls[mode]++
	key := strings.TrimSpace(routeKey) + "|" + mode
	e.positions[key]++
	return e.positions[key]
}

func bindingKey(routeKey, sessionKey string) string {
	return strings.TrimSpace(routeKey) + "|" + strings.TrimSpace(sessionKey)
}

func (e *Engine) affinityModel(routeKey, sessionKey string, now time.Time) string {
	if strings.TrimSpace(sessionKey) == "" {
		return ""
	}
	key := bindingKey(routeKey, sessionKey)
	e.mu.Lock()
	defer e.mu.Unlock()
	b, ok := e.bindings[key]
	if !ok {
		return ""
	}
	if !b.ExpiresAt.IsZero() && !now.Before(b.ExpiresAt) {
		delete(e.bindings, key)
		return ""
	}
	return b.ModelID
}

func (e *Engine) Pin(routeKey, sessionKey, modelID string, ttl time.Duration) {
	if strings.TrimSpace(sessionKey) == "" || strings.TrimSpace(modelID) == "" {
		return
	}
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	e.mu.Lock()
	e.bindings[bindingKey(routeKey, sessionKey)] = binding{ModelID: modelID, ExpiresAt: time.Now().Add(ttl)}
	e.mu.Unlock()
}

func (e *Engine) Release(routeKey, sessionKey, failedModelID string) {
	if strings.TrimSpace(sessionKey) == "" {
		return
	}
	key := bindingKey(routeKey, sessionKey)
	e.mu.Lock()
	if b, ok := e.bindings[key]; ok && (failedModelID == "" || b.ModelID == failedModelID) {
		delete(e.bindings, key)
	}
	e.mu.Unlock()
}

func (e *Engine) Snapshot() Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	for k, b := range e.bindings {
		if !b.ExpiresAt.IsZero() && !now.Before(b.ExpiresAt) {
			delete(e.bindings, k)
		}
	}
	calls := make(map[string]uint64, len(e.calls))
	for k, v := range e.calls {
		calls[k] = v
	}
	return Snapshot{Version: "3.1", StrategyCalls: calls, AffinityBindings: len(e.bindings)}
}

func effectiveLatency(c Candidate) float64 {
	if c.P95LatencyMS > 0 {
		return float64(c.P95LatencyMS)
	}
	if c.AverageLatencyMS > 0 {
		return c.AverageLatencyMS
	}
	return 1e12
}

func loadValue(c Candidate) float64 {
	latency := effectiveLatency(c)
	if latency >= 1e11 {
		latency = 2000
	}
	return float64(c.Requests24h) + float64(c.HealthRank)*100000 + latency/100
}

func hintValue(m map[string]float64, c Candidate) (float64, bool) {
	if len(m) == 0 {
		return 0, false
	}
	for _, k := range []string{c.ID, c.NativeID, c.ProviderID} {
		if k == "" {
			continue
		}
		if v, ok := m[k]; ok {
			return v, true
		}
	}
	return 0, false
}

func weightFor(weights map[string]float64, c Candidate) float64 {
	if v, ok := hintValue(weights, c); ok && v > 0 {
		return v
	}
	return 1
}

func rotate(in []Candidate, start int) []Candidate {
	if len(in) < 2 {
		return append([]Candidate(nil), in...)
	}
	start %= len(in)
	out := make([]Candidate, 0, len(in))
	out = append(out, in[start:]...)
	out = append(out, in[:start]...)
	return out
}

func weightedFirst(in []Candidate, weights map[string]float64, seq uint64) []Candidate {
	if len(in) < 2 {
		return append([]Candidate(nil), in...)
	}
	total := 0
	units := make([]int, len(in))
	for i, c := range in {
		w := weightFor(weights, c)
		u := int(math.Round(w * 100))
		if u < 1 {
			u = 1
		}
		if u > 1000000 {
			u = 1000000
		}
		units[i] = u
		total += u
	}
	if total <= 0 {
		return append([]Candidate(nil), in...)
	}
	pick := int((seq - 1) % uint64(total))
	selected := 0
	for i, u := range units {
		if pick < u {
			selected = i
			break
		}
		pick -= u
	}
	out := []Candidate{in[selected]}
	for i, c := range in {
		if i != selected {
			out = append(out, c)
		}
	}
	return out
}

func (e *Engine) smoothWeightedFirst(in []Candidate, weights map[string]float64, routeKey string) []Candidate {
	if len(in) < 2 {
		return append([]Candidate(nil), in...)
	}
	key := strings.TrimSpace(routeKey)
	e.mu.Lock()
	state := e.weightedCurrent[key]
	if state == nil {
		state = map[string]float64{}
		e.weightedCurrent[key] = state
	}
	total := 0.0
	selected := 0
	best := math.Inf(-1)
	alive := make(map[string]bool, len(in))
	for i, c := range in {
		alive[c.ID] = true
		w := weightFor(weights, c)
		state[c.ID] += w
		total += w
		if state[c.ID] > best {
			best = state[c.ID]
			selected = i
		}
	}
	for id := range state {
		if !alive[id] {
			delete(state, id)
		}
	}
	state[in[selected].ID] -= total
	e.mu.Unlock()
	out := []Candidate{in[selected]}
	for i, c := range in {
		if i != selected {
			out = append(out, c)
		}
	}
	return out
}

func p2c(in []Candidate, seq uint64) []Candidate {
	if len(in) < 2 {
		return append([]Candidate(nil), in...)
	}
	a := int((seq - 1) % uint64(len(in)))
	b := int((seq*11400714819323198485 + 1) % uint64(len(in)))
	if b == a {
		b = (b + 1) % len(in)
	}
	winner := a
	if loadValue(in[b]) < loadValue(in[a]) {
		winner = b
	}
	out := []Candidate{in[winner]}
	rest := make([]Candidate, 0, len(in)-1)
	for i, c := range in {
		if i != winner {
			rest = append(rest, c)
		}
	}
	sort.SliceStable(rest, func(i, j int) bool { return loadValue(rest[i]) < loadValue(rest[j]) })
	return append(out, rest...)
}

func fastest(in []Candidate) []Candidate {
	out := append([]Candidate(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].HealthRank != out[j].HealthRank {
			return out[i].HealthRank < out[j].HealthRank
		}
		li, lj := effectiveLatency(out[i]), effectiveLatency(out[j])
		if li != lj {
			return li < lj
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func leastUsed(in []Candidate) []Candidate {
	out := append([]Candidate(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].HealthRank != out[j].HealthRank {
			return out[i].HealthRank < out[j].HealthRank
		}
		if out[i].Requests24h != out[j].Requests24h {
			return out[i].Requests24h < out[j].Requests24h
		}
		return effectiveLatency(out[i]) < effectiveLatency(out[j])
	})
	return out
}

func quotaOptimized(in []Candidate) []Candidate {
	out := append([]Candidate(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].HealthRank != out[j].HealthRank {
			return out[i].HealthRank < out[j].HealthRank
		}
		if out[i].QuotaKnown != out[j].QuotaKnown {
			return out[i].QuotaKnown
		}
		if out[i].QuotaKnown && out[i].QuotaRemaining != out[j].QuotaRemaining {
			return out[i].QuotaRemaining > out[j].QuotaRemaining
		}
		return loadValue(out[i]) < loadValue(out[j])
	})
	return out
}

func costOptimized(in []Candidate) []Candidate {
	out := append([]Candidate(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].HealthRank != out[j].HealthRank {
			return out[i].HealthRank < out[j].HealthRank
		}
		if out[i].CostKnown != out[j].CostKnown {
			return out[i].CostKnown
		}
		if out[i].CostKnown && out[i].CostPerMTokens != out[j].CostPerMTokens {
			return out[i].CostPerMTokens < out[j].CostPerMTokens
		}
		return loadValue(out[i]) < loadValue(out[j])
	})
	return out
}

func profileBonus(c Candidate, profile string) float64 {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "coding":
		bonus := 0.0
		if c.AgentReady {
			bonus += 28
		}
		if c.ToolCalling {
			bonus += 15
		}
		if c.Streaming {
			bonus += 8
		}
		return bonus
	case "agent":
		if c.AgentReady {
			return 45
		}
		return -40
	case "reasoning":
		if c.Reasoning {
			return 38
		}
		return -35
	case "vision":
		if c.Vision {
			return 38
		}
		return -35
	case "local":
		if c.Local {
			return 50
		}
		return -60
	}
	return 0
}

func ScoreCandidate(c Candidate, profile string) Score {
	return ScoreCandidateForRequest(c, profile, Requirements{})
}

func capabilityFitScore(c Candidate, r Requirements) float64 {
	score := 0.0
	checks := []struct{ required, supported bool }{
		{r.Streaming, c.Streaming},
		{r.SystemPrompt, c.SystemPrompt},
		{r.ToolCalling, c.ToolCalling},
		{r.ParallelTools, c.ParallelTools},
		{r.StructuredOutput, c.StructuredOutput},
		{r.Vision, c.Vision},
		{r.Reasoning, c.Reasoning},
		{r.AgentMode, c.AgentReady},
	}
	for _, x := range checks {
		if !x.required {
			continue
		}
		if x.supported {
			score += 7
		} else {
			score -= 35
		}
	}
	return score
}

func contextFitScore(c Candidate, r Requirements) float64 {
	if r.MinimumContext <= 0 || c.ContextWindow <= 0 {
		return 0
	}
	if r.MinimumContext > c.ContextWindow {
		return -80
	}
	headroom := float64(c.ContextWindow-r.MinimumContext) / float64(c.ContextWindow)
	if headroom > 0.75 {
		headroom = 0.75
	}
	return 4 + headroom*12
}

func ScoreCandidateForRequest(c Candidate, profile string, req Requirements) Score {
	components := map[string]float64{}
	health := 30.0 - float64(c.HealthRank)*12
	if health < -30 {
		health = -30
	}
	components["health"] = health

	runtimeReliability := 12.0
	if c.Requests24h > 0 {
		runtimeReliability = (c.SuccessRate / 100) * 24
	}
	components["runtime_reliability"] = runtimeReliability

	lat := effectiveLatency(c)
	latencyScore := 4.0
	if lat < 1e11 {
		latencyScore = 22 / (1 + lat/1800)
	}
	if strings.EqualFold(profile, "fast") {
		latencyScore *= 1.8
	}
	components["latency"] = latencyScore

	usage := 10 / (1 + math.Log10(float64(c.Requests24h)+1))
	components["load"] = usage

	quota := 0.0
	if c.QuotaKnown {
		quota = math.Max(0, math.Min(100, c.QuotaRemaining)) * 0.18
	}
	components["quota"] = quota

	cost := 0.0
	if c.CostKnown {
		cost = 16 / (1 + math.Max(0, c.CostPerMTokens))
		if strings.EqualFold(profile, "cheap") {
			cost *= 2
		}
	}
	components["cost"] = cost

	components["profile"] = profileBonus(c, profile)
	components["capability_fit"] = capabilityFitScore(c, req)
	components["context_fit"] = contextFitScore(c, req)

	doctor := 0.0
	if c.DoctorVerified {
		doctor = c.DoctorScore * 0.22
	} else if req.RequiresDoctorProof {
		doctor = -18
	}
	components["doctor"] = doctor

	docRel := 0.0
	if c.DoctorVerified {
		docRel = c.ReliabilityScore * 0.12
	}
	components["doctor_reliability"] = docRel

	agent := 0.0
	if strings.EqualFold(profile, "agent") || req.AgentMode {
		agent = c.AgentScore * 0.20
	}
	components["agent_score"] = agent

	coding := 0.0
	if strings.EqualFold(profile, "coding") {
		coding = c.CodingScore * 0.18
	}
	components["coding_score"] = coding

	total := 0.0
	for _, v := range components {
		total += v
	}
	return Score{CandidateID: c.ID, Total: total, Components: components}
}

func autoOrder(in []Candidate, profile string, req Requirements) []Candidate {
	out := append([]Candidate(nil), in...)
	scores := make(map[string]float64, len(out))
	for _, c := range out {
		scores[c.ID] = ScoreCandidateForRequest(c, profile, req).Total
	}
	sort.SliceStable(out, func(i, j int) bool {
		si, sj := scores[out[i].ID], scores[out[j].ID]
		if si != sj {
			return si > sj
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Order returns candidates in attempt order. Resilience filtering is intentionally
// performed by the caller before this method; this engine only ranks usable candidates.
func (e *Engine) Order(in []Candidate, opts Options) []Candidate {
	if len(in) == 0 {
		return nil
	}
	mode := normalizeMode(opts.Mode)
	seq := e.next(mode, opts.RouteKey)
	var out []Candidate
	switch mode {
	case "fastest":
		out = fastest(in)
	case "round_robin":
		out = rotate(in, int((seq-1)%uint64(len(in))))
	case "least_used":
		out = leastUsed(in)
	case "weighted":
		out = e.smoothWeightedFirst(in, opts.Weights, opts.RouteKey)
	case "p2c":
		out = p2c(in, seq)
	case "quota_optimized":
		out = quotaOptimized(in)
	case "cost_optimized":
		out = costOptimized(in)
	case "auto":
		out = autoOrder(in, opts.Profile, opts.Requirements)
	case "direct":
		out = append([]Candidate(nil), in[0])
	default:
		out = append([]Candidate(nil), in...)
	}

	if opts.SessionStickiness && strings.TrimSpace(opts.SessionKey) != "" {
		pinned := e.affinityModel(opts.RouteKey, opts.SessionKey, time.Now())
		if pinned != "" {
			for i, c := range out {
				if c.ID == pinned && c.HealthRank == 0 {
					if i > 0 {
						out = append([]Candidate{c}, append(out[:i], out[i+1:]...)...)
					}
					break
				}
			}
		}
	}
	return out
}
