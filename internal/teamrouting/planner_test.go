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
		{ID: "same", ProviderID: "p1", Family: "p1:gpt", DoctorVerified: true, ReliabilityScore: 99, DoctorScore: 99, AgentScore: 95},
		{ID: "independent", ProviderID: "p2", Family: "p2:claude", DoctorVerified: true, ReliabilityScore: 88, DoctorScore: 90, AgentScore: 90},
	}, DefaultProfile("judge"), History{Providers: []string{"p1"}, Families: []string{"p1:gpt"}}, DefaultPolicy())
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
		{ID: "gpt", ProviderID: "openai", Family: "openai:gpt", ReliabilityScore: 95, DoctorScore: 95},
		{ID: "o3", ProviderID: "openai", Family: "openai:o-series", ReliabilityScore: 90, DoctorScore: 90},
	}, DefaultProfile("critic"), History{Families: []string{"openai:gpt"}}, p)
	if len(plan.Candidates) == 0 || plan.Candidates[0].Candidate.ID != "o3" {
		t.Fatalf("family plan=%+v", plan.Candidates)
	}
}

func TestDiversityFallbackCanBeDisabled(t *testing.T) {
	p := DefaultPolicy()
	p.AllowDiversityFallback = false
	plan := Rank([]Candidate{{ID: "a", ProviderID: "p1", Family: "p1:gpt", CodingScore: 99}}, DefaultProfile("developer"), History{Providers: []string{"p1"}}, p)
	if len(plan.Candidates) != 0 {
		t.Fatalf("expected no fallback candidates, got %+v", plan.Candidates)
	}
}

func TestInferFamily(t *testing.T) {
	if got := InferFamily("openai", "gpt-4.1-mini", ""); got != "openai:gpt" {
		t.Fatalf("family=%q", got)
	}
	if got := InferFamily("openai", "o3-mini", ""); got != "openai:o-series" {
		t.Fatalf("family=%q", got)
	}
}
