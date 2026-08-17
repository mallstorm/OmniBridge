package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"omnibridge/internal/config"
	"omnibridge/internal/providers"
	obr "omnibridge/internal/router"
)

var autoVirtualModels = []string{
	"auto/smart",
	"auto/coding",
	"auto/fast",
	"auto/cheap",
	"auto/local",
	"auto/reasoning",
	"auto/vision",
	"auto/agent",
}

func isAutoVirtualModel(id string) bool {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, v := range autoVirtualModels {
		if id == v {
			return true
		}
	}
	return false
}

func autoPolicy(id string) (config.RoutingPolicy, string) {
	profile := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(id)), "auto/")
	mode := "auto"
	switch profile {
	case "fast":
		mode = "fastest"
	case "cheap":
		mode = "cost_optimized"
	case "local", "coding", "reasoning", "vision", "agent", "smart":
		mode = "auto"
	default:
		profile = "smart"
	}
	return config.RoutingPolicy{Mode: mode}, profile
}

func requirementsForProfile(req obr.Requirements, profile string) obr.Requirements {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "agent":
		req.AgentMode = true
		req.ToolCalling = true
		req.RequiresDoctorProof = true
	case "vision":
		req.Vision = true
		req.RequiresDoctorProof = true
	case "reasoning":
		req.Reasoning = true
		req.RequiresDoctorProof = true
	}
	return req
}

func routingHint(m map[string]float64, model providers.Model) (float64, bool) {
	if len(m) == 0 {
		return 0, false
	}
	for _, key := range []string{model.ID, model.NativeID, model.ProviderID} {
		if v, ok := m[key]; ok {
			return v, true
		}
	}
	return 0, false
}

func (s *Server) toRouterCandidate(m providers.Model, policy config.RoutingPolicy) obr.Candidate {
	metric := s.Stats.ModelMetrics(m.ID, "24h")
	healthRank := 0
	if s.Resilience != nil {
		healthRank = s.Resilience.HealthRank(resilienceConnectionKey(m))
		if mr := s.Resilience.HealthRank("model:" + m.ID); mr > healthRank {
			healthRank = mr
		}
	}
	c := obr.Candidate{
		ID:               m.ID,
		ProviderID:       m.ProviderID,
		NativeID:         m.NativeID,
		Source:           m.Source,
		Local:            s.isLocalModel(m),
		AgentReady:       modelAgentReady(m),
		ToolCalling:      m.ToolCalling,
		ParallelTools:    m.ParallelTools,
		StructuredOutput: m.StructuredOutput,
		SystemPrompt:     m.SystemPrompt,
		Reasoning:        m.Reasoning,
		Vision:           m.Vision,
		Streaming:        m.Streaming,
		ContextWindow:    m.ContextWindow,
		MaxOutputTokens:  m.MaxOutputTokens,
		DoctorVerified:   m.DoctorVerified,
		DoctorScore:      m.DoctorScore,
		AgentScore:       m.AgentScore,
		CodingScore:      m.CodingScore,
		ReliabilityScore: m.ReliabilityScore,
		HealthRank:       healthRank,
		Requests24h:      metric.Requests,
		SuccessRate:      metric.SuccessRate,
		AverageLatencyMS: metric.AverageLatencyMS,
		P95LatencyMS:     metric.P95LatencyMS,
	}
	if s.QuotaCost != nil {
		providerKey := m.ProviderID
		for _, p := range s.Config.Get().Providers {
			if p.ID == m.ProviderID {
				providerKey = p.Kind
				break
			}
		}
		for _, a := range s.Config.Get().Accounts {
			if a.ID == m.ProviderID {
				providerKey = a.Kind
				break
			}
		}
		q, qKnown, cost, costKnown := s.QuotaCost.CandidateHints(m.ProviderID, providerKey, m.NativeID)
		if !costKnown && providerKey != m.ProviderID {
			_, _, cost, costKnown = s.QuotaCost.CandidateHints(m.ProviderID, m.ProviderID, m.NativeID)
		}
		if qKnown {
			c.QuotaKnown = true
			c.QuotaRemaining = q
		}
		if costKnown {
			c.CostKnown = true
			c.CostPerMTokens = cost
		}
	}
	if !c.QuotaKnown {
		if v, ok := routingHint(policy.QuotaHints, m); ok {
			c.QuotaKnown = true
			c.QuotaRemaining = v
		}
	}
	if !c.CostKnown {
		if v, ok := routingHint(policy.CostHints, m); ok {
			c.CostKnown = true
			c.CostPerMTokens = v
		}
	}
	return c
}

func (s *Server) filterRouterModels(models []providers.Model, mode, profile string) []providers.Model {
	out := make([]providers.Model, 0, len(models))
	for _, m := range models {
		if m.Status == "error" || s.modelBlocked(m) {
			continue
		}
		switch mode {
		case "agent_ready_only":
			if !modelAgentReady(m) {
				continue
			}
		case "local_only":
			if !s.isLocalModel(m) {
				continue
			}
		case "cloud_only":
			if s.isLocalModel(m) {
				continue
			}
		}
		switch profile {
		case "local":
			if !s.isLocalModel(m) {
				continue
			}
		case "agent":
			if !modelAgentReady(m) {
				continue
			}
		case "reasoning":
			if !m.Reasoning {
				continue
			}
		case "vision":
			if !m.Vision {
				continue
			}
		}
		out = append(out, m)
	}
	return out
}

func (s *Server) orderRouterModels(routeKey string, policy config.RoutingPolicy, profile, sessionKey string, models []providers.Model) []providers.Model {
	return s.orderRouterModelsForRequest(routeKey, policy, profile, sessionKey, models, obr.Requirements{})
}

func (s *Server) orderRouterModelsForRequest(routeKey string, policy config.RoutingPolicy, profile, sessionKey string, models []providers.Model, req obr.Requirements) []providers.Model {
	if s.Router == nil || len(models) == 0 {
		return models
	}
	cfg := s.Config.Get().Routing
	candidates := make([]obr.Candidate, 0, len(models))
	byID := make(map[string]providers.Model, len(models))
	for _, m := range models {
		candidates = append(candidates, s.toRouterCandidate(m, policy))
		byID[m.ID] = m
	}
	ordered := s.Router.Order(candidates, obr.Options{
		RouteKey:          routeKey,
		Mode:              policy.Mode,
		Profile:           profile,
		SessionKey:        sessionKey,
		SessionStickiness: cfg.SessionStickiness,
		SessionTTL:        time.Duration(cfg.SessionTTLSeconds) * time.Second,
		Weights:           policy.Weights,
		Requirements:      req,
	})
	out := make([]providers.Model, 0, len(ordered))
	for _, c := range ordered {
		if m, ok := byID[c.ID]; ok {
			out = append(out, m)
		}
	}
	return out
}

func (s *Server) filterRouterModelsForRequest(models []providers.Model, mode, profile string, req obr.Requirements, requireDoctorProof bool) ([]providers.Model, map[string][]string) {
	base := s.filterRouterModels(models, mode, profile)
	out := make([]providers.Model, 0, len(base))
	rejected := map[string][]string{}
	for _, m := range base {
		check := obr.CheckCompatibility(s.toRouterCandidate(m, config.RoutingPolicy{}), req, requireDoctorProof)
		if !check.Compatible {
			rejected[m.ID] = append([]string(nil), check.Reasons...)
			continue
		}
		out = append(out, m)
	}
	return out, rejected
}

func hashedSessionKey(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:12])
}

func extractRoutingSession(r *http.Request, body []byte) string {
	for _, h := range []string{"X-OmniBridge-Session", "X-Session-ID", "X-Conversation-ID", "OpenAI-Conversation-ID"} {
		if v := strings.TrimSpace(r.Header.Get(h)); v != "" {
			return hashedSessionKey(v)
		}
	}
	var v map[string]any
	if json.Unmarshal(body, &v) != nil {
		return ""
	}
	for _, key := range []string{"session_id", "conversation_id", "previous_response_id"} {
		if x, ok := v[key].(string); ok && strings.TrimSpace(x) != "" {
			return hashedSessionKey(x)
		}
	}
	if meta, ok := v["metadata"].(map[string]any); ok {
		for _, key := range []string{"session_id", "conversation_id", "thread_id"} {
			if x, ok := meta[key].(string); ok && strings.TrimSpace(x) != "" {
				return hashedSessionKey(x)
			}
		}
	}
	return ""
}

func (s *Server) routingScores(requested string) []obr.Score {
	return s.routingScoresForRequest(requested, obr.Requirements{})
}

func (s *Server) routingScoresForRequest(requested string, req obr.Requirements) []obr.Score {
	policy, profile := autoPolicy(requested)
	if p, ok := s.Config.Get().Routing.Policies[requested]; ok {
		policy = p
		profile = "smart"
	}
	req = requirementsForProfile(req, profile)
	models, err := s.routerCandidatePool(policy)
	if err != nil {
		return nil
	}
	models, _ = s.filterRouterModelsForRequest(models, policy.Mode, profile, req, true)
	out := make([]obr.Score, 0, len(models))
	for _, m := range models {
		out = append(out, obr.ScoreCandidateForRequest(s.toRouterCandidate(m, policy), profile, req))
	}
	return out
}

// routerEvaluate performs a dry-run Doctor-Aware routing decision without
// contacting any upstream. The body is an ordinary OpenAI/Responses/Messages
// request and therefore exercises the same request analyzer as the data plane.
func (s *Server) routerEvaluate(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var meta struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(raw, &meta) != nil || strings.TrimSpace(meta.Model) == "" {
		jsonErr(w, http.StatusBadRequest, "JSON body must contain model")
		return
	}
	apiType := strings.TrimSpace(r.URL.Query().Get("api_type"))
	if apiType == "" {
		apiType = "chat-completions"
	}
	req := obr.AnalyzeRequest(raw, apiType)
	candidates, routeErr := s.routingCandidatesForRequest(meta.Model, "", req)
	ids := make([]string, 0, len(candidates))
	for _, m := range candidates {
		ids = append(ids, m.ID)
	}
	out := map[string]any{
		"version":            "3.1-doctor-aware",
		"model":              meta.Model,
		"api_type":           apiType,
		"requirements":       req,
		"ordered_candidates": ids,
	}
	if strings.HasPrefix(strings.ToLower(meta.Model), "auto/") {
		out["scores"] = s.routingScoresForRequest(meta.Model, req)
	}
	if routeErr != nil {
		out["compatible"] = false
		out["error"] = routeErr.Error()
		jsonOut(w, http.StatusUnprocessableEntity, out)
		return
	}
	out["compatible"] = true
	jsonOut(w, http.StatusOK, out)
}
