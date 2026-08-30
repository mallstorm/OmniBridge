package orchestration

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"omnibridge/internal/storage"
)

const configDocument = "team-routing.json"

type Config struct {
	Enabled  bool               `json:"enabled"`
	Policy   Policy             `json:"policy"`
	Profiles map[string]Profile `json:"profiles"`
}

type Service struct {
	mu     sync.RWMutex
	db     *storage.DB
	cfg    Config
	engine *Engine
}

func DefaultConfig() Config {
	return Config{
		Enabled: false,
		Policy: Policy{
			Enabled:                 false,
			StickyTTLSeconds:        7200,
			ReviewerRiskThreshold:   "high",
			MaxAssistants:           1,
			AllowCriticalReserveUse: false,
		},
		Profiles: map[string]Profile{},
	}
}

func normalizeConfig(c Config) (Config, error) {
	if c.Profiles == nil {
		c.Profiles = map[string]Profile{}
	}
	if c.Policy.StickyTTLSeconds <= 0 {
		c.Policy.StickyTTLSeconds = 7200
	}
	if c.Policy.StickyTTLSeconds < 60 || c.Policy.StickyTTLSeconds > 7*24*3600 {
		return Config{}, errors.New("sticky_ttl_seconds must be between 60 and 604800")
	}
	if strings.TrimSpace(c.Policy.ReviewerRiskThreshold) == "" {
		c.Policy.ReviewerRiskThreshold = "high"
	}
	if !validRisk(c.Policy.ReviewerRiskThreshold) {
		return Config{}, errors.New("invalid reviewer_risk_threshold")
	}
	c.Policy.ReviewerRiskThreshold = normalizedRisk(c.Policy.ReviewerRiskThreshold)
	if c.Policy.MaxAssistants < 0 || c.Policy.MaxAssistants > 4 {
		return Config{}, errors.New("max_assistants must be between 0 and 4")
	}
	c.Policy.Enabled = c.Enabled
	out := make(map[string]Profile, len(c.Profiles))
	for key, p := range c.Profiles {
		key = strings.TrimSpace(key)
		if key == "" {
			return Config{}, errors.New("profile key must not be empty")
		}
		if strings.TrimSpace(p.ModelID) == "" {
			p.ModelID = key
		}
		if len(p.Roles) == 0 {
			return Config{}, fmt.Errorf("profile %s must define at least one role", key)
		}
		roles := make([]string, 0, len(p.Roles))
		seen := map[string]bool{}
		for _, role := range p.Roles {
			role = strings.ToLower(strings.TrimSpace(role))
			if role == "" || seen[role] {
				continue
			}
			seen[role] = true
			roles = append(roles, role)
		}
		if len(roles) == 0 {
			return Config{}, fmt.Errorf("profile %s has no valid roles", key)
		}
		p.Roles = roles
		if strings.TrimSpace(p.RiskLimit) == "" {
			p.RiskLimit = "medium"
		} else if !validRisk(p.RiskLimit) {
			return Config{}, fmt.Errorf("profile %s has invalid risk_limit", key)
		}
		p.RiskLimit = normalizedRisk(p.RiskLimit)
		if p.MinimumQuotaReserve < 0 || p.MinimumQuotaReserve > 100 {
			return Config{}, fmt.Errorf("profile %s minimum_quota_reserve must be between 0 and 100", key)
		}
		// A profile that omits all assignment switches is treated as lead-only.
		if !p.Lead && !p.Assist && !p.Review {
			p.Lead = true
		}
		out[key] = p
	}
	c.Profiles = out
	return c, nil
}

func NewService(db *storage.DB) *Service {
	cfg := DefaultConfig()
	if db != nil {
		if raw, ok, err := db.GetDocument(configDocument); err == nil && ok {
			var stored Config
			if json.Unmarshal(raw, &stored) == nil {
				if normalized, err := normalizeConfig(stored); err == nil {
					cfg = normalized
				}
			}
		}
	}
	return &Service{db: db, cfg: cfg, engine: New()}
}

func (s *Service) Config() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, _ := json.Marshal(s.cfg)
	var out Config
	_ = json.Unmarshal(b, &out)
	return out
}

func (s *Service) Update(c Config) error {
	normalized, err := normalizeConfig(c)
	if err != nil {
		return err
	}
	b, err := json.Marshal(normalized)
	if err != nil {
		return err
	}
	if s.db != nil {
		if err := s.db.PutDocument(configDocument, b); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.cfg = normalized
	s.mu.Unlock()
	return nil
}

func (s *Service) Engine() *Engine { return s.engine }

func (s *Service) ProfileFor(modelID, nativeID, providerID string) (Profile, bool, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, key := range []string{modelID, nativeID, providerID, "*"} {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if p, ok := s.cfg.Profiles[key]; ok {
			return p, true, key
		}
	}
	return Profile{}, false, ""
}

func (s *Service) Snapshot() map[string]any {
	return map[string]any{"version": Version, "config": s.Config(), "runtime": s.engine.Snapshot()}
}
