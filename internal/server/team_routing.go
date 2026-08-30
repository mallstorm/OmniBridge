package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"

	"omnibridge/internal/config"
	"omnibridge/internal/orchestration"
	"omnibridge/internal/providers"
	obr "omnibridge/internal/router"
	"omnibridge/internal/storage"
)

type teamServiceHolder struct {
	db      *storage.DB
	service *orchestration.Service
}

var (
	teamServicesMu sync.Mutex
	teamServices   = map[*Server]teamServiceHolder{}
)

// teamService keeps Team Routing optional and isolated from the Server ABI.
// It binds to the current storage handle lazily, so tests/headless instances
// without persistence still work and a later SetStorage transparently upgrades
// the service to persistent configuration.
func (s *Server) teamService() *orchestration.Service {
	teamServicesMu.Lock()
	defer teamServicesMu.Unlock()
	if h, ok := teamServices[s]; ok && h.db == s.Storage && h.service != nil {
		return h.service
	}
	svc := orchestration.NewService(s.Storage)
	teamServices[s] = teamServiceHolder{db: s.Storage, service: svc}
	return svc
}

func inferredTeamProfile(s *Server, m providers.Model) orchestration.Profile {
	roles := []string{"analysis", "documentation", "summarization"}
	if m.ContextWindow >= 64000 {
		roles = append(roles, "large_context", "research")
	}
	if m.AgentReady && m.ToolCalling && m.IdentityVerified && m.DoctorVerified {
		roles = append(roles, "implementation", "debugging", "testing")
	}
	if m.Reasoning && m.IdentityVerified && m.DoctorVerified {
		roles = append(roles, "architecture", "review")
	}
	if m.Vision && m.DoctorVerified {
		roles = append(roles, "vision")
	}
	risk := "medium"
	if m.IdentityVerified && m.DoctorVerified {
		risk = "high"
	}
	reserve := 10.0
	if s.isLocalModel(m) {
		reserve = 0
	}
	return orchestration.Profile{
		ModelID:             m.ID,
		Roles:               roles,
		RiskLimit:           risk,
		MinimumQuotaReserve: reserve,
		Lead:                true,
		Assist:              true,
		Review:              m.IdentityVerified && m.DoctorVerified && m.Reasoning,
	}
}

func (s *Server) teamProfile(m providers.Model) (orchestration.Profile, string) {
	if p, ok, key := s.teamService().ProfileFor(m.ID, m.NativeID, m.ProviderID); ok {
		p.ModelID = m.ID
		return p, "configured:" + key
	}
	return inferredTeamProfile(s, m), "inferred"
}

func (s *Server) teamCandidates(req obr.Requirements) ([]orchestration.Candidate, map[string]string) {
	models, _ := s.filterRouterModelsForRequest(s.Registry.Models(), "auto", "smart", req, true)
	out := make([]orchestration.Candidate, 0, len(models))
	sources := make(map[string]string, len(models))
	for _, m := range models {
		rc := s.toRouterCandidate(m, config.RoutingPolicy{})
		profile, source := s.teamProfile(m)
		if source == "inferred" && !rc.QuotaKnown {
			// A synthetic reserve is meaningless when the provider exposes no
			// usable quota telemetry. Explicit operator profiles keep their reserve
			// and therefore remain the mechanism for strict quota protection.
			profile.MinimumQuotaReserve = 0
		}
		score := obr.ScoreCandidateForRequest(rc, "smart", req)
		out = append(out, orchestration.Candidate{
			ModelID:          m.ID,
			ProviderID:       m.ProviderID,
			ChatVerified:     m.ChatVerified,
			IdentityVerified: m.IdentityVerified,
			DoctorVerified:   m.DoctorVerified,
			AgentReady:       modelAgentReady(m),
			ToolCalling:      m.ToolCalling,
			Reasoning:        m.Reasoning,
			Vision:           m.Vision,
			ContextWindow:    m.ContextWindow,
			HealthRank:       rc.HealthRank,
			QuotaKnown:       rc.QuotaKnown,
			QuotaRemaining:   rc.QuotaRemaining,
			CostKnown:        rc.CostKnown,
			CostPerMTokens:   rc.CostPerMTokens,
			RouterScore:      score.Total,
			Profile:          profile,
		})
		sources[m.ID] = source
	}
	return out, sources
}

func (s *Server) dispatchTeamAdmin(w http.ResponseWriter, r *http.Request) bool {
	// remoteGuard calls this extension point only after the remote CIDR policy.
	// Claim data-plane requests only when their body explicitly selects team/*;
	// all ordinary /v1 traffic falls through to the unchanged RC8 mux.
	if s.dispatchTeamData(w, r) {
		return true
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	var handler http.HandlerFunc
	switch path {
	case "/bridge/orchestration":
		switch r.Method {
		case http.MethodGet:
			handler = s.teamRoutingStatus
		case http.MethodPut:
			handler = s.putTeamRoutingConfig
		default:
			w.Header().Set("Allow", "GET, PUT")
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return true
		}
	case "/bridge/orchestration/evaluate":
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return true
		}
		handler = s.teamRoutingEvaluate
	default:
		return false
	}
	if !authOK(r, s.Config.Get().AdminAPIKey) {
		jsonErr(w, http.StatusUnauthorized, "invalid admin key")
		return true
	}
	// remoteGuard dispatches before the ordinary mux, so preserve the same
	// panic-recovery contract explicitly for these extension endpoints.
	s.recoverPanics(handler).ServeHTTP(w, r)
	return true
}

func (s *Server) teamRoutingStatus(w http.ResponseWriter, r *http.Request) {
	out := s.teamService().Snapshot()
	out["virtual_models"] = teamVirtualModels
	out["execution_contract"] = "bounded_non_streaming"
	jsonOut(w, http.StatusOK, out)
}

func (s *Server) putTeamRoutingConfig(w http.ResponseWriter, r *http.Request) {
	var cfg orchestration.Config
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&cfg); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	svc := s.teamService()
	if err := svc.Update(cfg); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonOut(w, http.StatusOK, map[string]any{"ok": true, "config": svc.Config(), "virtual_models": teamVirtualModels})
}

type teamEvaluateInput struct {
	APIType           string          `json:"api_type"`
	TaskID            string          `json:"task_id"`
	TaskType          string          `json:"task_type,omitempty"`
	Risk              string          `json:"risk,omitempty"`
	DelegationAllowed *bool           `json:"delegation_allowed,omitempty"`
	Request           json.RawMessage `json:"request"`
}

func applyTeamTaskType(task orchestration.Task, taskType string) (orchestration.Task, error) {
	taskType = strings.ToLower(strings.TrimSpace(taskType))
	if taskType == "" {
		return task, nil
	}
	switch taskType {
	case "general":
		task.Type, task.RequiredRoles = "general", []string{"analysis"}
	case "security_review":
		task.Type, task.RequiredRoles = taskType, []string{"security", "review"}
		task.Risk, task.Complexity, task.DelegationAllowed = "critical", "high", false
	case "architecture":
		task.Type, task.RequiredRoles = taskType, []string{"architecture", "analysis"}
		task.Risk, task.Complexity, task.RequiresReasoning = "high", "high", true
	case "debugging":
		task.Type, task.RequiredRoles = taskType, []string{"debugging", "implementation"}
	case "implementation":
		task.Type, task.RequiredRoles = taskType, []string{"implementation", "testing"}
	case "testing":
		task.Type, task.RequiredRoles = taskType, []string{"testing"}
	case "review":
		task.Type, task.RequiredRoles, task.Risk = taskType, []string{"review", "analysis"}, "high"
	case "research":
		task.Type, task.RequiredRoles = taskType, []string{"research", "large_context"}
	case "summarization":
		task.Type, task.RequiredRoles = taskType, []string{"summarization"}
	case "documentation":
		task.Type, task.RequiredRoles = taskType, []string{"documentation"}
	default:
		return task, errors.New("unsupported task_type")
	}
	return task, nil
}

func teamRiskRank(v string) int {
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
		return 0
	}
}

func (s *Server) teamRoutingEvaluate(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var wrapper teamEvaluateInput
	requestRaw := raw
	apiType := strings.TrimSpace(r.URL.Query().Get("api_type"))
	taskID := strings.TrimSpace(r.Header.Get("X-OmniBridge-Task"))
	wrapped := json.Unmarshal(raw, &wrapper) == nil && len(wrapper.Request) > 0
	if wrapped {
		requestRaw = wrapper.Request
		if strings.TrimSpace(wrapper.APIType) != "" {
			apiType = strings.TrimSpace(wrapper.APIType)
		}
		if strings.TrimSpace(wrapper.TaskID) != "" {
			taskID = strings.TrimSpace(wrapper.TaskID)
		}
	}
	if apiType == "" {
		apiType = teamChatCompletions
	}
	var meta struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(requestRaw, &meta) != nil || strings.TrimSpace(meta.Model) == "" {
		jsonErr(w, http.StatusBadRequest, "request must be valid JSON and contain model")
		return
	}
	req := obr.AnalyzeRequest(requestRaw, apiType)
	task := orchestration.Classify(requestRaw, req, taskID)
	if wrapped {
		task, err = applyTeamTaskType(task, wrapper.TaskType)
		if err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if strings.TrimSpace(wrapper.Risk) != "" {
			risk := strings.ToLower(strings.TrimSpace(wrapper.Risk))
			if risk != "low" && risk != "medium" && risk != "high" && risk != "critical" {
				jsonErr(w, http.StatusBadRequest, "risk must be low, medium, high, or critical")
				return
			}
			// Explicit risk is monotonic: an operator may raise conservatism but
			// cannot downgrade what the classifier/task policy already established.
			if teamRiskRank(risk) > teamRiskRank(task.Risk) {
				task.Risk = risk
			}
		}
		if wrapper.DelegationAllowed != nil {
			task.DelegationAllowed = *wrapper.DelegationAllowed
		}
	}
	if req.ToolCalling || req.AgentMode {
		if task.Risk == "low" || task.Risk == "medium" {
			task.Risk = "high"
		}
	}
	if task.Risk == "critical" {
		task.DelegationAllowed = false
	}
	task.RequiresStickyModel = task.Risk == "high" || task.Risk == "critical" || req.ToolCalling || req.AgentMode

	svc := s.teamService()
	cfg := svc.Config()
	candidates, profileSources := s.teamCandidates(req)
	plan := svc.Engine().Plan(task, candidates, cfg.Policy)
	plan = orchestration.PreferIndependentReviewer(plan, candidates, cfg.Policy)
	out := map[string]any{
		"version":                   orchestration.Version,
		"execution":                 "dry_run_only",
		"bounded_execution_enabled": cfg.Execution.Enabled,
		"request_model":             meta.Model,
		"api_type":                  apiType,
		"requirements":              req,
		"plan":                      plan,
		"profile_sources":           profileSources,
		"virtual_models":            teamVirtualModels,
		"hard_gates":                []string{"health", "chat_verified", "identity_verified_for_high_risk", "doctor_verified_for_high_risk", "agent_ready_for_tools", "context", "protected_quota_reserve", "dispatch_time_revalidation"},
	}
	if !cfg.Enabled {
		out["enabled"] = false
		jsonOut(w, http.StatusOK, out)
		return
	}
	out["enabled"] = true
	if !plan.Valid {
		jsonOut(w, http.StatusUnprocessableEntity, out)
		return
	}
	jsonOut(w, http.StatusOK, out)
}
