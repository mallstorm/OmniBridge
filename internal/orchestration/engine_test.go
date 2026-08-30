package orchestration

import (
	"encoding/json"
	"testing"

	obr "omnibridge/internal/router"
)

func candidate(id string, roles []string) Candidate {
	return Candidate{
		ModelID: id, ProviderID: "p-" + id, ChatVerified: true, IdentityVerified: true,
		DoctorVerified: true, AgentReady: true, ToolCalling: true, Reasoning: true,
		ContextWindow: 128000, QuotaKnown: true, QuotaRemaining: 80, RouterScore: 50,
		Profile: Profile{ModelID: id, Roles: roles, RiskLimit: "critical", MinimumQuotaReserve: 20, Lead: true, Assist: true, Review: true},
	}
}

func TestClassifyComplexDebuggingAndSecurity(t *testing.T) {
	raw := []byte(`{"messages":[{"role":"user","content":"Debug this multi-file race regression in the repository"}],"tools":[{"type":"function"}]}`)
	got := Classify(raw, obr.Requirements{ToolCalling: true, AgentMode: true, MinimumContext: 12000}, "task-1")
	if got.Type != "debugging" || got.Risk != "high" || !got.RequiresStickyModel || !got.RequiresTools {
		t.Fatalf("unexpected task: %+v", got)
	}
	sec := Classify([]byte(`{"messages":[{"role":"user","content":"Audit credential handling for a security vulnerability"}]}`), obr.Requirements{}, "")
	if sec.Type != "security_review" || sec.Risk != "critical" || sec.DelegationAllowed {
		t.Fatalf("unexpected security task: %+v", sec)
	}
}

func TestClassifierIgnoresToolSchemaText(t *testing.T) {
	raw := []byte(`{"messages":[{"role":"user","content":"Summarize this note"}],"tools":[{"type":"function","function":{"name":"security_vulnerability_exploit","description":"debug architecture credentials"}}]}`)
	got := Classify(raw, obr.Requirements{}, "")
	if got.Type != "summarization" || got.Risk != "low" {
		t.Fatalf("tool schema leaked into classification: %+v", got)
	}
}

func TestPlanUsesRoleQuotaAndIndependentReviewer(t *testing.T) {
	e := New()
	lead := candidate("codex", []string{"implementation", "debugging", "testing"})
	reviewer := candidate("claude", []string{"review", "analysis", "architecture"})
	cheap := candidate("small", []string{"summarization", "documentation"})
	cheap.CostKnown, cheap.CostPerMTokens = true, 0.1
	task := Task{ID: "T", Type: "debugging", Complexity: "high", Risk: "high", RequiredRoles: []string{"debugging", "implementation"}, RequiresTools: true, RequiresStickyModel: true, DelegationAllowed: true}
	p := e.Plan(task, []Candidate{reviewer, cheap, lead}, Policy{Enabled: true, StickyTTLSeconds: 3600, ReviewerRiskThreshold: "high", MaxAssistants: 1})
	if !p.Valid || p.Lead == nil || p.Lead.ModelID != "codex" {
		b, _ := json.Marshal(p)
		t.Fatalf("lead plan failed: %s", b)
	}
	if p.Reviewer == nil || p.Reviewer.ModelID != "claude" || p.Reviewer.ModelID == p.Lead.ModelID {
		t.Fatalf("independent reviewer missing: %+v", p)
	}
}

func TestProtectedReserveRejectsRoutineLeadButCriticalCanBeConfigured(t *testing.T) {
	e := New()
	c := candidate("premium", []string{"analysis", "security"})
	c.QuotaRemaining = 20
	medium := Task{Type: "general", Risk: "medium", RequiredRoles: []string{"analysis"}}
	p := e.Plan(medium, []Candidate{c}, Policy{Enabled: true, ReviewerRiskThreshold: "critical"})
	if p.Valid || p.Lead != nil {
		t.Fatalf("routine task consumed protected reserve: %+v", p)
	}
	critical := Task{Type: "security_review", Risk: "critical", RequiredRoles: []string{"security"}}
	p = e.Plan(critical, []Candidate{c}, Policy{Enabled: true, ReviewerRiskThreshold: "critical", AllowCriticalReserveUse: true})
	// Reviewer is required at critical, so a single-model team must still fail closed.
	if p.Valid || p.Lead == nil || p.Reviewer != nil {
		t.Fatalf("critical reserve/reviewer policy mismatch: %+v", p)
	}
}

func TestStickyOwnershipReusedWhileEligible(t *testing.T) {
	e := New()
	a := candidate("a", []string{"debugging"})
	b := candidate("b", []string{"debugging"})
	a.RouterScore = 100
	b.RouterScore = 50
	task := Task{ID: "sticky", Type: "debugging", Risk: "high", RequiredRoles: []string{"debugging"}, RequiresStickyModel: true}
	pol := Policy{Enabled: true, StickyTTLSeconds: 3600, ReviewerRiskThreshold: "critical"}
	first := e.Plan(task, []Candidate{a, b}, pol)
	if first.Lead == nil || first.Lead.ModelID != "a" {
		t.Fatalf("first lead=%+v", first.Lead)
	}
	a.RouterScore = 1
	b.RouterScore = 200
	second := e.Plan(task, []Candidate{a, b}, pol)
	if second.Lead == nil || second.Lead.ModelID != "a" || !second.StickyReused {
		t.Fatalf("sticky ownership not reused: %+v", second)
	}
}

func TestHighRiskToolTaskRequiresVerifiedAgent(t *testing.T) {
	e := New()
	c := candidate("weak", []string{"implementation"})
	c.IdentityVerified = false
	c.AgentReady = false
	task := Task{Type: "implementation", Risk: "high", RequiredRoles: []string{"implementation"}, RequiresTools: true}
	p := e.Plan(task, []Candidate{c}, Policy{Enabled: true, ReviewerRiskThreshold: "critical"})
	if p.Valid || len(p.Rejected["weak"]) == 0 {
		t.Fatalf("unverified agent was accepted: %+v", p)
	}
}

func TestHighRiskRequiresDoctorProof(t *testing.T) {
	e := New()
	c := candidate("identity-only", []string{"analysis"})
	c.DoctorVerified = false
	task := Task{Type: "general", Risk: "high", RequiredRoles: []string{"analysis"}}
	p := e.Plan(task, []Candidate{c}, Policy{Enabled: true, ReviewerRiskThreshold: "critical"})
	if p.Valid || len(p.Rejected[c.ModelID]) == 0 {
		t.Fatalf("high-risk candidate without Doctor proof accepted: %+v", p)
	}
}

func TestCriticalReserveUseIsExplicitlyMarked(t *testing.T) {
	e := New()
	lead := candidate("security-lead", []string{"security"})
	lead.QuotaRemaining = lead.Profile.MinimumQuotaReserve
	reviewer := candidate("reviewer", []string{"review", "analysis", "security"})
	reviewer.Profile.Lead = false
	reviewer.QuotaRemaining = 90
	task := Task{Type: "security_review", Risk: "critical", RequiredRoles: []string{"security"}}
	p := e.Plan(task, []Candidate{lead, reviewer}, Policy{Enabled: true, ReviewerRiskThreshold: "critical", AllowCriticalReserveUse: true})
	if !p.Valid || p.Lead == nil || !p.Lead.UsesProtectedReserve {
		t.Fatalf("critical reserve use not surfaced: %+v", p)
	}
}

func TestAssistantMustMatchTaskRole(t *testing.T) {
	e := New()
	lead := candidate("lead", []string{"implementation"})
	unrelated := candidate("unrelated", []string{"summarization"})
	task := Task{Type: "implementation", Risk: "medium", RequiredRoles: []string{"implementation"}, DelegationAllowed: true}
	p := e.Plan(task, []Candidate{lead, unrelated}, Policy{Enabled: true, ReviewerRiskThreshold: "critical", MaxAssistants: 1})
	if !p.Valid || len(p.Assistants) != 0 {
		t.Fatalf("unrelated assistant was delegated: %+v", p)
	}
}
