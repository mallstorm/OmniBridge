package orchestration

import (
	"context"
	"testing"
)

type historyExecutor struct {
	calls []Call
}

func (f *historyExecutor) Execute(_ context.Context, c Call) (CallResult, error) {
	f.calls = append(f.calls, c)
	switch c.Step.Role {
	case RoleDeveloper:
		return CallResult{Output: "draft", Model: "p1/gpt", Provider: "p1", Family: "p1:gpt", Routing: RoutingDecision{Profile: "developer", Family: "p1:gpt", CandidateRank: 1, CandidateCount: 3, Rationale: []string{"role profile=developer"}}}, nil
	case RoleCritic:
		return CallResult{Output: "critique", Model: "p2/claude", Provider: "p2", Family: "p2:claude", Routing: RoutingDecision{Profile: "critic", Family: "p2:claude", CandidateRank: 1, CandidateCount: 2}}, nil
	case RoleFixer:
		return CallResult{Output: "fixed", Model: "p3/gemini", Provider: "p3", Family: "p3:gemini", Routing: RoutingDecision{Profile: "fixer", Family: "p3:gemini", CandidateRank: 1, CandidateCount: 2}}, nil
	default:
		return CallResult{Output: "PASS", Model: "p4/o3", Provider: "p4", Family: "p4:o-series", Routing: RoutingDecision{Profile: "judge", Family: "p4:o-series", CandidateRank: 1, CandidateCount: 2}}, nil
	}
}

func TestEngineCarriesTeamHistoryAndPersistsRoutingDecision(t *testing.T) {
	f := &historyExecutor{}
	e := Engine{Registry: NewRegistry(), Executor: f, Verdicts: StrictVerdictParser{}}
	ex, err := e.Run(context.Background(), CodingReviewModel, Request{ID: "team", Input: "task"})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 4 || len(ex.Steps) != 4 {
		t.Fatalf("calls=%d steps=%d", len(f.calls), len(ex.Steps))
	}
	critic := f.calls[1]
	if len(critic.AvoidProviders) != 1 || critic.AvoidProviders[0] != "p1" || len(critic.AvoidFamilies) != 1 || critic.AvoidFamilies[0] != "p1:gpt" {
		t.Fatalf("critic history=%+v", critic)
	}
	reviewer := f.calls[3]
	if len(reviewer.AvoidProviders) != 3 || len(reviewer.AvoidFamilies) != 3 || len(reviewer.AvoidModels) != 3 {
		t.Fatalf("reviewer history=%+v", reviewer)
	}
	if ex.Steps[3].Routing.Profile != "judge" || ex.Steps[3].Routing.Family != "p4:o-series" {
		t.Fatalf("routing decision not persisted: %+v", ex.Steps[3].Routing)
	}
}

func TestBuiltinCodingReviewDeclaresRoleProfilesAndTeamPolicies(t *testing.T) {
	wf := BuiltinCodingReview()
	profiles := map[Role]string{}
	for _, step := range wf.Steps {
		profiles[step.Role] = step.Selector.Profile
	}
	if profiles[RoleDeveloper] != "developer" || profiles[RoleCritic] != "critic" || profiles[RoleFixer] != "fixer" || profiles[RoleReviewer] != "judge" {
		t.Fatalf("profiles=%v", profiles)
	}
	if wf.Policy.Fallback.MaxCandidates != 4 || !wf.Policy.Fallback.AllowDiversityFallback || wf.Policy.Fallback.QuotaFloorPercent != 5 {
		t.Fatalf("fallback policy=%+v", wf.Policy.Fallback)
	}
	if !wf.Policy.Judge.PreferDifferentProvider || !wf.Policy.Judge.PreferDifferentFamily {
		t.Fatalf("judge policy=%+v", wf.Policy.Judge)
	}
}
