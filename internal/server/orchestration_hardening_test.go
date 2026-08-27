package server

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"omnibridge/internal/config"
	"omnibridge/internal/orchestration"
	"omnibridge/internal/quotacost"
	"omnibridge/internal/storage"
)

func TestOrchestrationExecutorRejectsExceededBudgetBeforeRoleCall(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.New(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	qcfg := config.QuotaCostConfig{
		Enabled:         true,
		LowQuotaPercent: 10,
		RejectOnBudget:  true,
		Budget:          config.BudgetConfig{SessionUSD: 0.000001},
	}
	if err := cfg.SetQuotaCost(qcfg); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(filepath.Join(dir, "omnibridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	qc := quotacost.New(db, qcfg)
	if err := qc.UpsertPrice(quotacost.Price{Provider: "mock", Model: "model-a", InputUSDPerMillion: 1, OutputUSDPerMillion: 1, Currency: "USD", Confidence: "operator"}); err != nil {
		t.Fatal(err)
	}
	seed := qc.RecordCost("seed-cost", "mock", "model-a", "mock", 10, 0, 0)
	if !seed.Known || seed.CostUSD <= qcfg.Budget.SessionUSD {
		t.Fatalf("seed cost did not exceed test budget: %+v", seed)
	}

	s := &Server{Config: cfg, QuotaCost: qc}
	_, err = (orchestrationExecutor{server: s}).Execute(context.Background(), orchestration.Call{
		Step: orchestration.Step{ID: "critic", Role: orchestration.RoleCritic},
	})
	if err == nil || !strings.Contains(err.Error(), "workflow_budget_exceeded") {
		t.Fatalf("expected workflow_budget_exceeded before routing, got %v", err)
	}
}
