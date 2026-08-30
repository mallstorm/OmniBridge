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
	cfg.Profiles["native-x"] = Profile{Roles: []string{"Debugging", "Review", "debugging"}, RiskLimit: "high", MinimumQuotaReserve: 25, Lead: true, Review: true}
	if err := s.Update(cfg); err != nil {
		t.Fatal(err)
	}
	p, ok, key := s.ProfileFor("registry-x", "native-x", "provider-x")
	if !ok || key != "native-x" || len(p.Roles) != 2 || p.Roles[0] != "debugging" {
		t.Fatalf("profile=%+v key=%s", p, key)
	}
	restarted := NewService(db)
	if !restarted.Config().Enabled {
		t.Fatal("config did not survive restart")
	}
	if _, ok, _ := restarted.ProfileFor("registry-x", "native-x", "provider-x"); !ok {
		t.Fatal("persisted profile not restored")
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
