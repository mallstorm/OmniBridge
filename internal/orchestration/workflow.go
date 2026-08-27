package orchestration

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

const CodingReviewModel = "workflow/coding-review"

type Role string

const (
	RoleDeveloper Role = "developer"
	RoleCritic    Role = "critic"
	RoleFixer     Role = "fixer"
	RoleReviewer  Role = "reviewer"
)

type DiversityPolicy string

const (
	DiversityNone     DiversityPolicy = "none"
	DiversityProvider DiversityPolicy = "provider"
	DiversityFamily   DiversityPolicy = "family"
	DiversityModel    DiversityPolicy = "model"
)

type Permission string

const (
	PermissionNone     Permission = "none"
	PermissionReadOnly Permission = "read_only"
	PermissionWrite    Permission = "write"
)

type ModelSelector struct {
	Model              string `json:"model,omitempty"`
	PreferCoding       bool   `json:"prefer_coding,omitempty"`
	PreferReasoning    bool   `json:"prefer_reasoning,omitempty"`
	PreferReliability  bool   `json:"prefer_reliability,omitempty"`
	RequireTools       bool   `json:"require_tools,omitempty"`
	RequireDoctorProof bool   `json:"require_doctor_proof,omitempty"`
}

type Step struct {
	ID          string        `json:"id"`
	Role        Role          `json:"role"`
	DependsOn   []string      `json:"depends_on,omitempty"`
	Selector    ModelSelector `json:"selector"`
	Permission  Permission    `json:"permission"`
	Prompt      string        `json:"prompt,omitempty"`
}

type Policy struct {
	MaxIterations int             `json:"max_iterations"`
	Diversity     DiversityPolicy `json:"diversity"`
}

type Workflow struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Version     int    `json:"version"`
	Enabled     bool   `json:"enabled"`
	Steps       []Step `json:"steps"`
	Policy      Policy `json:"policy"`
}

func BuiltinCodingReview() Workflow {
	return Workflow{
		ID: CodingReviewModel, Name: "Coding Review", Version: 1, Enabled: true,
		Description: "Developer -> Critic -> Fixer -> Reviewer with bounded review/fix iterations.",
		Policy: Policy{MaxIterations: 2, Diversity: DiversityProvider},
		Steps: []Step{
			{ID: "developer", Role: RoleDeveloper, Selector: ModelSelector{Model: "auto/coding", PreferCoding: true}, Permission: PermissionWrite},
			{ID: "critic", Role: RoleCritic, DependsOn: []string{"developer"}, Selector: ModelSelector{Model: "auto/reasoning", PreferReasoning: true, PreferReliability: true}, Permission: PermissionReadOnly},
			{ID: "fixer", Role: RoleFixer, DependsOn: []string{"developer", "critic"}, Selector: ModelSelector{Model: "auto/coding", PreferCoding: true}, Permission: PermissionWrite},
			{ID: "reviewer", Role: RoleReviewer, DependsOn: []string{"fixer"}, Selector: ModelSelector{Model: "auto/smart", PreferReasoning: true, PreferReliability: true, RequireDoctorProof: true}, Permission: PermissionReadOnly},
		},
	}
}

func (w Workflow) Validate() error {
	if strings.TrimSpace(w.ID) == "" { return errors.New("workflow id is required") }
	if w.Version < 1 { return errors.New("workflow version must be positive") }
	if w.Policy.MaxIterations < 1 { return errors.New("max_iterations must be positive") }
	if len(w.Steps) == 0 { return errors.New("workflow requires at least one step") }
	ids := make(map[string]Step, len(w.Steps))
	for _, step := range w.Steps {
		if strings.TrimSpace(step.ID) == "" { return errors.New("step id is required") }
		if _, ok := ids[step.ID]; ok { return fmt.Errorf("duplicate step id %q", step.ID) }
		switch step.Role { case RoleDeveloper, RoleCritic, RoleFixer, RoleReviewer: default: return fmt.Errorf("invalid role %q", step.Role) }
		ids[step.ID] = step
	}
	for _, step := range w.Steps {
		for _, dep := range step.DependsOn { if _, ok := ids[dep]; !ok { return fmt.Errorf("step %q depends on missing step %q", step.ID, dep) } }
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] { return fmt.Errorf("workflow dependency cycle at %q", id) }
		if visited[id] { return nil }
		visiting[id] = true
		for _, dep := range ids[id].DependsOn { if err := visit(dep); err != nil { return err } }
		visiting[id] = false; visited[id] = true; return nil
	}
	for id := range ids { if err := visit(id); err != nil { return err } }
	return nil
}

type Registry struct { mu sync.RWMutex; workflows map[string]Workflow }

func NewRegistry() *Registry { r := &Registry{workflows: map[string]Workflow{}}; _ = r.Register(BuiltinCodingReview()); return r }

func (r *Registry) Register(w Workflow) error {
	if err := w.Validate(); err != nil { return err }
	r.mu.Lock(); defer r.mu.Unlock(); r.workflows[w.ID] = w; return nil
}

func (r *Registry) Get(id string) (Workflow, bool) { r.mu.RLock(); defer r.mu.RUnlock(); w, ok := r.workflows[id]; return w, ok }

func (r *Registry) List() []Workflow {
	r.mu.RLock(); defer r.mu.RUnlock(); out := make([]Workflow, 0, len(r.workflows)); for _, w := range r.workflows { if w.Enabled { out = append(out, w) } }; sort.Slice(out, func(i,j int) bool { return out[i].ID < out[j].ID }); return out
}

func IsWorkflowModel(model string) bool { return strings.HasPrefix(strings.TrimSpace(model), "workflow/") }
