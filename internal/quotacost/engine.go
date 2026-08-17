package quotacost

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"omnibridge/internal/config"
	"omnibridge/internal/storage"
)

const Version = "1.0"

type Price struct {
	Provider            string    `json:"provider"`
	Model               string    `json:"model"`
	InputUSDPerMillion  float64   `json:"input_usd_per_million,omitempty"`
	OutputUSDPerMillion float64   `json:"output_usd_per_million,omitempty"`
	CachedUSDPerMillion float64   `json:"cached_input_usd_per_million,omitempty"`
	Currency            string    `json:"currency"`
	Source              string    `json:"source,omitempty"`
	Confidence          string    `json:"confidence"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type Snapshot struct {
	ConnectionID     string            `json:"connection_id"`
	Confidence       string            `json:"confidence"`
	Source           string            `json:"source,omitempty"`
	Remaining        float64           `json:"remaining,omitempty"`
	Limit            float64           `json:"limit,omitempty"`
	RemainingPercent float64           `json:"remaining_percent,omitempty"`
	RemainingKnown   bool              `json:"remaining_known"`
	ResetAt          time.Time         `json:"reset_at,omitempty"`
	RequestRemaining float64           `json:"request_remaining,omitempty"`
	RequestLimit     float64           `json:"request_limit,omitempty"`
	TokenRemaining   float64           `json:"token_remaining,omitempty"`
	TokenLimit       float64           `json:"token_limit,omitempty"`
	BalanceUSD       float64           `json:"balance_usd,omitempty"`
	BalanceKnown     bool              `json:"balance_known"`
	LowQuota         bool              `json:"low_quota"`
	UpdatedAt        time.Time         `json:"updated_at"`
	Raw              map[string]string `json:"raw,omitempty"`
}

type Estimate struct {
	Known       bool    `json:"known"`
	CostUSD     float64 `json:"cost_usd,omitempty"`
	Confidence  string  `json:"confidence"`
	PriceSource string  `json:"price_source,omitempty"`
}

type BudgetStatus struct {
	SessionSpentUSD float64 `json:"session_spent_usd"`
	DailySpentUSD   float64 `json:"daily_spent_usd"`
	MonthlySpentUSD float64 `json:"monthly_spent_usd"`
	SessionLimitUSD float64 `json:"session_limit_usd,omitempty"`
	DailyLimitUSD   float64 `json:"daily_limit_usd,omitempty"`
	MonthlyLimitUSD float64 `json:"monthly_limit_usd,omitempty"`
	Exceeded        bool    `json:"exceeded"`
	Reason          string  `json:"reason,omitempty"`
}

type Engine struct {
	mu             sync.RWMutex
	db             *storage.DB
	cfg            config.QuotaCostConfig
	prices         map[string]Price
	quotas         map[string]Snapshot
	sessionStarted time.Time
}

func New(db *storage.DB, cfg config.QuotaCostConfig) *Engine {
	e := &Engine{db: db, cfg: cfg, prices: map[string]Price{}, quotas: map[string]Snapshot{}, sessionStarted: time.Now().UTC()}
	e.load()
	return e
}

func priceKey(provider, model string) string {
	return strings.ToLower(strings.TrimSpace(provider)) + "\x00" + strings.ToLower(strings.TrimSpace(model))
}

func normalizeConfidence(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "verified", "estimated", "operator":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return "unknown"
	}
}

func (e *Engine) load() {
	if e.db == nil {
		return
	}
	if rows, err := e.db.LoadPricing(); err == nil {
		for _, row := range rows {
			var p Price
			if json.Unmarshal(row.Payload, &p) == nil {
				if p.Provider == "" {
					p.Provider = row.Provider
				}
				if p.Model == "" {
					p.Model = row.Model
				}
				if p.Source == "" {
					p.Source = row.Source
				}
				if p.Confidence == "" {
					p.Confidence = row.Confidence
				}
				if p.UpdatedAt.IsZero() {
					p.UpdatedAt = row.UpdatedAt
				}
				e.prices[priceKey(p.Provider, p.Model)] = p
			}
		}
	}
	if rows, err := e.db.LoadQuotaSnapshots(); err == nil {
		for _, row := range rows {
			var q Snapshot
			if json.Unmarshal(row.Payload, &q) == nil {
				if q.ConnectionID == "" {
					q.ConnectionID = row.ConnectionID
				}
				if q.Confidence == "" {
					q.Confidence = row.Confidence
				}
				if q.UpdatedAt.IsZero() {
					q.UpdatedAt = row.At
				}
				e.quotas[q.ConnectionID] = q
			}
		}
	}
}

func (e *Engine) UpdateConfig(cfg config.QuotaCostConfig) { e.mu.Lock(); e.cfg = cfg; e.mu.Unlock() }

func (e *Engine) UpsertPrice(p Price) error {
	p.Provider = strings.TrimSpace(p.Provider)
	p.Model = strings.TrimSpace(p.Model)
	if p.Provider == "" || p.Model == "" {
		return errors.New("provider and model are required")
	}
	if p.InputUSDPerMillion < 0 || p.OutputUSDPerMillion < 0 || p.CachedUSDPerMillion < 0 {
		return errors.New("pricing values must be non-negative")
	}
	p.Currency = strings.ToUpper(strings.TrimSpace(p.Currency))
	if p.Currency == "" {
		p.Currency = "USD"
	}
	if p.Currency != "USD" {
		return errors.New("only USD pricing is supported in this engine version")
	}
	p.Confidence = normalizeConfidence(p.Confidence)
	if p.Confidence == "unknown" {
		p.Confidence = "operator"
	}
	if p.Source == "" {
		p.Source = "operator"
	}
	p.UpdatedAt = time.Now().UTC()
	b, _ := json.Marshal(p)
	if e.db != nil {
		if err := e.db.SavePricing(p.Provider, p.Model, p.Source, p.Confidence, p.UpdatedAt, b); err != nil {
			return err
		}
	}
	e.mu.Lock()
	e.prices[priceKey(p.Provider, p.Model)] = p
	e.mu.Unlock()
	return nil
}

func (e *Engine) SetQuota(q Snapshot) error {
	q.ConnectionID = strings.TrimSpace(q.ConnectionID)
	if q.ConnectionID == "" {
		return errors.New("connection_id is required")
	}
	q.Confidence = normalizeConfidence(q.Confidence)
	if q.Confidence == "unknown" {
		q.Confidence = "operator"
	}
	if q.Source == "" {
		q.Source = "operator"
	}
	q.UpdatedAt = time.Now().UTC()
	e.mu.RLock()
	threshold := e.cfg.LowQuotaPercent
	e.mu.RUnlock()
	if q.RemainingKnown {
		q.LowQuota = q.RemainingPercent <= threshold
	}
	b, _ := json.Marshal(q)
	if e.db != nil {
		if err := e.db.SaveQuotaSnapshot(q.ConnectionID, q.Confidence, q.UpdatedAt, b); err != nil {
			return err
		}
	}
	e.mu.Lock()
	e.quotas[q.ConnectionID] = q
	e.mu.Unlock()
	return nil
}

func parseFloatHeader(h http.Header, names ...string) (float64, bool) {
	for _, name := range names {
		v := strings.TrimSpace(h.Get(name))
		if v == "" {
			continue
		}
		// Some providers expose "123;w=60". Keep the numeric prefix only.
		if i := strings.IndexAny(v, ";,"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			return n, true
		}
	}
	return 0, false
}

func parseReset(h http.Header) time.Time {
	for _, name := range []string{"X-RateLimit-Reset", "RateLimit-Reset", "X-Ratelimit-Reset-Requests", "X-RateLimit-Reset-Tokens"} {
		v := strings.TrimSpace(h.Get(name))
		if v == "" {
			continue
		}
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			if n > 1e12 {
				return time.UnixMilli(n).UTC()
			}
			if n > 1e9 {
				return time.Unix(n, 0).UTC()
			}
			return time.Now().UTC().Add(time.Duration(n) * time.Second)
		}
		if t, err := http.ParseTime(v); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func (e *Engine) ObserveHeaders(connectionID string, h http.Header) {
	if h == nil || strings.TrimSpace(connectionID) == "" {
		return
	}
	reqRem, reqRemOK := parseFloatHeader(h, "X-RateLimit-Remaining-Requests", "RateLimit-Remaining", "X-Ratelimit-Remaining")
	reqLim, reqLimOK := parseFloatHeader(h, "X-RateLimit-Limit-Requests", "RateLimit-Limit", "X-Ratelimit-Limit")
	tokRem, tokRemOK := parseFloatHeader(h, "X-RateLimit-Remaining-Tokens")
	tokLim, tokLimOK := parseFloatHeader(h, "X-RateLimit-Limit-Tokens")
	if !reqRemOK && !tokRemOK {
		return
	}
	q := Snapshot{ConnectionID: connectionID, Confidence: "verified", Source: "upstream_headers", RequestRemaining: reqRem, RequestLimit: reqLim, TokenRemaining: tokRem, TokenLimit: tokLim, ResetAt: parseReset(h), Raw: map[string]string{}}
	var percent float64
	var have bool
	if reqRemOK && reqLimOK && reqLim > 0 {
		percent = 100 * reqRem / reqLim
		have = true
	}
	if tokRemOK && tokLimOK && tokLim > 0 {
		p := 100 * tokRem / tokLim
		if !have || p < percent {
			percent = p
			have = true
		}
	}
	q.RemainingKnown = have
	if have {
		q.RemainingPercent = percent
	}
	for _, name := range []string{"X-RateLimit-Remaining-Requests", "X-RateLimit-Limit-Requests", "X-RateLimit-Remaining-Tokens", "X-RateLimit-Limit-Tokens", "X-RateLimit-Reset"} {
		if v := h.Get(name); v != "" {
			q.Raw[name] = v
		}
	}
	_ = e.SetQuota(q)
}

func (e *Engine) Price(provider, model string) (Price, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if p, ok := e.prices[priceKey(provider, model)]; ok {
		return p, true
	}
	p, ok := e.prices[priceKey(provider, "*")]
	return p, ok
}

func (e *Engine) Quota(connectionID string) (Snapshot, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	q, ok := e.quotas[connectionID]
	return q, ok
}

func (e *Engine) CandidateHints(connectionID, provider, model string) (quota float64, quotaKnown bool, cost float64, costKnown bool) {
	if q, ok := e.Quota(connectionID); ok && q.RemainingKnown {
		quota, quotaKnown = q.RemainingPercent, true
	}
	if p, ok := e.Price(provider, model); ok {
		// Router compares one representative unit: 1M input + 1M output tokens.
		cost, costKnown = p.InputUSDPerMillion+p.OutputUSDPerMillion, true
	}
	return
}

func (e *Engine) Estimate(provider, model string, inputTokens, outputTokens, cachedInputTokens int64) Estimate {
	p, ok := e.Price(provider, model)
	if !ok {
		return Estimate{Known: false, Confidence: "unknown"}
	}
	normalInput := inputTokens - cachedInputTokens
	if normalInput < 0 {
		normalInput = 0
	}
	cost := float64(normalInput)/1e6*p.InputUSDPerMillion + float64(outputTokens)/1e6*p.OutputUSDPerMillion + float64(cachedInputTokens)/1e6*p.CachedUSDPerMillion
	return Estimate{Known: true, CostUSD: cost, Confidence: p.Confidence, PriceSource: p.Source}
}

func (e *Engine) RecordCost(eventID, provider, model, connectionID string, inputTokens, outputTokens, cachedInputTokens int64) Estimate {
	est := e.Estimate(provider, model, inputTokens, outputTokens, cachedInputTokens)
	if est.Known && e.db != nil {
		_ = e.db.AppendCostEvent(eventID, time.Now().UTC(), provider, model, connectionID, inputTokens, outputTokens, est.CostUSD, est.Confidence)
	}
	return est
}

func (e *Engine) BudgetStatus() BudgetStatus {
	e.mu.RLock()
	cfg := e.cfg
	session := e.sessionStarted
	e.mu.RUnlock()
	out := BudgetStatus{SessionLimitUSD: cfg.Budget.SessionUSD, DailyLimitUSD: cfg.Budget.DailyUSD, MonthlyLimitUSD: cfg.Budget.MonthlyUSD}
	if e.db == nil {
		return out
	}
	out.SessionSpentUSD, _ = e.db.CostSince(session)
	now := time.Now().UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	out.DailySpentUSD, _ = e.db.CostSince(day)
	out.MonthlySpentUSD, _ = e.db.CostSince(month)
	switch {
	case cfg.Budget.SessionUSD > 0 && out.SessionSpentUSD >= cfg.Budget.SessionUSD:
		out.Exceeded = true
		out.Reason = "session_budget"
	case cfg.Budget.DailyUSD > 0 && out.DailySpentUSD >= cfg.Budget.DailyUSD:
		out.Exceeded = true
		out.Reason = "daily_budget"
	case cfg.Budget.MonthlyUSD > 0 && out.MonthlySpentUSD >= cfg.Budget.MonthlyUSD:
		out.Exceeded = true
		out.Reason = "monthly_budget"
	}
	return out
}

func (e *Engine) ShouldRejectForBudget() (bool, string) {
	e.mu.RLock()
	reject := e.cfg.RejectOnBudget
	enabled := e.cfg.Enabled
	e.mu.RUnlock()
	if !enabled || !reject {
		return false, ""
	}
	b := e.BudgetStatus()
	return b.Exceeded, b.Reason
}

func (e *Engine) Summary() map[string]any {
	e.mu.RLock()
	prices := make([]Price, 0, len(e.prices))
	for _, p := range e.prices {
		prices = append(prices, p)
	}
	quotas := make([]Snapshot, 0, len(e.quotas))
	for _, q := range e.quotas {
		quotas = append(quotas, q)
	}
	cfg := e.cfg
	e.mu.RUnlock()
	return map[string]any{"version": Version, "config": cfg, "prices": prices, "quota": quotas, "budget": e.BudgetStatus()}
}

func (e *Engine) String() string {
	s := e.Summary()
	b, _ := json.Marshal(s)
	return fmt.Sprint(string(b))
}

func numberAny(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, e := x.Float64()
		return f, e == nil
	case string:
		f, e := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, e == nil
	}
	return 0, false
}
func mapNumber(m map[string]any, keys ...string) (float64, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if n, ok := numberAny(v); ok {
				return n, true
			}
		}
	}
	return 0, false
}
func parseAnyTime(v any) time.Time {
	if n, ok := numberAny(v); ok {
		if n > 1e12 {
			return time.UnixMilli(int64(n)).UTC()
		}
		if n > 1e9 {
			return time.Unix(int64(n), 0).UTC()
		}
	}
	if s, ok := v.(string); ok {
		if t, e := time.Parse(time.RFC3339, s); e == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// ObserveAccountUsage extracts only explicit rate-limit window values from an
// official account connector payload. Unknown shapes stay unknown; the engine
// never invents a quota from subscription names or local token usage.
func (e *Engine) ObserveAccountUsage(connectionID string, details map[string]any) bool {
	best := 101.0
	have := false
	var reset time.Time
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if p, ok := mapNumber(x, "remaining_percent", "remainingPercent", "remaining_percentage"); ok && p >= 0 && p <= 100 {
				if p < best {
					best = p
				}
				have = true
			}
			if u, ok := mapNumber(x, "used_percent", "usedPercent", "used_percentage"); ok && u >= 0 && u <= 100 {
				p := 100 - u
				if p < best {
					best = p
				}
				have = true
			}
			rem, rok := mapNumber(x, "remaining", "remaining_requests", "remainingRequests")
			lim, lok := mapNumber(x, "limit", "request_limit", "requestLimit")
			if rok && lok && lim > 0 && rem >= 0 {
				p := 100 * rem / lim
				if p >= 0 && p <= 100 {
					if p < best {
						best = p
					}
					have = true
				}
			}
			for _, k := range []string{"reset_at", "resetAt", "resets_at", "resetsAt"} {
				if t := parseAnyTime(x[k]); !t.IsZero() && (reset.IsZero() || t.Before(reset)) {
					reset = t
				}
			}
			for _, v2 := range x {
				walk(v2)
			}
		case []any:
			for _, v2 := range x {
				walk(v2)
			}
		}
	}
	walk(details)
	if !have {
		return false
	}
	_ = e.SetQuota(Snapshot{ConnectionID: connectionID, Confidence: "verified", Source: "official_account_usage", RemainingKnown: true, RemainingPercent: best, ResetAt: reset})
	return true
}
