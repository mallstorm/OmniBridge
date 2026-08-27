package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"omnibridge/internal/agentcompat"
)

// agentCompatibilityProbe runs the strict provider-side Agent Doctor v2 and
// persists the resulting evidence into the ordinary Model Registry. Account
// connectors are intentionally fail-closed until they expose the same strict
// two-round execution primitive.
func (s *Server) agentCompatibilityProbe(w http.ResponseWriter, r *http.Request) {
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
		jsonOut(w, http.StatusNotFound, map[string]any{"error": map[string]any{"message": "model not found", "type": "model_not_found", "code": "model_not_found"}})
		return
	}
	provider, ok := s.providerByID(model.ProviderID)
	if !ok {
		jsonOut(w, http.StatusUnprocessableEntity, map[string]any{"error": map[string]any{
			"message": "strict Agent Doctor v2 is not yet available for account connectors",
			"type":    "agent_probe_not_supported",
			"code":    "agent_probe_not_supported",
		}})
		return
	}

	probe := s.Providers.ProbeAgentV2(r.Context(), provider, model)
	s.Registry.UpdateProbe(model.ID, probe)
	updated, err := s.Registry.Find(model.ID)
	if err != nil {
		updated = model
		updated.ToolRoundTrip = probe.ToolRoundTrip
		updated.AgentReady = probe.AgentCompatible
		updated.DoctorStatus = probe.DoctorStatus
	}
	report := agentcompat.Evaluate(updated, provider.Kind)
	jsonOut(w, http.StatusOK, map[string]any{
		"version":       "2.0",
		"model":         model.ID,
		"probe":         probe,
		"compatibility": report,
	})
}
