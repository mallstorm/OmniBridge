package orchestration

import (
	"sort"
	"strings"
)

// ExecutionConfig is intentionally separate from planning Policy. Operators can
// use Team Routing dry-run/planning without allowing any additional upstream
// calls. Zero values are normalized by the persistence service.
type ExecutionConfig struct {
	Enabled                 bool    `json:"enabled"`
	MaxCalls                int     `json:"max_calls,omitempty"`
	MaxWallTimeSeconds      int     `json:"max_wall_time_seconds,omitempty"`
	MaxInputBytes           int64   `json:"max_input_bytes,omitempty"`
	MaxResponseBytes        int64   `json:"max_response_bytes,omitempty"`
	MaxReviewBytes          int64   `json:"max_review_bytes,omitempty"`
	MaxEstimatedCostUSD     float64 `json:"max_estimated_cost_usd,omitempty"`
	RequireKnownCost        bool    `json:"require_known_cost,omitempty"`
	RunAssistants           bool    `json:"run_assistants,omitempty"`
	BlockOnReviewConcernsAt string  `json:"block_on_review_concerns_at,omitempty"`
}

// ValidateAssignment reuses exactly the same role/risk/quota gates as planning.
// It is used immediately before dispatch so a stale plan cannot bypass a hard
// gate that changed after planning.
func ValidateAssignment(c Candidate, task Task, role string, policy Policy) []string {
	reasons, ok := eligibility(c, task, role, policy)
	if !ok {
		return reasons
	}
	if role == "lead" || role == "assist" {
		if _, matched := roleMatch(c.Profile, task.RequiredRoles); !matched {
			return []string{"required_role_mismatch"}
		}
	}
	if role == "review" {
		if _, matched := roleMatch(c.Profile, []string{"review", "analysis", "security"}); !matched {
			return []string{"review_role_mismatch"}
		}
	}
	return nil
}

// PreferIndependentReviewer keeps the normal reviewer score, but when the
// selected reviewer shares the lead provider it prefers a valid reviewer from a
// different provider. Different provider is not a hard requirement because a
// single-provider installation still needs a usable fail-closed review path.
func PreferIndependentReviewer(plan Plan, candidates []Candidate, policy Policy) Plan {
	if plan.Lead == nil || !plan.ReviewerRequired {
		return plan
	}
	if plan.Reviewer != nil && plan.Reviewer.ProviderID != "" && plan.Reviewer.ProviderID != plan.Lead.ProviderID {
		return plan
	}
	type scored struct {
		c       Candidate
		score   float64
		reasons []string
	}
	alternatives := []scored{}
	for _, c := range candidates {
		if c.ModelID == plan.Lead.ModelID || c.ProviderID == plan.Lead.ProviderID {
			continue
		}
		if reasons := ValidateAssignment(c, plan.Task, "review", policy); len(reasons) != 0 {
			continue
		}
		reviewTask := plan.Task
		reviewTask.RequiredRoles = []string{"review", "analysis", "security"}
		score, why, _ := assignmentScore(c, reviewTask, "review")
		score += 20
		why = append(why, "provider_independent_review")
		alternatives = append(alternatives, scored{c: c, score: score, reasons: why})
	}
	if len(alternatives) == 0 {
		return plan
	}
	sort.SliceStable(alternatives, func(i, j int) bool {
		if alternatives[i].score != alternatives[j].score {
			return alternatives[i].score > alternatives[j].score
		}
		return alternatives[i].c.ModelID < alternatives[j].c.ModelID
	})
	x := alternatives[0]
	plan.Reviewer = &Assignment{Role: "review", ModelID: x.c.ModelID, ProviderID: x.c.ProviderID, Score: x.score, Reasons: append([]string(nil), x.reasons...), UsesProtectedReserve: usesProtectedReserve(x.c, plan.Task, policy)}
	return plan
}

func ReviewMustBlock(risk, threshold string) bool {
	threshold = strings.TrimSpace(threshold)
	if threshold == "" {
		threshold = "critical"
	}
	return rankLevel(risk) >= rankLevel(threshold)
}

// EstimateExecutionCost performs a conservative preflight estimate using the
// router's blended cost-per-million-token hint. Unknown-cost models are reported
// separately so the caller can enforce require_known_cost when desired.
func EstimateExecutionCost(plan Plan, candidates []Candidate, assistantIDs []string, inputTokens, maxOutputTokens int) (usd float64, fullyKnown bool) {
	byID := make(map[string]Candidate, len(candidates))
	for _, c := range candidates {
		byID[c.ModelID] = c
	}
	fullyKnown = true
	add := func(id string, tokens int) {
		c, ok := byID[id]
		if !ok || !c.CostKnown {
			fullyKnown = false
			return
		}
		if tokens < 1 {
			tokens = 1
		}
		usd += float64(tokens) / 1_000_000 * c.CostPerMTokens
	}
	for _, id := range assistantIDs {
		add(id, inputTokens+minInt(maxOutputTokens, 1024))
	}
	if plan.Lead != nil {
		add(plan.Lead.ModelID, inputTokens+maxOutputTokens)
	}
	if plan.Reviewer != nil {
		// Reviewer sees the task plus a bounded lead response.
		add(plan.Reviewer.ModelID, inputTokens+maxOutputTokens+minInt(maxOutputTokens, 1024))
	}
	return usd, fullyKnown
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
