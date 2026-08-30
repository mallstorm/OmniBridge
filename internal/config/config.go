package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"omnibridge/internal/storage"
	"time"
)

const (
	CurrentVersion       = "1.0.0-rc8"
	CurrentSchemaVersion = 6
)

type RoutingPolicy struct {
	Mode       string             `json:"mode"`
	Candidates []string           `json:"candidates,omitempty"`
	Weights    map[string]float64 `json:"weights,omitempty"`
	// QuotaHints and CostHints are explicit operator hints used until the
	// dedicated Quota/Cost Engine supplies verified runtime values in stage 9.
	QuotaHints map[string]float64 `json:"quota_hints,omitempty"`
	CostHints  map[string]float64 `json:"cost_hints,omitempty"`
}

type RoutingConfig struct {
	Aliases                       map[string]string        `json:"aliases,omitempty"`
	Priorities                    map[string][]string      `json:"priorities,omitempty"`
	Fallbacks                     map[string][]string      `json:"fallbacks,omitempty"`
	Policies                      map[string]RoutingPolicy `json:"policies,omitempty"`
	MaxRetries                    int                      `json:"max_retries,omitempty"`
	CircuitBreakerThreshold       int                      `json:"circuit_breaker_threshold,omitempty"`
	CircuitBreakerCooldownSeconds int                      `json:"circuit_breaker_cooldown_seconds,omitempty"`
	SessionStickiness             bool                     `json:"session_stickiness"`
	SessionTTLSeconds             int                      `json:"session_ttl_seconds,omitempty"`
}

type BudgetConfig struct {
	SessionUSD float64 `json:"session_usd,omitempty"`
	DailyUSD   float64 `json:"daily_usd,omitempty"`
	MonthlyUSD float64 `json:"monthly_usd,omitempty"`
}

type QuotaCostConfig struct {
	Enabled             bool         `json:"enabled"`
	LowQuotaPercent     float64      `json:"low_quota_percent,omitempty"`
	RejectOnBudget      bool         `json:"reject_on_budget,omitempty"`
	Budget              BudgetConfig `json:"budget,omitempty"`
	PricingRefreshHours int          `json:"pricing_refresh_hours,omitempty"`
}

type ContextGuardConfig struct {
	Enabled             bool    `json:"enabled"`
	Mode                string  `json:"mode,omitempty"`
	MaxContextPercent   float64 `json:"max_context_percent,omitempty"`
	ReserveOutputTokens int     `json:"reserve_output_tokens,omitempty"`
	MinRecentMessages   int     `json:"min_recent_messages,omitempty"`
	MaxRequestBytes     int     `json:"max_request_bytes,omitempty"`
}

// AdaptiveConfig controls only soft routing feedback. Circuit breakers,
// authentication, quota and Doctor checks remain authoritative hard gates.
type AdaptiveConfig struct {
	Enabled            bool    `json:"enabled"`
	SuccessAlpha       float64 `json:"success_alpha,omitempty"`
	LatencyAlpha       float64 `json:"latency_alpha,omitempty"`
	FullConfidenceAt   int     `json:"full_confidence_at,omitempty"`
	QualityWeight      float64 `json:"quality_weight,omitempty"`
	EventQueueSize     int     `json:"event_queue_size,omitempty"`
	DecisionTTLMinutes int     `json:"decision_ttl_minutes,omitempty"`
	MaxDecisions       int     `json:"max_decisions,omitempty"`
}

type ExactCacheConfig struct {
	Enabled       bool `json:"enabled"`
	TTLSeconds    int  `json:"ttl_seconds,omitempty"`
	MaxEntries    int  `json:"max_entries,omitempty"`
	MaxEntryBytes int  `json:"max_entry_bytes,omitempty"`
	Deduplicate   bool `json:"deduplicate"`
}

type CatalogSyncConfig struct {
	Enabled      bool   `json:"enabled"`
	URL          string `json:"url,omitempty"`
	RefreshHours int    `json:"refresh_hours,omitempty"`
}

type RemoteConfig struct {
	Enabled       bool     `json:"enabled"`
	TLSCertPath   string   `json:"tls_cert_path,omitempty"`
	TLSKeyPath    string   `json:"tls_key_path,omitempty"`
	AllowedCIDRs  []string `json:"allowed_cidrs,omitempty"`
	MetricsPublic bool     `json:"metrics_public,omitempty"`
}

type Provider struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Kind              string            `json:"kind"`
	APIType           string            `json:"api_type"` // legacy alias for preferred_protocol
	BaseURL           string            `json:"base_url,omitempty"`
	ModelsURL         string            `json:"models_url,omitempty"`
	ProjectID         string            `json:"project_id,omitempty"`
	AuthType          string            `json:"auth_type,omitempty"`
	CredentialRef     string            `json:"credential_ref,omitempty"`
	Scope             string            `json:"scope,omitempty"`
	Enabled           bool              `json:"enabled"`
	Headers           map[string]string `json:"headers,omitempty"`
	Protocols         []string          `json:"protocols,omitempty"`
	PreferredProtocol string            `json:"preferred_protocol,omitempty"`
	ProtocolBaseURLs  map[string]string `json:"protocol_base_urls,omitempty"`
	ProtocolAuthTypes map[string]string `json:"protocol_auth_types,omitempty"`
	Quirks            map[string]string `json:"quirks,omitempty"`
}

type AccountConnection struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Kind            string            `json:"kind"`
	Enabled         bool              `json:"enabled"`
	Settings        map[string]string `json:"settings,omitempty"`
	Status          string            `json:"status,omitempty"`
	Email           string            `json:"email,omitempty"`
	Plan            string            `json:"plan,omitempty"`
	LastError       string            `json:"last_error,omitempty"`
	LastErrorKind   string            `json:"last_error_kind,omitempty"`
	LastChecked     time.Time         `json:"last_checked,omitempty"`
	LastConnectedAt time.Time         `json:"last_connected_at,omitempty"`
	RefreshFailures int               `json:"refresh_failures,omitempty"`
}

type Config struct {
	SchemaVersion int                 `json:"schema_version"`
	Version       string              `json:"version"`
	Listen        string              `json:"listen"`
	DataAPIKey    string              `json:"data_api_key,omitempty"`
	AdminAPIKey   string              `json:"admin_api_key,omitempty"`
	Providers     []Provider          `json:"providers"`
	Accounts      []AccountConnection `json:"accounts,omitempty"`
	Routing       RoutingConfig       `json:"routing,omitempty"`
	QuotaCost     QuotaCostConfig     `json:"quota_cost,omitempty"`
	ContextGuard  ContextGuardConfig  `json:"context_guard,omitempty"`
	Adaptive      AdaptiveConfig      `json:"adaptive_routing,omitempty"`
	ExactCache    ExactCacheConfig    `json:"exact_response_cache,omitempty"`
	CatalogSync   CatalogSyncConfig   `json:"catalog_sync,omitempty"`
	Remote        RemoteConfig        `json:"remote,omitempty"`
}

type Store struct {
	path string
	mu   sync.RWMutex
	cfg  Config
	db   *storage.DB
}

const (
	BridgeDataKeyRef  = "bridge:data-api-key"
	BridgeAdminKeyRef = "bridge:admin-api-key"
)

type SecretStore interface {
	Get(string) (string, error)
	Put(string, string) error
}

func New(path string) (*Store, error) {
	return newStore(path, nil)
}

func NewPersistent(path string, db *storage.DB) (*Store, error) {
	return newStore(path, db)
}

func newStore(path string, db *storage.DB) (*Store, error) {
	s := &Store{path: path, db: db}
	if err := s.Load(); err != nil {
		return nil, err
	}
	return s, nil
}

func defaultConfig() Config {
	return Config{
		SchemaVersion: CurrentSchemaVersion,
		Version:       CurrentVersion,
		Listen:        "127.0.0.1:4142",
		DataAPIKey:    randomToken(32),
		AdminAPIKey:   randomToken(32),
		Providers:     []Provider{},
		Accounts:      []AccountConnection{},
		QuotaCost:     QuotaCostConfig{Enabled: true, LowQuotaPercent: 10, PricingRefreshHours: 24},
		ContextGuard:  ContextGuardConfig{Enabled: true, Mode: "safe", MaxContextPercent: 90, ReserveOutputTokens: 2048, MinRecentMessages: 8, MaxRequestBytes: 32 << 20},
		Adaptive:      AdaptiveConfig{Enabled: true, SuccessAlpha: 0.2, LatencyAlpha: 0.1, FullConfidenceAt: 50, QualityWeight: 32, EventQueueSize: 512, DecisionTTLMinutes: 60, MaxDecisions: 500},
		ExactCache:    ExactCacheConfig{Enabled: false, TTLSeconds: 120, MaxEntries: 256, MaxEntryBytes: 2 << 20, Deduplicate: true},
		CatalogSync:   CatalogSyncConfig{Enabled: false, URL: "https://models.dev/api.json", RefreshHours: 24},
		Remote:        RemoteConfig{Enabled: false, AllowedCIDRs: []string{}},
		Routing: RoutingConfig{
			Aliases:                       map[string]string{},
			Priorities:                    map[string][]string{},
			Fallbacks:                     map[string][]string{},
			Policies:                      map[string]RoutingPolicy{},
			MaxRetries:                    2,
			CircuitBreakerThreshold:       3,
			CircuitBreakerCooldownSeconds: 30,
			SessionStickiness:             true,
			SessionTTLSeconds:             1800,
		},
	}
}

func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b []byte
	var err error
	if s.db != nil {
		if persisted, ok, dbErr := s.db.GetDocument("config.json"); dbErr != nil {
			return dbErr
		} else if ok {
			b = persisted
		}
	}
	if len(b) == 0 {
		b, err = os.ReadFile(s.path)
		if errors.Is(err, os.ErrNotExist) {
			s.cfg = defaultConfig()
			return s.saveLocked()
		}
		if err != nil {
			return err
		}
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return err
	}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:4142"
	}
	oldSchemaVersion := c.SchemaVersion
	schemaMigrated := c.SchemaVersion < CurrentSchemaVersion
	if schemaMigrated {
		c.SchemaVersion = CurrentSchemaVersion
	}
	if c.DataAPIKey == "" {
		c.DataAPIKey = randomToken(32)
	}
	if c.AdminAPIKey == "" {
		c.AdminAPIKey = randomToken(32)
	}
	routingMissing := c.Routing.Aliases == nil && c.Routing.Priorities == nil && c.Routing.Fallbacks == nil && c.Routing.Policies == nil
	migrated := c.Version != CurrentVersion || schemaMigrated
	for i := range c.Providers {
		n := NormalizeProvider(c.Providers[i])
		if !providersEqual(c.Providers[i], n) {
			migrated = true
		}
		c.Providers[i] = n
	}
	for i := range c.Accounts {
		c.Accounts[i].ID = sanitizeID(c.Accounts[i].ID)
		c.Accounts[i].Kind = strings.ToLower(strings.TrimSpace(c.Accounts[i].Kind))
		if c.Accounts[i].Settings == nil {
			c.Accounts[i].Settings = map[string]string{}
		}
	}
	if c.Routing.Aliases == nil {
		c.Routing.Aliases = map[string]string{}
		migrated = true
	}
	if c.Routing.Priorities == nil {
		c.Routing.Priorities = map[string][]string{}
		migrated = true
	}
	if c.Routing.Fallbacks == nil {
		c.Routing.Fallbacks = map[string][]string{}
		migrated = true
	}
	if c.Routing.Policies == nil {
		c.Routing.Policies = map[string]RoutingPolicy{}
		migrated = true
	}
	if routingMissing {
		c.Routing.MaxRetries = 2
	}
	if c.Routing.CircuitBreakerThreshold <= 0 || c.Routing.CircuitBreakerThreshold > 20 {
		c.Routing.CircuitBreakerThreshold = 3
		migrated = true
	}
	if c.Routing.CircuitBreakerCooldownSeconds <= 0 || c.Routing.CircuitBreakerCooldownSeconds > 3600 {
		c.Routing.CircuitBreakerCooldownSeconds = 30
		migrated = true
	}
	if oldSchemaVersion < 4 {
		c.Routing.SessionStickiness = true
		if c.Routing.SessionTTLSeconds <= 0 {
			c.Routing.SessionTTLSeconds = 1800
		}
		migrated = true
	}
	if oldSchemaVersion < 5 {
		if c.QuotaCost.LowQuotaPercent <= 0 {
			c.QuotaCost.LowQuotaPercent = 10
		}
		if c.QuotaCost.PricingRefreshHours <= 0 {
			c.QuotaCost.PricingRefreshHours = 24
		}
		c.QuotaCost.Enabled = true
		if strings.TrimSpace(c.ContextGuard.Mode) == "" {
			c.ContextGuard.Mode = "compact"
		}
		if c.ContextGuard.MaxContextPercent <= 0 {
			c.ContextGuard.MaxContextPercent = 90
		}
		if c.ContextGuard.ReserveOutputTokens <= 0 {
			c.ContextGuard.ReserveOutputTokens = 2048
		}
		if c.ContextGuard.MinRecentMessages <= 0 {
			c.ContextGuard.MinRecentMessages = 8
		}
		if c.ContextGuard.MaxRequestBytes <= 0 {
			c.ContextGuard.MaxRequestBytes = 32 << 20
		}
		c.ContextGuard.Enabled = true
		if c.Remote.AllowedCIDRs == nil {
			c.Remote.AllowedCIDRs = []string{}
		}
		migrated = true
	}
	if oldSchemaVersion < 6 {
		c.Adaptive = AdaptiveConfig{Enabled: true, SuccessAlpha: 0.2, LatencyAlpha: 0.1, FullConfidenceAt: 50, QualityWeight: 32, EventQueueSize: 512, DecisionTTLMinutes: 60, MaxDecisions: 500}
		c.ExactCache = ExactCacheConfig{Enabled: false, TTLSeconds: 120, MaxEntries: 256, MaxEntryBytes: 2 << 20, Deduplicate: true}
		c.CatalogSync = CatalogSyncConfig{Enabled: false, URL: "https://models.dev/api.json", RefreshHours: 24}
		if strings.EqualFold(c.ContextGuard.Mode, "compact") {
			c.ContextGuard.Mode = "safe"
		}
		migrated = true
	}
	if c.Adaptive.SuccessAlpha <= 0 || c.Adaptive.SuccessAlpha > 1 {
		c.Adaptive.SuccessAlpha = 0.2
		migrated = true
	}
	if c.Adaptive.LatencyAlpha <= 0 || c.Adaptive.LatencyAlpha > 1 {
		c.Adaptive.LatencyAlpha = 0.1
		migrated = true
	}
	if c.Adaptive.FullConfidenceAt < 5 || c.Adaptive.FullConfidenceAt > 10000 {
		c.Adaptive.FullConfidenceAt = 50
		migrated = true
	}
	if c.Adaptive.QualityWeight <= 0 || c.Adaptive.QualityWeight > 200 {
		c.Adaptive.QualityWeight = 32
		migrated = true
	}
	if c.Adaptive.EventQueueSize < 16 || c.Adaptive.EventQueueSize > 65536 {
		c.Adaptive.EventQueueSize = 512
		migrated = true
	}
	if c.Adaptive.DecisionTTLMinutes < 1 || c.Adaptive.DecisionTTLMinutes > 10080 {
		c.Adaptive.DecisionTTLMinutes = 60
		migrated = true
	}
	if c.Adaptive.MaxDecisions < 10 || c.Adaptive.MaxDecisions > 100000 {
		c.Adaptive.MaxDecisions = 500
		migrated = true
	}
	if c.ExactCache.TTLSeconds < 1 || c.ExactCache.TTLSeconds > 86400 {
		c.ExactCache.TTLSeconds = 120
		migrated = true
	}
	if c.ExactCache.MaxEntries < 1 || c.ExactCache.MaxEntries > 100000 {
		c.ExactCache.MaxEntries = 256
		migrated = true
	}
	if c.ExactCache.MaxEntryBytes < 1024 || c.ExactCache.MaxEntryBytes > 32<<20 {
		c.ExactCache.MaxEntryBytes = 2 << 20
		migrated = true
	}
	if c.CatalogSync.RefreshHours < 1 || c.CatalogSync.RefreshHours > 720 {
		c.CatalogSync.RefreshHours = 24
		migrated = true
	}
	if strings.TrimSpace(c.CatalogSync.URL) == "" {
		c.CatalogSync.URL = "https://models.dev/api.json"
		migrated = true
	}
	if c.QuotaCost.LowQuotaPercent <= 0 || c.QuotaCost.LowQuotaPercent > 100 {
		c.QuotaCost.LowQuotaPercent = 10
		migrated = true
	}
	if c.QuotaCost.PricingRefreshHours <= 0 || c.QuotaCost.PricingRefreshHours > 720 {
		c.QuotaCost.PricingRefreshHours = 24
		migrated = true
	}
	if strings.TrimSpace(c.ContextGuard.Mode) == "" {
		c.ContextGuard.Mode = "compact"
		migrated = true
	}
	if c.ContextGuard.MaxContextPercent <= 10 || c.ContextGuard.MaxContextPercent > 100 {
		c.ContextGuard.MaxContextPercent = 90
		migrated = true
	}
	if c.ContextGuard.ReserveOutputTokens < 0 || c.ContextGuard.ReserveOutputTokens > 262144 {
		c.ContextGuard.ReserveOutputTokens = 2048
		migrated = true
	}
	if c.ContextGuard.MinRecentMessages < 2 || c.ContextGuard.MinRecentMessages > 200 {
		c.ContextGuard.MinRecentMessages = 8
		migrated = true
	}
	if c.ContextGuard.MaxRequestBytes <= 0 || c.ContextGuard.MaxRequestBytes > 128<<20 {
		c.ContextGuard.MaxRequestBytes = 32 << 20
		migrated = true
	}
	if c.Remote.AllowedCIDRs == nil {
		c.Remote.AllowedCIDRs = []string{}
		migrated = true
	}
	if c.Routing.SessionTTLSeconds <= 0 || c.Routing.SessionTTLSeconds > 86400 {
		c.Routing.SessionTTLSeconds = 1800
		migrated = true
	}
	if c.Routing.MaxRetries < 0 || c.Routing.MaxRetries > 5 {
		c.Routing.MaxRetries = 2
		migrated = true
	}
	c.Version = CurrentVersion
	s.cfg = c
	if migrated {
		return s.saveLocked()
	}
	return nil
}

// EnsureProtectedBridgeKeys migrates legacy plaintext bridge keys and keeps
// the live data/admin keys in the same DPAPI-backed store as upstream secrets.
// Config.Get still exposes the keys in-memory for the local Core, but saveLocked
// never writes them to config.json.
func (s *Store) EnsureProtectedBridgeKeys(secrets SecretStore) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if secrets == nil {
		return errors.New("secret store is required")
	}
	ensure := func(ref string, current *string) error {
		if v, err := secrets.Get(ref); err == nil && strings.TrimSpace(v) != "" {
			*current = v
			return nil
		}
		if strings.TrimSpace(*current) == "" {
			*current = randomToken(32)
		}
		return secrets.Put(ref, *current)
	}
	if err := ensure(BridgeDataKeyRef, &s.cfg.DataAPIKey); err != nil {
		return err
	}
	if err := ensure(BridgeAdminKeyRef, &s.cfg.AdminAPIKey); err != nil {
		return err
	}
	return s.saveLocked()
}

func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	diskCfg := clone(s.cfg)
	// Bridge authentication keys are runtime-only and are persisted in the
	// DPAPI-backed secret store by EnsureProtectedBridgeKeys. Legacy plaintext
	// values are intentionally removed on the next save.
	diskCfg.DataAPIKey = ""
	diskCfg.AdminAPIKey = ""
	b, err := json.MarshalIndent(diskCfg, "", "  ")
	if err != nil {
		return err
	}
	if s.db != nil {
		if err := s.db.SyncConfigJSON(b); err != nil {
			return err
		}
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) Get() Config { s.mu.RLock(); defer s.mu.RUnlock(); return clone(s.cfg) }

func (s *Store) UpsertProvider(p Provider) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p = NormalizeProvider(p)
	p.ID = sanitizeID(p.ID)
	if p.ID == "" {
		p.ID = sanitizeID(p.Name)
	}
	if p.ID == "" {
		return errors.New("provider id is empty")
	}
	if p.APIType == "" {
		p.APIType = "chat-completions"
	}
	if p.Headers == nil {
		p.Headers = map[string]string{}
	}
	for _, a := range s.cfg.Accounts {
		if a.ID == p.ID {
			return errors.New("provider id conflicts with an account connector id")
		}
	}
	found := false
	for i := range s.cfg.Providers {
		if s.cfg.Providers[i].ID == p.ID {
			s.cfg.Providers[i] = p
			found = true
			break
		}
	}
	if !found {
		s.cfg.Providers = append(s.cfg.Providers, p)
	}
	return s.saveLocked()
}

func (s *Store) UpsertAccount(a AccountConnection) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a.ID = sanitizeID(a.ID)
	if a.ID == "" {
		a.ID = sanitizeID(a.Name)
	}
	if a.ID == "" {
		return errors.New("account id is empty")
	}
	a.Kind = strings.ToLower(strings.TrimSpace(a.Kind))
	if a.Settings == nil {
		a.Settings = map[string]string{}
	}
	for _, p := range s.cfg.Providers {
		if p.ID == a.ID {
			return errors.New("account id conflicts with an API provider id")
		}
	}
	found := false
	for i := range s.cfg.Accounts {
		if s.cfg.Accounts[i].ID == a.ID {
			s.cfg.Accounts[i] = a
			found = true
			break
		}
	}
	if !found {
		s.cfg.Accounts = append(s.cfg.Accounts, a)
	}
	return s.saveLocked()
}

func (s *Store) DeleteAccount(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.cfg.Accounts[:0]
	for _, a := range s.cfg.Accounts {
		if a.ID != id {
			out = append(out, a)
		}
	}
	s.cfg.Accounts = out
	return s.saveLocked()
}

func (s *Store) DeleteProvider(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.cfg.Providers[:0]
	for _, p := range s.cfg.Providers {
		if p.ID != id {
			out = append(out, p)
		}
	}
	s.cfg.Providers = out
	return s.saveLocked()
}

func (s *Store) SetRouting(r RoutingConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Aliases == nil {
		r.Aliases = map[string]string{}
	}
	if r.Priorities == nil {
		r.Priorities = map[string][]string{}
	}
	if r.Fallbacks == nil {
		r.Fallbacks = map[string][]string{}
	}
	if r.Policies == nil {
		r.Policies = map[string]RoutingPolicy{}
	}
	if r.MaxRetries < 0 || r.MaxRetries > 5 {
		return errors.New("max_retries must be between 0 and 5")
	}
	if r.CircuitBreakerThreshold == 0 {
		r.CircuitBreakerThreshold = 3
	}
	if r.CircuitBreakerThreshold < 1 || r.CircuitBreakerThreshold > 20 {
		return errors.New("circuit_breaker_threshold must be between 1 and 20")
	}
	if r.CircuitBreakerCooldownSeconds == 0 {
		r.CircuitBreakerCooldownSeconds = 30
	}
	if r.CircuitBreakerCooldownSeconds < 1 || r.CircuitBreakerCooldownSeconds > 3600 {
		return errors.New("circuit_breaker_cooldown_seconds must be between 1 and 3600")
	}
	if r.SessionTTLSeconds == 0 {
		r.SessionTTLSeconds = 1800
	}
	if r.SessionTTLSeconds < 30 || r.SessionTTLSeconds > 86400 {
		return errors.New("session_ttl_seconds must be between 30 and 86400")
	}
	cleanPolicies := make(map[string]RoutingPolicy, len(r.Policies))
	for name, policy := range r.Policies {
		name = sanitizeID(name)
		policy.Mode = strings.ToLower(strings.TrimSpace(policy.Mode))
		switch policy.Mode {
		case "direct", "priority", "fastest", "agent_ready_only", "local_only", "cloud_only", "weighted", "round_robin", "least_used", "p2c", "quota_optimized", "quota_share", "cost_optimized", "auto":
		default:
			return errors.New("unsupported routing policy mode")
		}
		if name == "" {
			continue
		}
		clean := make([]string, 0, len(policy.Candidates))
		for _, candidate := range policy.Candidates {
			candidate = strings.TrimSpace(candidate)
			if candidate != "" {
				clean = append(clean, candidate)
			}
		}
		policy.Candidates = clean
		for key, weight := range policy.Weights {
			if strings.TrimSpace(key) == "" || weight <= 0 || weight > 10000 {
				return errors.New("routing weights must be > 0 and <= 10000")
			}
		}
		for key, quota := range policy.QuotaHints {
			if strings.TrimSpace(key) == "" || quota < 0 || quota > 100 {
				return errors.New("routing quota_hints must be between 0 and 100")
			}
		}
		for key, cost := range policy.CostHints {
			if strings.TrimSpace(key) == "" || cost < 0 || cost > 1000000 {
				return errors.New("routing cost_hints must be non-negative")
			}
		}
		cleanPolicies[name] = policy
	}
	r.Policies = cleanPolicies
	cleanAliases := make(map[string]string, len(r.Aliases))
	for alias, target := range r.Aliases {
		alias = sanitizeID(alias)
		target = strings.TrimSpace(target)
		if alias == "" || target == "" {
			continue
		}
		cleanAliases[alias] = target
	}
	r.Aliases = cleanAliases
	s.cfg.Routing = r
	return s.saveLocked()
}

func (s *Store) SetQuotaCost(q QuotaCostConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if q.LowQuotaPercent <= 0 || q.LowQuotaPercent > 100 {
		return errors.New("low_quota_percent must be > 0 and <= 100")
	}
	if q.PricingRefreshHours <= 0 || q.PricingRefreshHours > 720 {
		return errors.New("pricing_refresh_hours must be between 1 and 720")
	}
	for _, v := range []float64{q.Budget.SessionUSD, q.Budget.DailyUSD, q.Budget.MonthlyUSD} {
		if v < 0 || v > 1e9 {
			return errors.New("budgets must be non-negative")
		}
	}
	s.cfg.QuotaCost = q
	return s.saveLocked()
}

func (s *Store) SetContextGuard(c ContextGuardConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c.Mode = strings.ToLower(strings.TrimSpace(c.Mode))
	if c.Mode == "" {
		c.Mode = "safe"
	}
	if c.Mode == "compact" {
		c.Mode = "safe"
	}
	if c.Mode != "safe" && c.Mode != "balanced" && c.Mode != "experimental" && c.Mode != "observe" && c.Mode != "off" {
		return errors.New("context guard mode must be safe, balanced, experimental, observe or off")
	}
	if c.MaxContextPercent <= 10 || c.MaxContextPercent > 100 {
		return errors.New("max_context_percent must be > 10 and <= 100")
	}
	if c.ReserveOutputTokens < 0 || c.ReserveOutputTokens > 262144 {
		return errors.New("reserve_output_tokens is out of range")
	}
	if c.MinRecentMessages < 2 || c.MinRecentMessages > 200 {
		return errors.New("min_recent_messages must be between 2 and 200")
	}
	if c.MaxRequestBytes <= 0 || c.MaxRequestBytes > 128<<20 {
		return errors.New("max_request_bytes is out of range")
	}
	s.cfg.ContextGuard = c
	return s.saveLocked()
}

func (s *Store) SetAdaptive(c AdaptiveConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.SuccessAlpha <= 0 || c.SuccessAlpha > 1 || c.LatencyAlpha <= 0 || c.LatencyAlpha > 1 {
		return errors.New("adaptive EWMA alpha values must be > 0 and <= 1")
	}
	if c.FullConfidenceAt < 5 || c.FullConfidenceAt > 10000 || c.QualityWeight <= 0 || c.QualityWeight > 200 {
		return errors.New("adaptive confidence or quality weight is out of range")
	}
	if c.EventQueueSize < 16 || c.EventQueueSize > 65536 || c.MaxDecisions < 10 || c.MaxDecisions > 100000 {
		return errors.New("adaptive queue or retention is out of range")
	}
	if c.DecisionTTLMinutes < 1 || c.DecisionTTLMinutes > 10080 {
		return errors.New("decision_ttl_minutes is out of range")
	}
	s.cfg.Adaptive = c
	return s.saveLocked()
}

func (s *Store) SetExactCache(c ExactCacheConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.TTLSeconds < 1 || c.TTLSeconds > 86400 || c.MaxEntries < 1 || c.MaxEntries > 100000 || c.MaxEntryBytes < 1024 || c.MaxEntryBytes > 32<<20 {
		return errors.New("exact response cache limits are out of range")
	}
	s.cfg.ExactCache = c
	return s.saveLocked()
}

func (s *Store) SetCatalogSync(c CatalogSyncConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c.URL = strings.TrimSpace(c.URL)
	if c.URL == "" || c.RefreshHours < 1 || c.RefreshHours > 720 {
		return errors.New("catalog sync URL or refresh interval is invalid")
	}
	s.cfg.CatalogSync = c
	return s.saveLocked()
}

func (s *Store) SetRemote(c RemoteConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range c.AllowedCIDRs {
		c.AllowedCIDRs[i] = strings.TrimSpace(c.AllowedCIDRs[i])
	}
	s.cfg.Remote = c
	return s.saveLocked()
}

func providersEqual(a, b Provider) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func clone(c Config) Config {
	b, _ := json.Marshal(c)
	var x Config
	_ = json.Unmarshal(b, &x)
	return x
}

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func sanitizeID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	dash := false
	for _, r := range s {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if ok {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func NormalizeProvider(p Provider) Provider {
	p.Kind = strings.ToLower(strings.TrimSpace(p.Kind))
	if p.Kind == "" || p.Kind == "auto" {
		p.Kind = DetectKind(p.BaseURL, "")
	}
	if p.Kind == "openai-compatible" {
		p.Kind = "custom"
	}
	if preset, ok := ProviderPresetByID(p.Kind); ok {
		if p.Name == "" {
			p.Name = preset.Name
		}
		if p.BaseURL == "" {
			p.BaseURL = preset.BaseURL
		}
		if p.ModelsURL == "" {
			p.ModelsURL = preset.ModelsURL
		}
		if p.AuthType == "" {
			p.AuthType = preset.AuthType
		}
		if len(p.Protocols) == 0 {
			p.Protocols = append([]string(nil), preset.Protocols...)
		}
		if p.PreferredProtocol == "" {
			p.PreferredProtocol = preset.PreferredProtocol
		}
		if len(p.ProtocolBaseURLs) == 0 {
			p.ProtocolBaseURLs = cloneMap(preset.ProtocolBaseURLs)
		}
		if len(p.ProtocolAuthTypes) == 0 {
			p.ProtocolAuthTypes = cloneMap(preset.ProtocolAuthTypes)
		}
		if p.Headers == nil {
			p.Headers = cloneMap(preset.Headers)
		} else {
			for k, v := range preset.Headers {
				if p.Headers[k] == "" {
					p.Headers[k] = v
				}
			}
		}
		if p.Quirks == nil {
			p.Quirks = cloneMap(preset.Quirks)
		} else {
			for k, v := range preset.Quirks {
				if p.Quirks[k] == "" {
					p.Quirks[k] = v
				}
			}
		}
	}
	if p.Kind == "gigachat" {
		p.AuthType = "gigachat-oauth"
		if p.Scope == "" {
			p.Scope = "GIGACHAT_API_PERS"
		}
	}
	if p.Kind == "yandex" {
		p.ProjectID = strings.TrimSpace(p.ProjectID)
		p.AuthType = "yandex-api-key"
		if p.Headers == nil {
			p.Headers = map[string]string{}
		}
		if p.ProjectID != "" {
			p.Headers["OpenAI-Project"] = p.ProjectID
		}
	}
	if p.AuthType == "" {
		p.AuthType = "bearer"
	}
	if len(p.Protocols) == 0 {
		p.Protocols = []string{"chat-completions"}
	}
	if p.PreferredProtocol == "" {
		if p.APIType != "" {
			p.PreferredProtocol = p.APIType
		} else {
			p.PreferredProtocol = p.Protocols[0]
		}
	}
	if p.APIType == "" {
		p.APIType = p.PreferredProtocol
	}
	if p.ProtocolBaseURLs == nil {
		p.ProtocolBaseURLs = map[string]string{}
	}
	if p.ProtocolAuthTypes == nil {
		p.ProtocolAuthTypes = map[string]string{}
	}
	if p.Headers == nil {
		p.Headers = map[string]string{}
	}
	if p.Quirks == nil {
		p.Quirks = map[string]string{}
	}
	p.BaseURL = strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
	p.ModelsURL = strings.TrimSpace(p.ModelsURL)
	for k, v := range p.ProtocolBaseURLs {
		p.ProtocolBaseURLs[k] = strings.TrimRight(strings.TrimSpace(v), "/")
	}
	return p
}

func DetectKind(baseURL, key string) string {
	return DetectProviderDefinition(baseURL, key)
}

func SupportsProtocol(p Provider, apiType string) bool {
	p = NormalizeProvider(p)
	for _, x := range p.Protocols {
		if x == apiType {
			return true
		}
	}
	return false
}

func ProtocolBaseURL(p Provider, apiType string) string {
	p = NormalizeProvider(p)
	if v := p.ProtocolBaseURLs[apiType]; v != "" {
		return v
	}
	return p.BaseURL
}

func ProtocolAuthType(p Provider, apiType string) string {
	p = NormalizeProvider(p)
	if v := p.ProtocolAuthTypes[apiType]; v != "" {
		return v
	}
	return p.AuthType
}
