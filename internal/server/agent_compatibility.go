package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"omnibridge/internal/agentcompat"
	"omnibridge/internal/providers"
)

func (s *Server) agentProviderKind(model providers.Model) string {
	if s.Config == nil {
		return ""
	}
	cfg := s.Config.Get()
	for _, provider := range cfg.Providers {
		if provider.ID == model.ProviderID {
			return provider.Kind
		}
	}
	for _, account := range cfg.Accounts {
		if account.ID == model.ProviderID {
			return account.Kind
		}
	}
	return ""
}

func (s *Server) agentCompatibilityStatus(w http.ResponseWriter, r *http.Request) {
	models := s.Registry.Models()
	reports := make([]agentcompat.Report, 0, len(models))
	compatible := 0
	for _, model := range models {
		report := agentcompat.Evaluate(model, s.agentProviderKind(model))
		if report.Compatible {
			compatible++
		}
		reports = append(reports, report)
	}
	jsonOut(w, http.StatusOK, map[string]any{
		"version":           agentcompat.Version,
		"models":            reports,
		"total_models":      len(reports),
		"compatible_models": compatible,
		"virtual_model":     "auto/agent",
	})
}

func (s *Server) agentCompatibilityEvaluate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	in.Model = strings.TrimSpace(in.Model)
	if in.Model == "" {
		jsonErr(w, http.StatusBadRequest, "model is required")
		return
	}
	model, err := s.Registry.Find(in.Model)
	if err != nil {
		jsonOut(w, http.StatusNotFound, map[string]any{
			"error": map[string]any{"message": "model not found", "type": "model_not_found", "code": "model_not_found"},
		})
		return
	}
	jsonOut(w, http.StatusOK, agentcompat.Evaluate(model, s.agentProviderKind(model)))
}
