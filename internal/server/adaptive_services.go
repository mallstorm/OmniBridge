package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"omnibridge/internal/adaptive"
	"omnibridge/internal/config"
	"omnibridge/internal/generation"
	"omnibridge/internal/modelcatalog"
	"omnibridge/internal/providerpacks"
	"omnibridge/internal/resilience"
)

func (s *Server) adaptiveStatus(w http.ResponseWriter, r *http.Request) {
	if s.Adaptive == nil {
		jsonErr(w, 503, "adaptive routing is not attached")
		return
	}
	jsonOut(w, 200, map[string]any{"config": s.Config.Get().Adaptive, "runtime": s.Adaptive.Snapshot()})
}
func (s *Server) putAdaptiveConfig(w http.ResponseWriter, r *http.Request) {
	var c config.AdaptiveConfig
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&c); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if err := s.Config.SetAdaptive(c); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if s.Adaptive != nil {
		s.Adaptive.UpdateConfig(c)
	}
	jsonOut(w, 200, map[string]any{"ok": true, "config": s.Config.Get().Adaptive})
}
func (s *Server) routeExplain(w http.ResponseWriter, r *http.Request) {
	if s.Adaptive == nil {
		jsonErr(w, 503, "adaptive routing is not attached")
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("trace_id"))
	if id == "" {
		jsonOut(w, 200, s.Adaptive.Recent(100))
		return
	}
	d, ok := s.Adaptive.Explain(id)
	if !ok {
		jsonErr(w, 404, "route decision not found or expired")
		return
	}
	jsonOut(w, 200, d)
}
func (s *Server) adaptiveEvaluatorOutcome(w http.ResponseWriter, r *http.Request) {
	if s.Adaptive == nil {
		jsonErr(w, 503, "adaptive routing is not attached")
		return
	}
	var in struct {
		Source          string  `json:"source"`
		TraceID         string  `json:"trace_id"`
		Route           string  `json:"route"`
		ProviderID      string  `json:"provider_id"`
		ConnectionID    string  `json:"connection_id"`
		ModelID         string  `json:"model_id"`
		NativeModelID   string  `json:"native_model_id"`
		SemanticQuality float64 `json:"semantic_quality"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if in.Source != "evaluator" || strings.TrimSpace(in.ModelID) == "" || in.SemanticQuality < 0 || in.SemanticQuality > 1 {
		jsonErr(w, 400, "source=evaluator, model_id and semantic_quality 0..1 are required")
		return
	}
	q := in.SemanticQuality
	accepted := s.Adaptive.Observe(adaptive.Outcome{TraceID: in.TraceID, Route: in.Route, ProviderID: in.ProviderID, ConnectionID: in.ConnectionID, ModelID: in.ModelID, NativeModelID: in.NativeModelID, Success: true, SemanticQuality: &q, At: time.Now().UTC()})
	jsonOut(w, 202, map[string]any{"accepted": accepted, "semantic_quality_inferred_from_http": false})
}
func (s *Server) cacheStatus(w http.ResponseWriter, r *http.Request) {
	if s.Generation == nil {
		jsonErr(w, 503, "generation cache is not attached")
		return
	}
	s.Generation.UpdateConfig(s.Config.Get().ExactCache)
	jsonOut(w, 200, s.Generation.Snapshot())
}
func (s *Server) putCacheConfig(w http.ResponseWriter, r *http.Request) {
	var c config.ExactCacheConfig
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&c); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if err := s.Config.SetExactCache(c); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	s.Generation.UpdateConfig(c)
	jsonOut(w, 200, map[string]any{"ok": true, "version": generation.Version, "config": c})
}
func (s *Server) clearCache(w http.ResponseWriter, r *http.Request) {
	if s.Generation != nil {
		s.Generation.Clear()
	}
	jsonOut(w, 200, map[string]any{"ok": true})
}

type autopilotAction struct {
	ID                string         `json:"id"`
	Title             string         `json:"title"`
	Risk              string         `json:"risk"`
	Evidence          map[string]any `json:"evidence"`
	RecommendedAction string         `json:"recommended_action"`
	Automatic         bool           `json:"automatic"`
	PreconditionsHash string         `json:"preconditions_hash"`
}

func actionHash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:16])
}
func (s *Server) autopilotActions() []autopilotAction {
	out := []autopilotAction{}
	now := time.Now().UTC()
	if s.Resilience != nil {
		for _, b := range s.Resilience.Snapshots() {
			if b.State == resilience.Closed {
				continue
			}
			e := map[string]any{"key": b.Key, "state": b.State, "failure_kind": b.LastFailureKind, "open_until": b.OpenUntil}
			a := autopilotAction{ID: "breaker:" + b.Key, Title: "Проверить проблемное подключение", Risk: "low", Evidence: e, RecommendedAction: "reset_breaker_after_recheck", Automatic: true}
			a.PreconditionsHash = actionHash(e)
			out = append(out, a)
		}
	}
	for _, p := range s.Config.Get().Providers {
		if !p.Enabled {
			continue
		}
		stale := true
		for _, m := range s.Registry.Models() {
			if m.ProviderID == p.ID && !m.LastChecked.IsZero() && now.Sub(m.LastChecked) < 24*time.Hour {
				stale = false
				break
			}
		}
		if stale {
			e := map[string]any{"provider_id": p.ID, "enabled": p.Enabled, "reason": "model_discovery_stale"}
			a := autopilotAction{ID: "refresh:" + p.ID, Title: "Обновить каталог моделей", Risk: "low", Evidence: e, RecommendedAction: "refresh_provider", Automatic: true}
			a.PreconditionsHash = actionHash(e)
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (s *Server) healthAutopilot(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, 200, map[string]any{"version": "1.0", "dry_run": true, "actions": s.autopilotActions()})
}
func (s *Server) healthAutopilotAction(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID                string `json:"id"`
		PreconditionsHash string `json:"preconditions_hash"`
		DryRun            bool   `json:"dry_run"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	var selected *autopilotAction
	for _, a := range s.autopilotActions() {
		if a.ID == in.ID {
			x := a
			selected = &x
			break
		}
	}
	if selected == nil {
		jsonErr(w, 409, "action is stale or no longer applicable")
		return
	}
	if in.PreconditionsHash != selected.PreconditionsHash {
		jsonErr(w, 409, "preconditions changed; run dry-run again")
		return
	}
	if in.DryRun {
		jsonOut(w, 200, map[string]any{"ok": true, "executed": false, "action": selected})
		return
	}
	switch {
	case strings.HasPrefix(in.ID, "breaker:"):
		if s.Resilience == nil || s.Resilience.Reset(strings.TrimPrefix(in.ID, "breaker:")) != nil {
			jsonErr(w, 409, "breaker reset failed")
			return
		}
	case strings.HasPrefix(in.ID, "refresh:"):
		snap := s.refreshRegistry(r.Context())
		jsonOut(w, 200, map[string]any{"ok": true, "executed": true, "snapshot": snap})
		return
	default:
		jsonErr(w, 400, "unsupported autopilot action")
		return
	}
	jsonOut(w, 200, map[string]any{"ok": true, "executed": true, "action_id": in.ID})
}

func (s *Server) validateProviderPack(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Manifest    json.RawMessage   `json:"manifest"`
		TrustedKeys map[string]string `json:"trusted_keys"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&in); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	m, err := providerpacks.Decode(in.Manifest)
	if err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	result := providerpacks.Validate(m, in.TrustedKeys)
	status := http.StatusOK
	if !result.Valid {
		status = http.StatusUnprocessableEntity
	}
	jsonOut(w, status, map[string]any{"version": providerpacks.Version, "result": result, "data_only": true, "execution_allowed": false})
}
func (s *Server) catalogSyncStatus(w http.ResponseWriter, r *http.Request) {
	if s.ModelCatalog == nil {
		jsonErr(w, 503, "model catalog is not attached")
		return
	}
	jsonOut(w, 200, map[string]any{"config": s.Config.Get().CatalogSync, "snapshot": s.ModelCatalog.Snapshot(), "verification_effect": "none"})
}
func (s *Server) putCatalogSync(w http.ResponseWriter, r *http.Request) {
	var c config.CatalogSyncConfig
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&c); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if err := s.Config.SetCatalogSync(c); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if s.ModelCatalog == nil {
		s.ModelCatalog = modelcatalog.New(s.Storage, c)
	} else {
		s.ModelCatalog.UpdateConfig(c)
	}
	jsonOut(w, 200, map[string]any{"ok": true, "config": c})
}
func (s *Server) runCatalogSync(w http.ResponseWriter, r *http.Request) {
	if s.ModelCatalog == nil {
		jsonErr(w, 503, "model catalog is not attached")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	snap, err := s.ModelCatalog.Sync(ctx)
	if err != nil {
		jsonOut(w, 502, map[string]any{"ok": false, "error": err.Error(), "last_known_good": snap})
		return
	}
	jsonOut(w, 200, map[string]any{"ok": true, "snapshot": snap, "verified_flags_changed": false})
}
func (s *Server) vscodeSelfTest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	m, err := s.resolveModel(strings.TrimSpace(in.Model))
	if err != nil {
		jsonErr(w, 404, err.Error())
		return
	}
	if !m.ChatVerified {
		jsonErr(w, 422, "VS Code self-test requires ChatVerified model")
		return
	}
	payload, _ := json.Marshal(map[string]any{"model": m.ID, "stream": false, "temperature": 0, "max_tokens": 80, "messages": []any{map[string]any{"role": "user", "content": "Identify your exact model and provider in one sentence."}}})
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	call, err := s.upstream(ctx, m, "chat-completions", payload)
	if err != nil {
		jsonErr(w, 502, err.Error())
		return
	}
	if call.Body == nil {
		jsonErr(w, 502, "empty upstream response")
		return
	}
	defer call.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(call.Body, 2<<20))
	jsonOut(w, 200, map[string]any{"ok": call.Status/100 == 2, "status": call.Status, "selected_model": m.ID, "provider": m.ProviderID, "native_model": m.NativeID, "chat_verified": m.ChatVerified, "identity_verified": m.IdentityVerified, "upstream_model_id": m.UpstreamModelID, "response": json.RawMessage(raw)})
}
