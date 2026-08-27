package server

import (
	"path/filepath"
	"testing"

	"omnibridge/internal/config"
	"omnibridge/internal/orchestration"
	"omnibridge/internal/providers"
	"omnibridge/internal/quotacost"
	"omnibridge/internal/stats"
	"omnibridge/internal/storage"
)

func teamTestServer(t *testing.T) (*Server, *quotacost.Engine) {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.New(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(filepath.Join(dir, "omnibridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	qcfg := config.QuotaCostConfig{Enabled: true, LowQuotaPercent: 10, PricingRefreshHours: 24}
	qc := quotacost.New(db, qcfg)
	return &Server{Config: cfg, Stats: stats.New(filepath.Join(dir, "stats.json")), QuotaCost: qc}, qc
}

func workflowStep(t *testing.T, role orchestration.Role) orchestration.Step {
	t.Helper()
	for _, step := range orchestration.BuiltinCodingReview().Steps {
		if step.Role == role {
			return step
		}
	}
	t.Fatalf("missing role %s", role)
	return orchestration.Step{}
}

func TestOrchestrationTeamPlanUsesQuotaAndCostForFixer(t *testing.T) {
	srv, qc := teamTestServer(t)
	if err := qc.SetQuota(quotacost.Snapshot{ConnectionID: "p1", RemainingKnown: true, RemainingPercent: 2, Confidence: "operator"}); err != nil {
		t.Fatal(err)
	}
	if err := qc.SetQuota(quotacost.Snapshot{ConnectionID: "p2", RemainingKnown: true, RemainingPercent: 85, Confidence: "operator"}); err != nil {
		t.Fatal(err)
	}
	if err := qc.UpsertPrice(quotacost.Price{Provider: "p1", Model: "model-a", InputUSDPerMillion: 20, OutputUSDPerMillion: 20, Currency: "USD", Confidence: "operator"}); err != nil {
		t.Fatal(err)
	}
	if err := qc.UpsertPrice(quotacost.Price{Provider: "p2", Model: "model-b", InputUSDPerMillion: 2, OutputUSDPerMillion: 2, Currency: "USD", Confidence: "operator"}); err != nil {
		t.Fatal(err)
	}

	wf := orchestration.BuiltinCodingReview()
	call := orchestration.Call{Workflow: wf, Step: workflowStep(t, orchestration.RoleFixer)}
	ordered, decisions, _ := srv.orchestrationTeamPlan(call, []providers.Model{
		{ID: "p1/model-a", ProviderID: "p1", NativeID: "model-a", CodingScore: 94, ReliabilityScore: 92, DoctorScore: 90, Status: "ready"},
		{ID: "p2/model-b", ProviderID: "p2", NativeID: "model-b", CodingScore: 91, ReliabilityScore: 90, DoctorScore: 89, Status: "ready"},
	})
	if len(ordered) != 2 || ordered[0].ProviderID != "p2" {
		t.Fatalf("ordered=%+v decisions=%+v", ordered, decisions)
	}
	if !decisions[ordered[0].ID].QuotaKnown || !decisions[ordered[0].ID].CostKnown {
		t.Fatalf("quota/cost evidence missing: %+v", decisions[ordered[0].ID])
	}
}

func TestOrchestrationTeamPlanPrefersIndependentJudge(t *testing.T) {
	srv, _ := teamTestServer(t)
	wf := orchestration.BuiltinCodingReview()
	call := orchestration.Call{
		Workflow:       wf,
		Step:           workflowStep(t, orchestration.RoleReviewer),
		AvoidProviders: []string{"p1"},
		AvoidFamilies:  []string{"p1:gpt"},
		AvoidModels:    []string{"p1/gpt-4.1"},
	}
	ordered, decisions, plan := srv.orchestrationTeamPlan(call, []providers.Model{
		{ID: "p1/gpt-4.1", ProviderID: "p1", NativeID: "gpt-4.1", DoctorVerified: true, DoctorScore: 99, AgentScore: 98, ReliabilityScore: 99, Reasoning: true, Status: "ready"},
		{ID: "p2/claude-sonnet", ProviderID: "p2", NativeID: "claude-sonnet", DoctorVerified: true, DoctorScore: 91, AgentScore: 90, ReliabilityScore: 91, Reasoning: true, Status: "ready"},
	})
	if plan.Profile.Name != "judge" || len(ordered) != 2 || ordered[0].ProviderID != "p2" {
		t.Fatalf("plan=%+v ordered=%+v", plan, ordered)
	}
	if decisions["p1/gpt-4.1"].Fallback != true {
		t.Fatalf("same-provider judge should be fallback: %+v", decisions["p1/gpt-4.1"])
	}
}
