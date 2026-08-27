package teamrouting

import "testing"

func TestDeveloperProfilePrefersCoding(t *testing.T) {
	plan := Rank([]Candidate{
		{ID: "a", ProviderID: "p1", CodingScore: 96, ReliabilityScore: 75, DoctorScore: 80, OriginalRank: 1},
		{ID: "b", ProviderID: "p2", CodingScore: 70, ReliabilityScore: 98, DoctorScore: 95, OriginalRank: 0},
	}, DefaultProfile("developer"), History{}, DefaultPolicy())
	if len(plan.Candidates) == 0 || plan.Candidates[0].Candidate.ID != "a" {
		t.Fatalf("developer plan=%+v", plan.Candidates)
	}
}

func TestJudgePrefersIndependentProvider(t *testing.T) {
	plan := Rank([]Candidate{
		{ID: "same", ProviderID: "p1", Family: "gpt", DoctorVerified: true, ReliabilityScore: 99, DoctorScore: 99, AgentScore: 95},
		{ID: "independent", ProviderID: "p2", Family: "claude", DoctorVerified: true, ReliabilityScore: 88, DoctorScore: 90, AgentScore: 90},
	}, DefaultProfile("judge"), History{Providers: []string{"p1"}, Families: []string{"gpt"}}, DefaultPolicy())
	if len(plan.Candidates) == 0 || plan.Candidates[0].Candidate.ID != "independent" {
		t.Fatalf("judge plan=%+v", plan.Candidates)
	}
}

func TestQuotaAndCostInfluenceNearEqualCandidates(t *testing.T) {
	p := DefaultPolicy()
	plan := Rank([]Candidate{
		{ID: "expensive-low", ProviderID: "p1", CodingScore: 90, ReliabilityScore: 90, DoctorScore: 90, QuotaKnown: true, QuotaRemaining: 3, CostKnown: true, CostPerMTokens: 40},
		{ID: "cheap-healthy", ProviderID: "p2", CodingScore: 89, ReliabilityScore: 89, DoctorScore: 89, QuotaKnown: true, QuotaRemaining: 80, CostKnown: true, CostPerMTokens: 4},
	}, DefaultProfile("fixer"), History{}, p)
	if len(plan.Candidates) == 0 || plan.Candidates[0].Candidate.ID != "cheap-healthy" {
		t.Fatalf("quota/cost plan=%+v", plan.Candidates)
	}
}

func TestFamilyDiversityWithinProvider(t *testing.T) {
	p := DefaultPolicy()
	p.Diversity = "family"
	plan := Rank([]Candidate{
		{ID: "gpt", ProviderID: "openai", Family: "gpt", ReliabilityScore: 95, DoctorScore: 95},
		{ID: "o3", ProviderID: "openai", Family: "o-series", ReliabilityScore: 90, DoctorScore: 90},
	}, DefaultProfile("critic"), History{Families: []string{"gpt"}}, p)
	if len(plan.Candidates) == 0 || plan.Candidates[0].Candidate.ID != "o3" {
		t.Fatalf("family plan=%+v", plan.Candidates)
	}
}

func TestFamilyDiversityAcrossProvidersTreatsSameModelLineAsCorrelated(t *testing.T) {
	p := DefaultPolicy()
	p.Diversity = "family"
	plan := Rank([]Candidate{
		{ID: "direct/gpt", ProviderID: "openai", Family: InferFamily("openai", "gpt-4.1", ""), ReliabilityScore: 99, DoctorScore: 99},
		{ID: "gateway/gpt", ProviderID: "openrouter", Family: InferFamily("openrouter", "gpt-4.1", ""), ReliabilityScore: 98, DoctorScore: 98},
		{ID: "gateway/claude", ProviderID: "openrouter", Family: InferFamily("openrouter", "claude-sonnet", ""), ReliabilityScore: 90, DoctorScore: 90},
	}, DefaultProfile("critic"), History{Families: []string{"gpt"}}, p)
	if len(plan.Candidates) == 0 || plan.Candidates[0].Candidate.ID != "gateway/claude" {
		t.Fatalf("cross-provider family diversity plan=%+v", plan.Candidates)
	}
}

func TestDiversityFallbackCanBeDisabled(t *testing.T) {
	p := DefaultPolicy()
	p.AllowDiversityFallback = false
	plan := Rank([]Candidate{{ID: "a", ProviderID: "p1", Family: "gpt", CodingScore: 99}}, DefaultProfile("developer"), History{Providers: []string{"p1"}}, p)
	if len(plan.Candidates) != 0 {
		t.Fatalf("expected no fallback candidates, got %+v", plan.Candidates)
	}
}

func TestInferFamily(t *testing.T) {
	if got := InferFamily("openai", "gpt-4.1-mini", ""); got != "gpt" {
		t.Fatalf("family=%q", got)
	}
	if got := InferFamily("openrouter", "gpt-4.1-mini", ""); got != "gpt" {
		t.Fatalf("cross-provider family=%q", got)
	}
	if got := InferFamily("openai", "o3-mini", ""); got != "o-series" {
		t.Fatalf("family=%q", got)
	}
	if got := InferFamily("private", "custom-alpha", ""); got != "private:custom" {
		t.Fatalf("unknown family=%q", got)
	}
}
