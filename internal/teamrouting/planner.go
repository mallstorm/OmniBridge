package teamrouting

import (
	"fmt"
	"sort"
	"strings"
)

const Version = "1.0"

type Candidate struct {
	ID               string  `json:"id"`
	ProviderID       string  `json:"provider_id"`
	Family           string  `json:"family"`
	DoctorVerified   bool    `json:"doctor_verified"`
	DoctorScore      float64 `json:"doctor_score,omitempty"`
	AgentScore       float64 `json:"agent_score,omitempty"`
	CodingScore      float64 `json:"coding_score,omitempty"`
	ReliabilityScore float64 `json:"reliability_score,omitempty"`
	Reasoning        bool    `json:"reasoning,omitempty"`
	QuotaKnown       bool    `json:"quota_known,omitempty"`
	QuotaRemaining   float64 `json:"quota_remaining,omitempty"`
	CostKnown        bool    `json:"cost_known,omitempty"`
	CostPerMTokens   float64 `json:"cost_per_m_tokens,omitempty"`
	OriginalRank     int     `json:"original_rank"`
}

type History struct {
	Providers []string `json:"providers,omitempty"`
	Families  []string `json:"families,omitempty"`
	Models    []string `json:"models,omitempty"`
}

type Profile struct {
	Name              string  `json:"name"`
	CodingWeight      float64 `json:"coding_weight,omitempty"`
	ReliabilityWeight float64 `json:"reliability_weight,omitempty"`
	DoctorWeight      float64 `json:"doctor_weight,omitempty"`
	AgentWeight       float64 `json:"agent_weight,omitempty"`
	ReasoningBonus    float64 `json:"reasoning_bonus,omitempty"`
	CostWeight        float64 `json:"cost_weight,omitempty"`
	QuotaWeight       float64 `json:"quota_weight,omitempty"`
	RequireDoctor     bool    `json:"require_doctor,omitempty"`
	Judge             bool    `json:"judge,omitempty"`
}

type Policy struct {
	Diversity               string  `json:"diversity"`
	AllowDiversityFallback  bool    `json:"allow_diversity_fallback"`
	MaxCandidates           int     `json:"max_candidates"`
	QuotaFloorPercent       float64 `json:"quota_floor_percent,omitempty"`
	DiversityPenalty        float64 `json:"diversity_penalty,omitempty"`
	JudgeDiversityPenalty   float64 `json:"judge_diversity_penalty,omitempty"`
	PreferDifferentProvider bool    `json:"prefer_different_provider,omitempty"`
	PreferDifferentFamily   bool    `json:"prefer_different_family,omitempty"`
}

type RankedCandidate struct {
	Candidate Candidate `json:"candidate"`
	Score     float64   `json:"score"`
	Fallback  bool      `json:"fallback,omitempty"`
	Eligible  bool      `json:"eligible"`
	Rationale []string  `json:"rationale"`
}

type Plan struct {
	Version    string            `json:"version"`
	Profile    Profile           `json:"profile"`
	Policy     Policy            `json:"policy"`
	Candidates []RankedCandidate `json:"candidates"`
}

func DefaultProfile(name string) Profile {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "developer":
		return Profile{Name: "developer", CodingWeight: .52, ReliabilityWeight: .18, DoctorWeight: .10, AgentWeight: .05, ReasoningBonus: 4, CostWeight: .07, QuotaWeight: .08}
	case "fixer":
		return Profile{Name: "fixer", CodingWeight: .45, ReliabilityWeight: .25, DoctorWeight: .12, AgentWeight: .05, ReasoningBonus: 5, CostWeight: .05, QuotaWeight: .08}
	case "critic":
		return Profile{Name: "critic", CodingWeight: .08, ReliabilityWeight: .30, DoctorWeight: .25, AgentWeight: .05, ReasoningBonus: 18, CostWeight: .05, QuotaWeight: .09}
	case "reviewer", "judge":
		return Profile{Name: "judge", CodingWeight: .05, ReliabilityWeight: .38, DoctorWeight: .30, AgentWeight: .10, ReasoningBonus: 15, CostWeight: .02, QuotaWeight: .05, RequireDoctor: true, Judge: true}
	default:
		return Profile{Name: "smart", CodingWeight: .18, ReliabilityWeight: .27, DoctorWeight: .22, AgentWeight: .08, ReasoningBonus: 8, CostWeight: .10, QuotaWeight: .10}
	}
}

func DefaultPolicy() Policy {
	return Policy{
		Diversity:               "provider",
		AllowDiversityFallback:  true,
		MaxCandidates:           4,
		QuotaFloorPercent:       5,
		DiversityPenalty:        45,
		JudgeDiversityPenalty:   70,
		PreferDifferentProvider: true,
		PreferDifferentFamily:   true,
	}
}

func InferFamily(providerID, nativeID, qualifiedID string) string {
	provider := strings.ToLower(strings.TrimSpace(providerID))
	id := strings.ToLower(strings.TrimSpace(nativeID))
	if id == "" {
		id = strings.ToLower(strings.TrimSpace(qualifiedID))
		if i := strings.LastIndex(id, "/"); i >= 0 && i+1 < len(id) {
			id = id[i+1:]
		}
	}
	patterns := []struct{ needle, family string }{
		{"gigachat", "gigachat"},
		{"claude", "claude"},
		{"gemini", "gemini"},
		{"deepseek", "deepseek"},
		{"qwen", "qwen"},
		{"llama", "llama"},
		{"mistral", "mistral"},
		{"mixtral", "mixtral"},
		{"grok", "grok"},
		{"command", "command"},
		{"phi", "phi"},
	}
	for _, p := range patterns {
		if strings.Contains(id, p.needle) {
			return joinFamily(provider, p.family)
		}
	}
	if strings.HasPrefix(id, "gpt-") || strings.HasPrefix(id, "chatgpt-") {
		return joinFamily(provider, "gpt")
	}
	if strings.HasPrefix(id, "o1") || strings.HasPrefix(id, "o3") || strings.HasPrefix(id, "o4") {
		return joinFamily(provider, "o-series")
	}
	base := id
	if i := strings.IndexAny(base, "-:._"); i > 0 {
		base = base[:i]
	}
	if base == "" {
		base = "unknown"
	}
	return joinFamily(provider, base)
}

func joinFamily(provider, family string) string {
	if provider == "" {
		return family
	}
	return provider + ":" + family
}

func Rank(candidates []Candidate, profile Profile, history History, policy Policy) Plan {
	if strings.TrimSpace(profile.Name) == "" {
		profile = DefaultProfile("smart")
	}
	if policy.MaxCandidates <= 0 {
		policy.MaxCandidates = 4
	}
	if policy.DiversityPenalty <= 0 {
		policy.DiversityPenalty = 45
	}
	if policy.JudgeDiversityPenalty <= 0 {
		policy.JudgeDiversityPenalty = 70
	}
	if policy.QuotaFloorPercent < 0 {
		policy.QuotaFloorPercent = 0
	}

	usedProviders := set(history.Providers)
	usedFamilies := set(history.Families)
	usedModels := set(history.Models)
	minCost, maxCost, haveCost := costRange(candidates)
	out := make([]RankedCandidate, 0, len(candidates))

	for _, c := range candidates {
		r := RankedCandidate{Candidate: c, Eligible: true}
		if c.Family == "" {
			c.Family = InferFamily(c.ProviderID, "", c.ID)
			r.Candidate.Family = c.Family
		}
		if profile.RequireDoctor && !c.DoctorVerified {
			r.Eligible = false
			r.Rationale = append(r.Rationale, "rejected: doctor proof required by profile")
		}

		r.Score += profile.CodingWeight * clamp100(c.CodingScore)
		r.Score += profile.ReliabilityWeight * clamp100(c.ReliabilityScore)
		r.Score += profile.DoctorWeight * clamp100(c.DoctorScore)
		r.Score += profile.AgentWeight * clamp100(c.AgentScore)
		if c.Reasoning && profile.ReasoningBonus > 0 {
			r.Score += profile.ReasoningBonus
			r.Rationale = append(r.Rationale, "reasoning capability bonus")
		}

		if profile.QuotaWeight > 0 {
			quotaScore := 50.0
			if c.QuotaKnown {
				quotaScore = clamp100(c.QuotaRemaining)
				r.Rationale = append(r.Rationale, fmt.Sprintf("quota %.1f%%", c.QuotaRemaining))
				if policy.QuotaFloorPercent > 0 && c.QuotaRemaining < policy.QuotaFloorPercent {
					r.Score -= 35
					r.Fallback = true
					r.Rationale = append(r.Rationale, fmt.Sprintf("low-quota penalty below %.1f%% floor", policy.QuotaFloorPercent))
				}
			}
			r.Score += profile.QuotaWeight * quotaScore
		}

		if profile.CostWeight > 0 {
			costScore := 50.0
			if c.CostKnown && haveCost {
				costScore = relativeCheapness(c.CostPerMTokens, minCost, maxCost)
				r.Rationale = append(r.Rationale, fmt.Sprintf("cost %.4f per representative M-token unit", c.CostPerMTokens))
			}
			r.Score += profile.CostWeight * costScore
		}

		conflict := diversityConflict(c, usedProviders, usedFamilies, usedModels, policy)
		if conflict != "" {
			penalty := policy.DiversityPenalty
			if profile.Judge {
				penalty = policy.JudgeDiversityPenalty
			}
			r.Score -= penalty
			r.Fallback = true
			r.Rationale = append(r.Rationale, fmt.Sprintf("diversity penalty: %s already represented", conflict))
			if !policy.AllowDiversityFallback {
				r.Eligible = false
				r.Rationale = append(r.Rationale, "rejected: diversity fallback disabled")
			}
		}

		if profile.Judge {
			if policy.PreferDifferentProvider && usedProviders[c.ProviderID] {
				r.Score -= 20
				r.Fallback = true
				r.Rationale = append(r.Rationale, "judge independence penalty: provider already used")
			}
			if policy.PreferDifferentFamily && usedFamilies[c.Family] {
				r.Score -= 15
				r.Fallback = true
				r.Rationale = append(r.Rationale, "judge independence penalty: model family already used")
			}
		}

		r.Rationale = append(r.Rationale, "role profile="+profile.Name)
		out = append(out, r)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Eligible != out[j].Eligible {
			return out[i].Eligible
		}
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Candidate.OriginalRank != out[j].Candidate.OriginalRank {
			return out[i].Candidate.OriginalRank < out[j].Candidate.OriginalRank
		}
		return out[i].Candidate.ID < out[j].Candidate.ID
	})

	eligible := make([]RankedCandidate, 0, len(out))
	for _, r := range out {
		if r.Eligible {
			eligible = append(eligible, r)
		}
	}
	if len(eligible) > policy.MaxCandidates {
		eligible = eligible[:policy.MaxCandidates]
	}
	return Plan{Version: Version, Profile: profile, Policy: policy, Candidates: eligible}
}

func diversityConflict(c Candidate, providers, families, models map[string]bool, policy Policy) string {
	switch strings.ToLower(strings.TrimSpace(policy.Diversity)) {
	case "model":
		if models[c.ID] {
			return "model"
		}
	case "family":
		if families[c.Family] {
			return "family"
		}
	case "provider":
		if providers[c.ProviderID] {
			return "provider"
		}
	}
	return ""
}

func set(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			out[v] = true
		}
	}
	return out
}

func clamp100(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func costRange(candidates []Candidate) (float64, float64, bool) {
	var min, max float64
	have := false
	for _, c := range candidates {
		if !c.CostKnown || c.CostPerMTokens < 0 {
			continue
		}
		if !have || c.CostPerMTokens < min {
			min = c.CostPerMTokens
		}
		if !have || c.CostPerMTokens > max {
			max = c.CostPerMTokens
		}
		have = true
	}
	return min, max, have
}

func relativeCheapness(cost, min, max float64) float64 {
	if max <= min {
		return 100
	}
	v := 100 * (max - cost) / (max - min)
	return clamp100(v)
}
