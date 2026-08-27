package server

import (
	"strings"

	"omnibridge/internal/config"
	"omnibridge/internal/orchestration"
	"omnibridge/internal/providers"
	"omnibridge/internal/teamrouting"
)

func orchestrationProfileName(step orchestration.Step) string {
	if name := strings.TrimSpace(step.Selector.Profile); name != "" {
		return name
	}
	switch step.Role {
	case orchestration.RoleDeveloper:
		return "developer"
	case orchestration.RoleCritic:
		return "critic"
	case orchestration.RoleFixer:
		return "fixer"
	case orchestration.RoleReviewer:
		return "judge"
	default:
		return "smart"
	}
}

func (s *Server) orchestrationTeamPlan(call orchestration.Call, candidates []providers.Model) ([]providers.Model, map[string]orchestration.RoutingDecision, teamrouting.Plan) {
	profile := teamrouting.DefaultProfile(orchestrationProfileName(call.Step))
	policy := teamrouting.DefaultPolicy()
	policy.Diversity = string(call.Workflow.Policy.Diversity)
	if call.Workflow.Policy.Fallback.MaxCandidates > 0 {
		policy.MaxCandidates = call.Workflow.Policy.Fallback.MaxCandidates
	}
	// A workflow that explicitly configures fallback controls owns this value.
	// The built-in workflow enables graceful same-provider fallback.
	if call.Workflow.Policy.Fallback.MaxCandidates > 0 || call.Workflow.Policy.Fallback.QuotaFloorPercent > 0 {
		policy.AllowDiversityFallback = call.Workflow.Policy.Fallback.AllowDiversityFallback
	}
	if call.Workflow.Policy.Fallback.QuotaFloorPercent > 0 {
		policy.QuotaFloorPercent = call.Workflow.Policy.Fallback.QuotaFloorPercent
	}
	if call.Step.Role == orchestration.RoleReviewer {
		policy.PreferDifferentProvider = call.Workflow.Policy.Judge.PreferDifferentProvider
		policy.PreferDifferentFamily = call.Workflow.Policy.Judge.PreferDifferentFamily
	}

	routingPolicy := config.RoutingPolicy{}
	selector := strings.TrimSpace(call.Step.Selector.Model)
	if strings.HasPrefix(strings.ToLower(selector), "auto/") {
		routingPolicy, _ = autoPolicy(selector)
	}
	if s != nil && s.Config != nil {
		if configured, ok := s.Config.Get().Routing.Policies[selector]; ok {
			routingPolicy = configured
		}
	}

	generic := make([]teamrouting.Candidate, 0, len(candidates))
	byID := make(map[string]providers.Model, len(candidates))
	for i, m := range candidates {
		rc := s.toRouterCandidate(m, routingPolicy)
		family := teamrouting.InferFamily(m.ProviderID, m.NativeID, m.ID)
		generic = append(generic, teamrouting.Candidate{
			ID:               m.ID,
			ProviderID:       m.ProviderID,
			Family:           family,
			DoctorVerified:   rc.DoctorVerified,
			DoctorScore:      rc.DoctorScore,
			AgentScore:       rc.AgentScore,
			CodingScore:      rc.CodingScore,
			ReliabilityScore: rc.ReliabilityScore,
			Reasoning:        rc.Reasoning,
			QuotaKnown:       rc.QuotaKnown,
			QuotaRemaining:   rc.QuotaRemaining,
			CostKnown:        rc.CostKnown,
			CostPerMTokens:   rc.CostPerMTokens,
			OriginalRank:     i,
		})
		byID[m.ID] = m
	}

	plan := teamrouting.Rank(generic, profile, teamrouting.History{
		Providers: append([]string(nil), call.AvoidProviders...),
		Families:  append([]string(nil), call.AvoidFamilies...),
		Models:    append([]string(nil), call.AvoidModels...),
	}, policy)

	ordered := make([]providers.Model, 0, len(plan.Candidates))
	decisions := make(map[string]orchestration.RoutingDecision, len(plan.Candidates))
	for i, ranked := range plan.Candidates {
		m, ok := byID[ranked.Candidate.ID]
		if !ok {
			continue
		}
		ordered = append(ordered, m)
		decisions[m.ID] = orchestration.RoutingDecision{
			Profile:        plan.Profile.Name,
			Family:         ranked.Candidate.Family,
			CandidateRank:  i + 1,
			CandidateCount: len(plan.Candidates),
			Fallback:       ranked.Fallback,
			QuotaKnown:     ranked.Candidate.QuotaKnown,
			QuotaRemaining: ranked.Candidate.QuotaRemaining,
			CostKnown:      ranked.Candidate.CostKnown,
			CostPerMTokens: ranked.Candidate.CostPerMTokens,
			Rationale:      append([]string(nil), ranked.Rationale...),
		}
	}
	return ordered, decisions, plan
}

func appendRoutingRationale(in orchestration.RoutingDecision, note string) orchestration.RoutingDecision {
	in.Rationale = append(in.Rationale, note)
	return in
}

// applyOrchestrationDiversity remains as a small compatibility helper for
// existing unit tests and callers. Production workflow execution uses the richer
// team planner above. Unlike the v1.1 helper, family diversity is now distinct
// from provider diversity.
func applyOrchestrationDiversity(candidates []providers.Model, call orchestration.Call) []providers.Model {
	if call.Workflow.Policy.Diversity == orchestration.DiversityNone || len(candidates) < 2 {
		return candidates
	}
	usedProviders := stringSet(call.AvoidProviders)
	usedFamilies := stringSet(call.AvoidFamilies)
	usedModels := stringSet(call.AvoidModels)
	preferred := make([]providers.Model, 0, len(candidates))
	fallback := make([]providers.Model, 0, len(candidates))
	for _, m := range candidates {
		family := teamrouting.InferFamily(m.ProviderID, m.NativeID, m.ID)
		avoid := false
		switch call.Workflow.Policy.Diversity {
		case orchestration.DiversityProvider:
			avoid = usedProviders[m.ProviderID]
		case orchestration.DiversityFamily:
			avoid = usedFamilies[family]
		case orchestration.DiversityModel:
			avoid = usedModels[m.ID]
		}
		if avoid {
			fallback = append(fallback, m)
		} else {
			preferred = append(preferred, m)
		}
	}
	return append(preferred, fallback...)
}

func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			out[v] = true
		}
	}
	return out
}
