package orchestration

import (
	"path/filepath"
	"testing"

	"omnibridge/internal/storage"
)

func TestServicePersistsConfigAndMatchesProfiles(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "team.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := NewService(db)
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Execution.Enabled = true
	cfg.Execution.MaxCalls = 4
	cfg.Profiles["native-x"] = Profile{Roles: []string{"Debugging", "Review", "debugging"}, RiskLimit: "high", MinimumQuotaReserve: 25, Lead: true, Review: true}
	if err := s.Update(cfg); err != nil {
		t.Fatal(err)
	}
	p, ok, key := s.ProfileFor("registry-x", "native-x", "provider-x")
	if !ok || key != "native-x" || len(p.Roles) != 2 || p.Roles[0] != "debugging" {
		t.Fatalf("profile=%+v key=%s", p, key)
	}
	restarted := NewService(db)
	got := restarted.Config()
	if !got.Enabled || !got.Execution.Enabled || got.Execution.MaxCalls != 4 {
		t.Fatalf("config did not survive restart: %+v", got)
	}
	if _, ok, _ := restarted.ProfileFor("registry-x", "native-x", "provider-x"); !ok {
		t.Fatal("persisted profile not restored")
	}
}

func TestExecutionIsDisabledByDefault(t *testing.T) {
	cfg := NewService(nil).Config()
	if cfg.Enabled || cfg.Execution.Enabled {
		t.Fatalf("planning/execution must be disabled by default: %+v", cfg)
	}
	if cfg.Execution.MaxCalls != 3 || cfg.Execution.MaxWallTimeSeconds != 90 {
		t.Fatalf("unexpected bounded defaults: %+v", cfg.Execution)
	}
}

func TestDisablingPlanningForcesExecutionOff(t *testing.T) {
	s := NewService(nil)
	cfg := DefaultConfig()
	cfg.Execution.Enabled = true
	if err := s.Update(cfg); err != nil {
		t.Fatal(err)
	}
	if s.Config().Execution.Enabled {
		t.Fatal("execution remained enabled while planning was disabled")
	}
}

func TestServiceRejectsInvalidReserve(t *testing.T) {
	s := NewService(nil)
	cfg := DefaultConfig()
	cfg.Profiles["m"] = Profile{Roles: []string{"analysis"}, RiskLimit: "high", MinimumQuotaReserve: 101}
	if err := s.Update(cfg); err == nil {
		t.Fatal("invalid reserve accepted")
	}
}

func TestServiceRejectsInvalidRiskLevels(t *testing.T) {
	s := NewService(nil)
	cfg := DefaultConfig()
	cfg.Policy.ReviewerRiskThreshold = "banana"
	if err := s.Update(cfg); err == nil {
		t.Fatal("invalid reviewer threshold accepted")
	}
	cfg = DefaultConfig()
	cfg.Profiles["m"] = Profile{Roles: []string{"analysis"}, RiskLimit: "banana"}
	if err := s.Update(cfg); err == nil {
		t.Fatal("invalid profile risk limit accepted")
	}
}

func TestServiceRejectsUnboundedExecutionLimits(t *testing.T) {
	s := NewService(nil)
	cases := []Config{}
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Execution.MaxCalls = 7
	cases = append(cases, cfg)
	cfg = DefaultConfig()
	cfg.Enabled = true
	cfg.Execution.MaxWallTimeSeconds = 301
	cases = append(cases, cfg)
	cfg = DefaultConfig()
	cfg.Enabled = true
	cfg.Execution.MaxInputBytes = 33 << 20
	cases = append(cases, cfg)
	cfg = DefaultConfig()
	cfg.Enabled = true
	cfg.Execution.BlockOnReviewConcernsAt = "banana"
	cases = append(cases, cfg)
	for i, c := range cases {
		if err := s.Update(c); err == nil {
			t.Fatalf("invalid execution case %d accepted: %+v", i, c.Execution)
		}
	}
}

func TestPreferIndependentReviewerAndCostEstimate(t *testing.T) {
	lead := candidate("lead", []string{"analysis"})
	lead.ProviderID = "p1"
	same := candidate("same", []string{"review", "analysis"})
	same.ProviderID = "p1"
	other := candidate("other", []string{"review", "analysis"})
	other.ProviderID = "p2"
	other.RouterScore = same.RouterScore - 5
	plan := Plan{
		Task: Task{Risk: "high", RequiredRoles: []string{"analysis"}},
		Valid: true,
		Lead: &Assignment{Role: "lead", ModelID: lead.ModelID, ProviderID: lead.ProviderID},
		Reviewer: &Assignment{Role: "review", ModelID: same.ModelID, ProviderID: same.ProviderID},
		ReviewerRequired: true,
	}
	plan = PreferIndependentReviewer(plan, []Candidate{lead, same, other}, Policy{Enabled: true, ReviewerRiskThreshold: "high"})
	if plan.Reviewer == nil || plan.Reviewer.ModelID != "other" {
		t.Fatalf("different-provider reviewer was not preferred: %+v", plan.Reviewer)
	}
	lead.CostKnown, lead.CostPerMTokens = true, 2
	other.CostKnown, other.CostPerMTokens = true, 4
	usd, known := EstimateExecutionCost(plan, []Candidate{lead, other}, nil, 1000, 1000)
	if !known || usd <= 0 {
		t.Fatalf("cost estimate invalid usd=%f known=%v", usd, known)
	}
	if !ReviewMustBlock("critical", "critical") || ReviewMustBlock("high", "critical") {
		t.Fatal("review concern threshold is not monotonic")
	}
}
