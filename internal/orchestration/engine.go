package orchestration

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	obr "omnibridge/internal/router"
)

const Version = "1.0"

type Task struct {
	ID                  string   `json:"id,omitempty"`
	Type                string   `json:"task_type"`
	Complexity          string   `json:"complexity"`
	Risk                string   `json:"risk"`
	RequiredRoles       []string `json:"required_roles"`
	RequiresTools       bool     `json:"requires_tools,omitempty"`
	RequiresReasoning   bool     `json:"requires_reasoning,omitempty"`
	RequiresVision      bool     `json:"requires_vision,omitempty"`
	RequiredContext     int      `json:"required_context_tokens,omitempty"`
	RequiresStickyModel bool     `json:"requires_sticky_model,omitempty"`
	DelegationAllowed   bool     `json:"delegation_allowed"`
}

type Profile struct {
	ModelID             string   `json:"model_id"`
	Roles               []string `json:"roles"`
	RiskLimit           string   `json:"risk_limit"`
	MinimumQuotaReserve float64  `json:"minimum_quota_reserve,omitempty"`
	Lead                bool     `json:"lead"`
	Assist              bool     `json:"assist"`
	Review              bool     `json:"review"`
	Priority            float64  `json:"priority,omitempty"`
}

type Candidate struct {
	ModelID          string  `json:"model_id"`
	ProviderID       string  `json:"provider_id,omitempty"`
	ChatVerified     bool    `json:"chat_verified"`
	IdentityVerified bool    `json:"identity_verified"`
	DoctorVerified   bool    `json:"doctor_verified"`
	AgentReady       bool    `json:"agent_ready"`
	ToolCalling      bool    `json:"tool_calling"`
	Reasoning        bool    `json:"reasoning"`
	Vision           bool    `json:"vision"`
	ContextWindow    int     `json:"context_window,omitempty"`
	HealthRank       int     `json:"health_rank"`
	QuotaKnown       bool    `json:"quota_known"`
	QuotaRemaining   float64 `json:"quota_remaining,omitempty"`
	CostKnown        bool    `json:"cost_known"`
	CostPerMTokens   float64 `json:"cost_per_m_tokens,omitempty"`
	RouterScore      float64 `json:"router_score,omitempty"`
	Profile          Profile `json:"profile"`
}

type Policy struct {
	Enabled                 bool   `json:"enabled"`
	StickyTTLSeconds        int    `json:"sticky_ttl_seconds,omitempty"`
	ReviewerRiskThreshold   string `json:"reviewer_risk_threshold,omitempty"`
	MaxAssistants           int    `json:"max_assistants,omitempty"`
	AllowCriticalReserveUse bool   `json:"allow_critical_reserve_use,omitempty"`
}

type Assignment struct {
	Role                 string   `json:"role"`
	ModelID              string   `json:"model_id"`
	ProviderID           string   `json:"provider_id,omitempty"`
	Score                float64  `json:"score"`
	Reasons              []string `json:"reasons"`
	UsesProtectedReserve bool     `json:"uses_protected_reserve,omitempty"`
}

type Plan struct {
	Version          string              `json:"version"`
	Task             Task                `json:"task"`
	Valid            bool                `json:"valid"`
	Lead             *Assignment         `json:"lead,omitempty"`
	Assistants       []Assignment        `json:"assistants"`
	Reviewer         *Assignment         `json:"reviewer,omitempty"`
	ReviewerRequired bool                `json:"reviewer_required"`
	StickyReused     bool                `json:"sticky_reused,omitempty"`
	Rejected         map[string][]string `json:"rejected,omitempty"`
	Warnings         []string            `json:"warnings,omitempty"`
}

type binding struct {
	ModelID   string
	ExpiresAt time.Time
}

type Engine struct {
	mu       sync.Mutex
	bindings map[string]binding
}

func New() *Engine { return &Engine{bindings: map[string]binding{}} }

func rankLevel(v string) int {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "low":
		return 1
	case "medium":
		return 2
	case "high":
		return 3
	case "critical":
		return 4
	default:
		return 2
	}
}

func validRisk(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "low", "medium", "high", "critical":
		return true
	default:
		return false
	}
}

func normalizedRisk(v string) string {
	if validRisk(v) {
		return strings.ToLower(strings.TrimSpace(v))
	}
	return "medium"
}

func flattenText(raw []byte) string {
	var root any
	if json.Unmarshal(raw, &root) != nil {
		return ""
	}
	var parts []string
	var walk func(any, string)
	walk = func(v any, key string) {
		switch x := v.(type) {
		case string:
			lk := strings.ToLower(key)
			if lk == "content" || lk == "input" || lk == "instructions" || lk == "system" || lk == "prompt" {
				parts = append(parts, x)
			}
		case []any:
			for _, item := range x {
				walk(item, key)
			}
		case map[string]any:
			for k, item := range x {
				// Never classify on tool schema names/descriptions. They describe
				// integration mechanics, not the user's actual task intent.
				if strings.EqualFold(k, "tools") || strings.EqualFold(k, "functions") {
					continue
				}
				walk(item, k)
			}
		}
	}
	walk(root, "")
	return strings.ToLower(strings.Join(parts, "\n"))
}

func containsAny(s string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(s, term) {
			return true
		}
	}
	return false
}

// Classify is deterministic and side-effect free. Prompt text is used only in
// memory to derive the task class and is never included in the returned Task.
func Classify(raw []byte, req obr.Requirements, taskID string) Task {
	text := flattenText(raw)
	t := Task{ID: strings.TrimSpace(taskID), Type: "general", Complexity: "medium", Risk: "medium", DelegationAllowed: true, RequiredContext: req.MinimumContext, RequiresTools: req.ToolCalling || req.AgentMode, RequiresReasoning: req.Reasoning, RequiresVision: req.Vision}

	switch {
	case containsAny(text, "security", "credential", "secret", "auth", "vulnerability", "cve", "exploit", "шифр", "уязв", "безопасност"):
		t.Type, t.Risk, t.Complexity = "security_review", "critical", "high"
		t.RequiredRoles = []string{"security", "review"}
	case containsAny(text, "architecture", "architect", "design system", "system design", "архитектур", "проектирован"):
		t.Type, t.Risk, t.Complexity = "architecture", "high", "high"
		t.RequiredRoles = []string{"architecture", "analysis"}
		t.RequiresReasoning = true
	case containsAny(text, "debug", "bug", "stack trace", "panic", "exception", "regression", "ошибк", "баг", "отлад"):
		t.Type = "debugging"
		t.RequiredRoles = []string{"debugging", "implementation"}
		if containsAny(text, "multi-file", "race", "deadlock", "corrupt", "production", "несколько файл", "гонк", "в прод") || req.ToolCalling {
			t.Risk, t.Complexity = "high", "high"
		}
	case containsAny(text, "implement", "refactor", "add feature", "write code", "patch", "реализ", "рефактор", "добавь функ", "исправь код"):
		t.Type = "implementation"
		t.RequiredRoles = []string{"implementation", "testing"}
		if req.ToolCalling || containsAny(text, "multi-file", "repository", "repo", "несколько файл", "репозитор") {
			t.Risk, t.Complexity = "high", "high"
		}
	case containsAny(text, "test", "benchmark", "regression test", "тест", "бенчмарк"):
		t.Type, t.RequiredRoles = "testing", []string{"testing"}
	case containsAny(text, "review", "audit", "check diff", "code review", "проверь", "аудит", "ревью"):
		t.Type, t.RequiredRoles = "review", []string{"review", "analysis"}
		t.Risk = "high"
	case containsAny(text, "research", "compare", "find documentation", "documentation search", "исслед", "сравни", "найди документац"):
		t.Type, t.RequiredRoles = "research", []string{"research", "large_context"}
	case containsAny(text, "summarize", "summary", "condense", "суммар", "кратко излож", "резюме"):
		t.Type, t.Risk, t.Complexity = "summarization", "low", "low"
		t.RequiredRoles = []string{"summarization"}
	case containsAny(text, "readme", "documentation", "docs", "release notes", "документац", "описание"):
		t.Type, t.Risk = "documentation", "low"
		t.RequiredRoles = []string{"documentation"}
	default:
		t.RequiredRoles = []string{"analysis"}
	}

	if req.MinimumContext >= 64000 {
		t.RequiredRoles = appendUnique(t.RequiredRoles, "large_context")
		if t.Complexity == "low" {
			t.Complexity = "medium"
		}
	}
	if req.Reasoning {
		t.RequiredRoles = appendUnique(t.RequiredRoles, "analysis")
	}
	if req.ToolCalling || req.AgentMode {
		if rankLevel(t.Risk) < rankLevel("high") {
			t.Risk = "high"
		}
		if t.Complexity == "low" {
			t.Complexity = "medium"
		}
	}
	t.Risk = normalizedRisk(t.Risk)
	t.RequiresStickyModel = rankLevel(t.Risk) >= rankLevel("high") || req.AgentMode || req.ToolCalling
	if t.Risk == "critical" {
		t.DelegationAllowed = false
	}
	return t
}

func appendUnique(in []string, value string) []string {
	for _, x := range in {
		if x == value {
			return in
		}
	}
	return append(in, value)
}

func roleMatch(profile Profile, roles []string) (string, bool) {
	for _, wanted := range roles {
		for _, have := range profile.Roles {
			if strings.EqualFold(strings.TrimSpace(wanted), strings.TrimSpace(have)) {
				return strings.ToLower(strings.TrimSpace(wanted)), true
			}
		}
	}
	return "", false
}

func eligibility(c Candidate, task Task, role string, policy Policy) ([]string, bool) {
	reasons := []string{}
	if c.HealthRank > 0 {
		reasons = append(reasons, "health_gate")
	}
	if !c.ChatVerified {
		reasons = append(reasons, "chat_verification_required")
	}
	if rankLevel(task.Risk) >= rankLevel("high") && !c.IdentityVerified {
		reasons = append(reasons, "identity_verification_required")
	}
	if rankLevel(task.Risk) >= rankLevel("high") && !c.DoctorVerified {
		reasons = append(reasons, "doctor_verification_required")
	}
	if task.RequiresTools && (!c.AgentReady || !c.ToolCalling || !c.IdentityVerified) {
		reasons = append(reasons, "agent_tools_verification_required")
	}
	if task.RequiresReasoning && !c.Reasoning {
		reasons = append(reasons, "reasoning_capability_required")
	}
	if task.RequiresVision && !c.Vision {
		reasons = append(reasons, "vision_capability_required")
	}
	if task.RequiredContext > 0 && c.ContextWindow > 0 && c.ContextWindow < task.RequiredContext {
		reasons = append(reasons, "context_window_insufficient")
	}
	if rankLevel(task.Risk) > rankLevel(c.Profile.RiskLimit) {
		reasons = append(reasons, "risk_limit_exceeded")
	}
	if role == "lead" && !c.Profile.Lead {
		reasons = append(reasons, "lead_role_disabled")
	}
	if role == "assist" && !c.Profile.Assist {
		reasons = append(reasons, "assistant_role_disabled")
	}
	if role == "review" && !c.Profile.Review {
		reasons = append(reasons, "review_role_disabled")
	}
	if role == "review" && !c.IdentityVerified {
		reasons = append(reasons, "review_identity_required")
	}
	if role == "review" && !c.DoctorVerified {
		reasons = append(reasons, "review_doctor_verification_required")
	}
	if c.QuotaKnown && c.QuotaRemaining <= c.Profile.MinimumQuotaReserve {
		if !(task.Risk == "critical" && policy.AllowCriticalReserveUse) {
			reasons = append(reasons, "protected_quota_reserve")
		}
	}
	return reasons, len(reasons) == 0
}

func assignmentScore(c Candidate, task Task, role string) (float64, []string, bool) {
	score := c.RouterScore + c.Profile.Priority
	reasons := []string{}
	if matched, ok := roleMatch(c.Profile, task.RequiredRoles); ok {
		score += 100
		reasons = append(reasons, "role_match:"+matched)
	} else if role == "lead" || role == "assist" {
		return score, append(reasons, "required_role_mismatch"), false
	}
	if c.DoctorVerified {
		score += 12
		reasons = append(reasons, "doctor_verified")
	}
	if c.IdentityVerified {
		score += 8
		reasons = append(reasons, "identity_verified")
	}
	if c.AgentReady && task.RequiresTools {
		score += 12
		reasons = append(reasons, "agent_ready")
	}
	if c.QuotaKnown {
		headroom := c.QuotaRemaining - c.Profile.MinimumQuotaReserve
		if headroom > 0 {
			score += headroom * .25
			reasons = append(reasons, "quota_above_reserve")
		}
	}
	if role == "assist" && c.CostKnown {
		score += 12 / (1 + c.CostPerMTokens)
		reasons = append(reasons, "cost_efficiency")
	}
	return score, reasons, true
}

func usesProtectedReserve(c Candidate, task Task, policy Policy) bool {
	return c.QuotaKnown && c.QuotaRemaining <= c.Profile.MinimumQuotaReserve && task.Risk == "critical" && policy.AllowCriticalReserveUse
}

func reviewerRequired(task Task, policy Policy) bool {
	threshold := normalizedRisk(policy.ReviewerRiskThreshold)
	return rankLevel(task.Risk) >= rankLevel(threshold)
}

func stickyTTL(policy Policy) time.Duration {
	sec := policy.StickyTTLSeconds
	if sec < 60 {
		sec = 7200
	}
	return time.Duration(sec) * time.Second
}

func (e *Engine) pinned(taskID string, now time.Time) string {
	if strings.TrimSpace(taskID) == "" {
		return ""
	}
	key := strings.TrimSpace(taskID)
	e.mu.Lock()
	defer e.mu.Unlock()
	b, ok := e.bindings[key]
	if !ok {
		return ""
	}
	if !b.ExpiresAt.IsZero() && !now.Before(b.ExpiresAt) {
		delete(e.bindings, key)
		return ""
	}
	return b.ModelID
}

func (e *Engine) bind(taskID, modelID string, ttl time.Duration) {
	taskID = strings.TrimSpace(taskID)
	modelID = strings.TrimSpace(modelID)
	if taskID == "" || modelID == "" {
		return
	}
	e.mu.Lock()
	e.bindings[taskID] = binding{ModelID: modelID, ExpiresAt: time.Now().Add(ttl)}
	e.mu.Unlock()
}

func (e *Engine) Release(taskID string) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return
	}
	e.mu.Lock()
	delete(e.bindings, taskID)
	e.mu.Unlock()
}

func (e *Engine) Snapshot() map[string]any {
	now := time.Now()
	e.mu.Lock()
	for k, b := range e.bindings {
		if !b.ExpiresAt.IsZero() && !now.Before(b.ExpiresAt) {
			delete(e.bindings, k)
		}
	}
	n := len(e.bindings)
	e.mu.Unlock()
	return map[string]any{"version": Version, "sticky_bindings": n}
}

func (e *Engine) Plan(task Task, candidates []Candidate, policy Policy) Plan {
	plan := Plan{Version: Version, Task: task, Assistants: []Assignment{}, Rejected: map[string][]string{}}
	if !policy.Enabled {
		plan.Warnings = append(plan.Warnings, "orchestration_disabled")
		return plan
	}
	plan.ReviewerRequired = reviewerRequired(task, policy)

	type scored struct {
		c       Candidate
		score   float64
		reasons []string
	}
	leads := []scored{}
	for _, c := range candidates {
		reasons, ok := eligibility(c, task, "lead", policy)
		if !ok {
			plan.Rejected[c.ModelID] = append(plan.Rejected[c.ModelID], reasons...)
			continue
		}
		score, why, roleOK := assignmentScore(c, task, "lead")
		if !roleOK {
			plan.Rejected[c.ModelID] = append(plan.Rejected[c.ModelID], why...)
			continue
		}
		leads = append(leads, scored{c: c, score: score, reasons: why})
	}
	sort.SliceStable(leads, func(i, j int) bool {
		if leads[i].score != leads[j].score {
			return leads[i].score > leads[j].score
		}
		return leads[i].c.ModelID < leads[j].c.ModelID
	})

	pinned := e.pinned(task.ID, time.Now())
	var lead scored
	haveLead := false
	if task.RequiresStickyModel && pinned != "" {
		for _, x := range leads {
			if x.c.ModelID == pinned {
				lead, haveLead, plan.StickyReused = x, true, true
				break
			}
		}
		if !haveLead {
			plan.Warnings = append(plan.Warnings, "sticky_owner_unavailable_reselected")
		}
	}
	if !haveLead && len(leads) > 0 {
		lead, haveLead = leads[0], true
	}
	if !haveLead {
		plan.Warnings = append(plan.Warnings, "no_eligible_lead")
		return plan
	}
	plan.Lead = &Assignment{Role: "lead", ModelID: lead.c.ModelID, ProviderID: lead.c.ProviderID, Score: lead.score, Reasons: append([]string(nil), lead.reasons...), UsesProtectedReserve: usesProtectedReserve(lead.c, task, policy)}
	if task.RequiresStickyModel {
		e.bind(task.ID, lead.c.ModelID, stickyTTL(policy))
	}

	maxAssist := policy.MaxAssistants
	if maxAssist < 0 {
		maxAssist = 0
	}
	if maxAssist > 4 {
		maxAssist = 4
	}
	if task.DelegationAllowed && maxAssist > 0 {
		assists := []scored{}
		for _, c := range candidates {
			if c.ModelID == lead.c.ModelID {
				continue
			}
			_, ok := eligibility(c, task, "assist", policy)
			if !ok {
				continue
			}
			score, why, roleOK := assignmentScore(c, task, "assist")
			if !roleOK {
				continue
			}
			assists = append(assists, scored{c: c, score: score, reasons: why})
		}
		sort.SliceStable(assists, func(i, j int) bool {
			if assists[i].score != assists[j].score {
				return assists[i].score > assists[j].score
			}
			return assists[i].c.ModelID < assists[j].c.ModelID
		})
		for i := 0; i < len(assists) && i < maxAssist; i++ {
			x := assists[i]
			plan.Assistants = append(plan.Assistants, Assignment{Role: "assist", ModelID: x.c.ModelID, ProviderID: x.c.ProviderID, Score: x.score, Reasons: append([]string(nil), x.reasons...), UsesProtectedReserve: usesProtectedReserve(x.c, task, policy)})
		}
	}

	if plan.ReviewerRequired {
		reviewers := []scored{}
		for _, c := range candidates {
			if c.ModelID == lead.c.ModelID {
				continue
			}
			_, ok := eligibility(c, task, "review", policy)
			if !ok {
				continue
			}
			if _, ok := roleMatch(c.Profile, []string{"review", "analysis", "security"}); !ok {
				continue
			}
			score, why, _ := assignmentScore(c, Task{Risk: task.Risk, RequiredRoles: []string{"review", "analysis", "security"}}, "review")
			reviewers = append(reviewers, scored{c: c, score: score, reasons: why})
		}
		sort.SliceStable(reviewers, func(i, j int) bool {
			if reviewers[i].score != reviewers[j].score {
				return reviewers[i].score > reviewers[j].score
			}
			return reviewers[i].c.ModelID < reviewers[j].c.ModelID
		})
		if len(reviewers) == 0 {
			plan.Warnings = append(plan.Warnings, "independent_reviewer_required_but_unavailable")
			return plan
		}
		x := reviewers[0]
		plan.Reviewer = &Assignment{Role: "review", ModelID: x.c.ModelID, ProviderID: x.c.ProviderID, Score: x.score, Reasons: append([]string(nil), x.reasons...), UsesProtectedReserve: usesProtectedReserve(x.c, task, policy)}
	}
	plan.Valid = true
	return plan
}
