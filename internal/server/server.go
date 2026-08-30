package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"omnibridge/internal/accounts"
	"omnibridge/internal/adaptive"
	"omnibridge/internal/config"
	"omnibridge/internal/generation"
	"omnibridge/internal/health"
	"omnibridge/internal/integrations"
	"omnibridge/internal/mcpcontrol"
	"omnibridge/internal/modelcatalog"
	"omnibridge/internal/observability"
	"omnibridge/internal/protocol"
	"omnibridge/internal/providers"
	"omnibridge/internal/quotacost"
	"omnibridge/internal/registry"
	"omnibridge/internal/resilience"
	obr "omnibridge/internal/router"
	"omnibridge/internal/secretstore"
	"omnibridge/internal/stats"
	"omnibridge/internal/storage"
)

type Server struct {
	Config          *config.Store
	Secrets         *secretstore.Store
	Providers       *providers.Manager
	Accounts        *accounts.Manager
	Registry        *registry.Registry
	Stats           *stats.Store
	Health          *health.Store
	Storage         *storage.DB
	Logger          *log.Logger
	httpServer      *http.Server
	shutting        atomic.Bool
	paused          atomic.Bool
	startedAt       time.Time
	trayRefreshing  atomic.Bool
	trayMu          sync.Mutex
	trayRefreshAt   time.Time
	trayRefreshErr  string
	Resilience      *resilience.Manager
	ControlFlight   *resilience.SingleFlight
	Router          *obr.Engine
	QuotaCost       *quotacost.Engine
	Integrations    *integrations.Hub
	MCP             *mcpcontrol.Control
	Observability   *observability.Recorder
	Adaptive        *adaptive.Engine
	Generation      *generation.Manager
	ModelCatalog    *modelcatalog.Service
	Root            string
	generationSlots chan struct{}
	probeSlots      chan struct{}
}

func New(cfg *config.Store, secrets *secretstore.Store, pm *providers.Manager, am *accounts.Manager, reg *registry.Registry, st *stats.Store, logger *log.Logger, healthStores ...*health.Store) *Server {
	var hs *health.Store
	if len(healthStores) > 0 {
		hs = healthStores[0]
	}
	return &Server{Config: cfg, Secrets: secrets, Providers: pm, Accounts: am, Registry: reg, Stats: st, Health: hs, Logger: logger, Resilience: resilience.New(resilienceConfig(cfg.Get().Routing), nil), ControlFlight: resilience.NewSingleFlight(), Router: obr.New(), Generation: generation.New(cfg.Get().ExactCache), startedAt: time.Now(), generationSlots: make(chan struct{}, 16), probeSlots: make(chan struct{}, 2)}
}

func (s *Server) SetStorage(db *storage.DB) {
	s.Storage = db
	s.Resilience = resilience.New(resilienceConfig(s.Config.Get().Routing), db)
	s.QuotaCost = quotacost.New(db, s.Config.Get().QuotaCost)
	s.MCP = mcpcontrol.New(db)
	s.Observability = observability.New(db)
	s.Adaptive = adaptive.New(db, s.Config.Get().Adaptive)
	s.ModelCatalog = modelcatalog.New(db, s.Config.Get().CatalogSync)
	s.Integrations = integrations.New(s.Root, db)
}
func (s *Server) SetRoot(root string) {
	s.Root = root
	s.Integrations = integrations.New(root, s.Storage)
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /readyz", s.readiness)
	mux.HandleFunc("GET /v1/models", s.withData(s.models))
	mux.HandleFunc("POST /v1/chat/completions", s.withData(func(w http.ResponseWriter, r *http.Request) { s.proxy(w, r, "chat-completions") }))
	mux.HandleFunc("POST /v1/responses", s.withData(func(w http.ResponseWriter, r *http.Request) { s.proxy(w, r, "responses") }))
	mux.HandleFunc("POST /v1/messages", s.withData(func(w http.ResponseWriter, r *http.Request) { s.proxy(w, r, "messages") }))
	mux.HandleFunc("POST /v1/embeddings", s.withData(s.embeddings))
	mux.HandleFunc("GET /v1/ws", s.withData(s.websocket))

	mux.HandleFunc("GET /bridge/status", s.withAdmin(s.status))
	mux.HandleFunc("GET /bridge/release-readiness", s.withAdmin(s.releaseReadiness))
	mux.HandleFunc("GET /bridge/storage/status", s.withAdmin(s.storageStatus))
	mux.HandleFunc("GET /bridge/protocol-engine", s.withAdmin(s.protocolEngineStatus))
	mux.HandleFunc("POST /bridge/storage/checkpoint", s.withAdmin(s.storageCheckpoint))
	mux.HandleFunc("GET /bridge/tray", s.withAdmin(s.trayStatus))
	mux.HandleFunc("POST /bridge/tray/start", s.withAdmin(s.resumeGateway))
	mux.HandleFunc("POST /bridge/tray/stop", s.withAdmin(s.pauseGateway))
	mux.HandleFunc("POST /bridge/tray/refresh", s.withAdmin(s.trayRefresh))
	mux.HandleFunc("GET /bridge/connections/health", s.withAdmin(s.connectionHealth))
	mux.HandleFunc("GET /bridge/connections/history", s.withAdmin(s.connectionHistory))
	mux.HandleFunc("POST /bridge/connections/refresh", s.withAdmin(s.refreshConnections))
	mux.HandleFunc("GET /bridge/catalog", s.withAdmin(s.catalog))
	mux.HandleFunc("GET /bridge/provider-engine", s.withAdmin(s.providerEngineStatus))
	mux.HandleFunc("GET /bridge/account-catalog", s.withAdmin(s.accountCatalog))
	mux.HandleFunc("GET /bridge/accounts", s.withAdmin(s.listAccounts))
	mux.HandleFunc("POST /bridge/accounts", s.withAdmin(s.addAccount))
	mux.HandleFunc("DELETE /bridge/accounts/{id}", s.withAdmin(s.deleteAccount))
	mux.HandleFunc("POST /bridge/accounts/{id}/login/start", s.withAdmin(s.startAccountLogin))
	mux.HandleFunc("GET /bridge/accounts/{id}/preflight", s.withAdmin(s.accountPreflight))
	mux.HandleFunc("GET /bridge/accounts/login/status", s.withAdmin(s.accountLoginStatus))
	mux.HandleFunc("GET /bridge/accounts/{id}/info", s.withAdmin(s.accountInfo))
	mux.HandleFunc("GET /bridge/accounts/{id}/usage", s.withAdmin(s.accountUsage))
	mux.HandleFunc("POST /bridge/accounts/{id}/logout", s.withAdmin(s.accountLogout))
	mux.HandleFunc("POST /bridge/accounts/{id}/refresh", s.withAdmin(s.refreshAccount))
	mux.HandleFunc("GET /bridge/local-scan", s.withAdmin(s.localScan))
	mux.HandleFunc("GET /bridge/providers", s.withAdmin(s.listProviders))
	mux.HandleFunc("POST /bridge/providers", s.withAdmin(s.addProvider))
	mux.HandleFunc("DELETE /bridge/providers/{id}", s.withAdmin(s.deleteProvider))
	mux.HandleFunc("POST /bridge/providers/{id}/refresh", s.withAdmin(s.refreshProvider))
	mux.HandleFunc("POST /bridge/models/{provider}/{model}/probe", s.withAdmin(s.probeModel))
	mux.HandleFunc("POST /bridge/probe", s.withAdmin(s.probeByBody))
	mux.HandleFunc("GET /bridge/models", s.withAdmin(s.bridgeModels))
	mux.HandleFunc("GET /bridge/stats", s.withAdmin(s.bridgeStats))
	mux.HandleFunc("GET /bridge/statistics", s.withAdmin(s.bridgeStatistics))
	mux.HandleFunc("GET /bridge/balance", s.withAdmin(s.bridgeBalance))
	mux.HandleFunc("GET /bridge/router", s.withAdmin(s.getRouter))
	mux.HandleFunc("GET /bridge/router/health", s.withAdmin(s.routerHealth))
	mux.HandleFunc("GET /bridge/router/diagnostics", s.withAdmin(s.routerDiagnostics))
	mux.HandleFunc("POST /bridge/router/evaluate", s.withAdmin(s.routerEvaluate))
	mux.HandleFunc("GET /bridge/router/quality", s.withAdmin(s.adaptiveStatus))
	mux.HandleFunc("PUT /bridge/router/quality", s.withAdmin(s.putAdaptiveConfig))
	mux.HandleFunc("POST /bridge/router/outcomes", s.withAdmin(s.adaptiveEvaluatorOutcome))
	mux.HandleFunc("GET /bridge/router/explain", s.withAdmin(s.routeExplain))
	mux.HandleFunc("GET /bridge/resilience", s.withAdmin(s.resilienceStatus))
	mux.HandleFunc("POST /bridge/resilience/reset", s.withAdmin(s.resetResilience))
	mux.HandleFunc("PUT /bridge/router", s.withAdmin(s.putRouter))
	mux.HandleFunc("GET /bridge/quota-cost", s.withAdmin(s.quotaCostStatus))
	mux.HandleFunc("PUT /bridge/quota-cost", s.withAdmin(s.putQuotaCostConfig))
	mux.HandleFunc("POST /bridge/quota-cost/refresh", s.withAdmin(s.refreshQuotaCost))
	mux.HandleFunc("POST /bridge/quota", s.withAdmin(s.setQuotaSnapshot))
	mux.HandleFunc("POST /bridge/pricing", s.withAdmin(s.setPricing))
	mux.HandleFunc("GET /bridge/context-guard", s.withAdmin(s.contextGuardStatus))
	mux.HandleFunc("PUT /bridge/context-guard", s.withAdmin(s.putContextGuard))
	mux.HandleFunc("POST /bridge/context-guard/preview", s.withAdmin(s.contextGuardPreview))
	mux.HandleFunc("GET /bridge/context-optimizer", s.withAdmin(s.contextGuardStatus))
	mux.HandleFunc("PUT /bridge/context-optimizer", s.withAdmin(s.putContextGuard))
	mux.HandleFunc("POST /bridge/context-optimizer/preview", s.withAdmin(s.contextGuardPreview))
	mux.HandleFunc("GET /bridge/cache", s.withAdmin(s.cacheStatus))
	mux.HandleFunc("PUT /bridge/cache", s.withAdmin(s.putCacheConfig))
	mux.HandleFunc("DELETE /bridge/cache", s.withAdmin(s.clearCache))
	mux.HandleFunc("GET /bridge/health/autopilot", s.withAdmin(s.healthAutopilot))
	mux.HandleFunc("POST /bridge/health/autopilot/actions", s.withAdmin(s.healthAutopilotAction))
	mux.HandleFunc("GET /bridge/integrations", s.withAdmin(s.integrationHubStatus))
	mux.HandleFunc("GET /bridge/integrations/{id}/plan", s.withAdmin(s.integrationPlan))
	mux.HandleFunc("POST /bridge/integrations/{id}/configured", s.withAdmin(s.integrationConfigured))
	mux.HandleFunc("GET /bridge/mcp", s.withAdmin(s.mcpStatus))
	mux.HandleFunc("POST /bridge/mcp", s.withAdmin(s.mcpUpsert))
	mux.HandleFunc("DELETE /bridge/mcp/{id}", s.withAdmin(s.mcpDelete))
	mux.HandleFunc("POST /bridge/mcp/{id}/probe", s.withAdmin(s.mcpProbe))
	mux.HandleFunc("POST /bridge/mcp/probe-all", s.withAdmin(s.mcpProbeAll))
	mux.HandleFunc("GET /v1/mcp/servers", s.withData(s.mcpDataServers))
	mux.HandleFunc("GET /bridge/observability", s.withAdmin(s.observabilityStatus))
	mux.HandleFunc("GET /bridge/traces", s.withAdmin(s.observabilityTraces))
	mux.HandleFunc("GET /bridge/remote", s.withAdmin(s.remoteStatus))
	mux.HandleFunc("PUT /bridge/remote", s.withAdmin(s.putRemote))
	mux.HandleFunc("GET /metrics", s.metricsHandler)
	mux.HandleFunc("GET /bridge/vscode", s.withAdmin(s.bridgeVSCode))
	mux.HandleFunc("GET /bridge/vscode/export", s.withAdmin(s.bridgeVSCodeExport))
	mux.HandleFunc("POST /bridge/vscode/self-test", s.withAdmin(s.vscodeSelfTest))
	mux.HandleFunc("POST /bridge/detect", s.withAdmin(s.detect))
	mux.HandleFunc("POST /bridge/provider-packs/validate", s.withAdmin(s.validateProviderPack))
	mux.HandleFunc("GET /bridge/catalog-sync", s.withAdmin(s.catalogSyncStatus))
	mux.HandleFunc("PUT /bridge/catalog-sync", s.withAdmin(s.putCatalogSync))
	mux.HandleFunc("POST /bridge/catalog-sync", s.withAdmin(s.runCatalogSync))
	mux.HandleFunc("POST /control/shutdown", s.withAdmin(s.shutdown))
	var h http.Handler = mux
	h = s.recoverPanics(h)
	h = s.remoteGuard(h)
	h = s.observe(h)
	return logging(h, s.Logger)
}

func acquireSlot(ctx context.Context, slots chan struct{}, wait time.Duration) (func(), bool) {
	if slots == nil {
		return func() {}, true
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, true
	case <-ctx.Done():
		return nil, false
	case <-timer.C:
		return nil, false
	}
}

func busy(w http.ResponseWriter, operation string) {
	w.Header().Set("Retry-After", "1")
	jsonOut(w, http.StatusTooManyRequests, map[string]any{"error": map[string]any{"message": "OmniBridge is busy; retry shortly", "type": "gateway_busy", "code": "gateway_busy", "operation": operation}})
}

func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				if s.Logger != nil {
					s.Logger.Printf("[recovery] request panic method=%s path=%s error=%v\n%s", r.Method, r.URL.Path, recovered, debug.Stack())
				}
				if ow, ok := w.(*observedWriter); !ok || ow.status == 0 {
					jsonOut(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"message": "OmniBridge recovered from an internal error", "type": "internal_recovered", "code": "internal_recovered"}})
				}
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) storageStatus(w http.ResponseWriter, r *http.Request) {
	if s.Storage == nil {
		jsonOut(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "persistence 2.0 storage is not attached"})
		return
	}
	counts, err := s.Storage.Counts()
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	check, err := s.Storage.QuickCheck()
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOut(w, http.StatusOK, map[string]any{"ok": check == "ok", "schema_version": storage.CurrentSchemaVersion, "journal_mode": "WAL", "quick_check": check, "counts": counts})
}

func (s *Server) storageCheckpoint(w http.ResponseWriter, r *http.Request) {
	if s.Storage == nil {
		jsonErr(w, http.StatusServiceUnavailable, "persistence 2.0 storage is not attached")
		return
	}
	if err := s.Storage.Checkpoint(); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOut(w, http.StatusOK, map[string]any{"ok": true, "checkpoint": "truncate"})
}

func (s *Server) Start() error {
	s.shutting.Store(false)
	c := s.Config.Get()
	if err := s.validateRemoteBind(c); err != nil {
		return err
	}
	s.httpServer = &http.Server{Addr: c.Listen, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
	s.Logger.Printf("OmniBridge %s listening on %s", config.CurrentVersion, c.Listen)
	if isNonLoopbackListen(c.Listen) && c.Remote.Enabled {
		s.Logger.Printf("[remote] TLS remote mode enabled; allowed CIDRs=%d", len(c.Remote.AllowedCIDRs))
		return s.httpServer.ListenAndServeTLS(c.Remote.TLSCertPath, c.Remote.TLSKeyPath)
	}
	return s.httpServer.ListenAndServe()
}

// Stop terminates the local gateway and its account sessions. It is safe to
// call from both the integrated desktop lifecycle and the admin HTTP handler.
func (s *Server) Stop(ctx context.Context) error {
	s.shutting.Store(true)
	if s.Accounts != nil {
		s.Accounts.Close()
	}
	if s.httpServer == nil {
		return nil
	}
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	c := s.Config.Get()
	jsonOut(w, http.StatusOK, map[string]any{"ok": true, "version": config.CurrentVersion, "listen": c.Listen, "lifecycle": s.lifecycle(), "shutting_down": s.shutting.Load(), "paused": s.paused.Load()})
}

func (s *Server) lifecycle() string {
	if s.shutting.Load() {
		return "Stopping"
	}
	if s.paused.Load() {
		return "Stopped"
	}
	cfg := s.Config.Get()
	configured := len(cfg.Providers) + len(cfg.Accounts)
	if configured == 0 {
		return "Ready"
	}
	mods := s.Registry.Models()
	if len(mods) == 0 {
		return "Starting"
	}
	ready, failed := 0, 0
	for _, m := range mods {
		if m.Status == "error" {
			failed++
		} else {
			ready++
		}
	}
	for _, a := range cfg.Accounts {
		if a.Status == "error" {
			failed++
		}
	}
	if ready == 0 && failed > 0 {
		return "Error"
	}
	if failed > 0 {
		return "Degraded"
	}
	return "Ready"
}

func (s *Server) withData(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authOK(r, s.Config.Get().DataAPIKey) {
			expected := bridgeKeyFingerprint(s.Config.Get().DataAPIKey)
			present := make([]string, 0, 4)
			if strings.TrimSpace(r.Header.Get("Authorization")) != "" {
				present = append(present, "authorization")
			}
			if strings.TrimSpace(r.Header.Get("x-api-key")) != "" {
				present = append(present, "x-api-key")
			}
			if strings.TrimSpace(r.Header.Get("api-key")) != "" {
				present = append(present, "api-key")
			}
			if strings.TrimSpace(r.Header.Get("x-bridge-key")) != "" {
				present = append(present, "x-bridge-key")
			}
			w.Header().Set("X-OmniBridge-Key-Fingerprint", expected)
			w.Header().Set("X-OmniBridge-Auth-Headers", strings.Join(present, ","))
			if s.Logger != nil {
				s.Logger.Printf("data-plane auth rejected: remote=%s headers=%s expected_fp=%s", r.RemoteAddr, strings.Join(present, ","), expected)
			}
			jsonErr(w, http.StatusUnauthorized, "invalid local bridge key")
			return
		}
		if s.paused.Load() {
			jsonOut(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]any{"message": "OmniBridge gateway is paused from Tray", "type": "gateway_paused", "code": "gateway_paused"}})
			return
		}
		next(w, r)
	}
}
func (s *Server) withAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authOK(r, s.Config.Get().AdminAPIKey) {
			jsonErr(w, http.StatusUnauthorized, "invalid admin key")
			return
		}
		next(w, r)
	}
}

func bearerToken(v string) string {
	parts := strings.Fields(strings.TrimSpace(v))
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return parts[1]
	}
	return ""
}

func authOK(r *http.Request, key string) bool {
	expected := sha256.Sum256([]byte(key))
	candidates := []string{bearerToken(r.Header.Get("Authorization")), r.Header.Get("x-api-key"), r.Header.Get("api-key"), r.Header.Get("x-bridge-key")}
	for _, v := range candidates {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		candidate := sha256.Sum256([]byte(v))
		if subtle.ConstantTimeCompare(candidate[:], expected[:]) == 1 {
			return true
		}
	}
	return false
}

func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	mods := s.Registry.Models()
	data := make([]map[string]any, 0, len(mods)+len(autoVirtualModels))
	for _, v := range autoVirtualModels {
		data = append(data, map[string]any{"id": v, "object": "model", "owned_by": "omnibridge-router", "type": "virtual"})
	}
	for _, m := range mods {
		data = append(data, map[string]any{"id": m.ID, "object": "model", "owned_by": m.ProviderID, "type": m.Type, "name": m.Name, "native_id": m.NativeID})
	}
	jsonOut(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (s *Server) providerByID(id string) (config.Provider, bool) {
	for _, p := range s.Config.Get().Providers {
		if p.ID == id && p.Enabled {
			return p, true
		}
	}
	return config.Provider{}, false
}

func (s *Server) accountByID(id string) (config.AccountConnection, bool) {
	for _, a := range s.Config.Get().Accounts {
		if a.ID == id && a.Enabled {
			return a, true
		}
	}
	return config.AccountConnection{}, false
}

func (s *Server) resolveModel(modelID string) (providers.Model, error) {
	if target := strings.TrimSpace(s.Config.Get().Routing.Aliases[modelID]); target != "" {
		modelID = target
	}
	if m, err := s.Registry.Find(modelID); err == nil {
		return m, nil
	}
	matches := []providers.Model{}
	for _, m := range s.Registry.Models() {
		if m.NativeID == modelID {
			matches = append(matches, m)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) == 0 {
		return providers.Model{}, fmt.Errorf("model %q not found", modelID)
	}
	return providers.Model{}, fmt.Errorf("model %q is ambiguous; use source/model", modelID)
}

func (s *Server) isLocalModel(m providers.Model) bool {
	if m.Source == "account" {
		return false
	}
	p, ok := s.providerByID(m.ProviderID)
	if !ok {
		return false
	}
	switch strings.ToLower(p.Kind) {
	case "ollama", "lmstudio", "vllm", "localai":
		return true
	}
	base := strings.ToLower(p.BaseURL)
	return strings.Contains(base, "127.0.0.1") || strings.Contains(base, "localhost") || strings.Contains(base, "[::1]")
}

func modelAgentReady(m providers.Model) bool {
	return m.DoctorVerified && m.ChatVerified && m.IdentityVerified && m.ToolCalling && m.AgentReady && strings.EqualFold(m.DoctorStatus, "agent_ready")
}

func resilienceConfig(r config.RoutingConfig) resilience.Config {
	threshold := r.CircuitBreakerThreshold
	if threshold <= 0 {
		threshold = 3
	}
	cooldown := time.Duration(r.CircuitBreakerCooldownSeconds) * time.Second
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	degraded := (threshold*60 + 99) / 100
	if degraded < 1 {
		degraded = 1
	}
	return resilience.Config{
		FailureThreshold:     threshold,
		DegradationThreshold: degraded,
		BaseCooldown:         cooldown,
		MaxCooldown:          30 * time.Minute,
		HalfOpenProbes:       1,
	}
}

func (s *Server) refreshResilienceConfig() {
	if s.Resilience != nil {
		s.Resilience.Reconfigure(resilienceConfig(s.Config.Get().Routing))
	}
}

func (s *Server) refreshRegistry(ctx context.Context) registry.Snapshot {
	if s.ControlFlight == nil {
		return s.Registry.RefreshAll(ctx)
	}
	v, err, _ := s.ControlFlight.Do(ctx, "model-discovery", func() (any, error) {
		opCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		return s.Registry.RefreshAll(opCtx), nil
	})
	if err != nil {
		return s.Registry.Snapshot()
	}
	if snap, ok := v.(registry.Snapshot); ok {
		return snap
	}
	return s.Registry.Snapshot()
}

func resilienceConnectionKey(m providers.Model) string {
	return resilience.ConnectionKey(m.Source, m.ProviderID)
}

func (s *Server) modelBlocked(m providers.Model) bool {
	if s.Resilience == nil {
		return false
	}
	if !s.Resilience.Blocked(resilienceConnectionKey(m)).Allowed {
		return true
	}
	return !s.Resilience.Blocked(resilience.ModelKey(m.ID)).Allowed
}

func (s *Server) acquireModelResilience(m providers.Model) bool {
	if s.Resilience == nil {
		return true
	}
	if !s.Resilience.Blocked(resilienceConnectionKey(m)).Allowed || !s.Resilience.Blocked(resilience.ModelKey(m.ID)).Allowed {
		return false
	}
	if !s.Resilience.Acquire(resilienceConnectionKey(m)).Allowed {
		return false
	}
	if !s.Resilience.Acquire(resilience.ModelKey(m.ID)).Allowed {
		return false
	}
	return true
}

func (s *Server) recordResilienceFailure(m providers.Model, class resilience.Classification) {
	if s.Resilience == nil || class.Kind == "" {
		return
	}
	key := resilienceConnectionKey(m)
	if class.Kind == resilience.FailureModelUnavailable {
		key = resilience.ModelKey(m.ID)
	}
	s.Resilience.RecordFailure(key, class.Kind, class.Reason, class.Cooldown)
}

func (s *Server) recordResilienceSuccess(m providers.Model) {
	if s.Resilience == nil {
		return
	}
	s.Resilience.RecordSuccess(resilienceConnectionKey(m))
	s.Resilience.RecordSuccess(resilience.ModelKey(m.ID))
}

func (s *Server) resolveRoutingID(id string) ([]providers.Model, error) {
	if target := strings.TrimSpace(s.Config.Get().Routing.Aliases[id]); target != "" {
		id = target
	}
	out := []providers.Model{}
	seen := map[string]bool{}
	// 1) Exact registry ID is authoritative (provider/native-qualified form).
	if m, err := s.Registry.Find(id); err == nil {
		out = append(out, m)
		seen[m.ID] = true
	}
	// 2) Yandex and other native providers expose ids that contain '/' (for
	// example "gpt://<folder>/yandexgpt/latest"). Match an exact native id too so
	// requests coming from older BYOK VS Code entries work without re-sync.
	for _, m := range s.Registry.Models() {
		if !seen[m.ID] && m.NativeID == id {
			seen[m.ID] = true
			out = append(out, m)
		}
	}
	// 3) Allow short native refs without the folder prefix ("yandexgpt/latest").
	if len(out) == 0 {
		want := providers.NativeModelSlug(id)
		for _, m := range s.Registry.Models() {
			if providers.NativeModelSlug(m.NativeID) == want {
				if !seen[m.ID] {
					seen[m.ID] = true
					out = append(out, m)
				}
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("model %q not found", id)
	}
	if len(out) > 1 {
		sort.SliceStable(out, func(i, j int) bool {
			if s.Resilience != nil {
				ri := s.Resilience.HealthRank(resilienceConnectionKey(out[i]))
				rj := s.Resilience.HealthRank(resilienceConnectionKey(out[j]))
				if ri != rj {
					return ri < rj
				}
			}
			return out[i].ID < out[j].ID
		})
	}
	return out, nil
}

func (s *Server) routerCandidatePool(policy config.RoutingPolicy) ([]providers.Model, error) {
	if len(policy.Candidates) == 0 {
		mods := s.Registry.Models()
		if len(mods) == 0 {
			return nil, errors.New("Model Registry is empty")
		}
		return mods, nil
	}
	out := make([]providers.Model, 0, len(policy.Candidates))
	seenModels := map[string]bool{}
	var firstErr error
	for _, id := range policy.Candidates {
		mods, err := s.resolveRoutingID(id)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, m := range mods {
			if seenModels[m.ID] {
				continue
			}
			seenModels[m.ID] = true
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		if firstErr == nil {
			firstErr = errors.New("routing policy has no usable candidates")
		}
		return nil, firstErr
	}
	return out, nil
}

func (s *Server) applyRoutingPolicy(policy config.RoutingPolicy) ([]providers.Model, error) {
	return s.applyRoutingPolicyFor("policy", policy, "", "")
}

func (s *Server) applyRoutingPolicyFor(routeKey string, policy config.RoutingPolicy, profile, sessionKey string) ([]providers.Model, error) {
	return s.applyRoutingPolicyForRequest(routeKey, policy, profile, sessionKey, obr.Requirements{}, false)
}

func (s *Server) applyRoutingPolicyForRequest(routeKey string, policy config.RoutingPolicy, profile, sessionKey string, req obr.Requirements, requireDoctorProof bool) ([]providers.Model, error) {
	mods, err := s.routerCandidatePool(policy)
	if err != nil {
		return nil, err
	}
	filtered, rejected := s.filterRouterModelsForRequest(mods, policy.Mode, profile, req, requireDoctorProof)
	if len(filtered) == 0 {
		if len(rejected) > 0 {
			b, _ := json.Marshal(rejected)
			return nil, fmt.Errorf("routing policy %s has no request-compatible candidates: %s", policy.Mode, string(b))
		}
		return nil, fmt.Errorf("routing policy %s has no healthy candidates", policy.Mode)
	}
	ordered := s.orderRouterModelsForRequest(routeKey, policy, profile, sessionKey, filtered, req)
	if len(ordered) == 0 {
		return nil, fmt.Errorf("routing policy %s has no ordered candidates", policy.Mode)
	}
	return ordered, nil
}

func (s *Server) routingCandidates(requested string) ([]providers.Model, error) {
	return s.routingCandidatesFor(requested, "")
}

func (s *Server) routingCandidatesFor(requested, sessionKey string) ([]providers.Model, error) {
	return s.routingCandidatesForRequest(requested, sessionKey, obr.Requirements{})
}

func (s *Server) routingCandidatesForRequest(requested, sessionKey string, req obr.Requirements) ([]providers.Model, error) {
	r := s.Config.Get().Routing
	if isAutoVirtualModel(requested) {
		policy, profile := autoPolicy(requested)
		req = requirementsForProfile(req, profile)
		return s.applyRoutingPolicyForRequest(requested, policy, profile, sessionKey, req, true)
	}
	if policy, ok := r.Policies[requested]; ok {
		strict := strings.EqualFold(policy.Mode, "auto")
		return s.applyRoutingPolicyForRequest(requested, policy, "smart", sessionKey, req, strict)
	}
	ids := append([]string(nil), r.Priorities[requested]...)
	if len(ids) == 0 {
		if target := strings.TrimSpace(r.Aliases[requested]); target != "" {
			ids = append(ids, target)
		} else {
			ids = append(ids, requested)
		}
	}
	ids = append(ids, r.Fallbacks[requested]...)
	seenModels := map[string]bool{}
	out := make([]providers.Model, 0, len(ids))
	var firstErr error
	for _, id := range ids {
		mods, err := s.resolveRoutingID(id)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, m := range mods {
			if seenModels[m.ID] {
				continue
			}
			seenModels[m.ID] = true
			if m.Status == "error" || s.modelBlocked(m) {
				continue
			}
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		if firstErr == nil {
			firstErr = fmt.Errorf("model %q has no healthy routing candidates", requested)
		}
		return nil, firstErr
	}
	compatible, rejected := s.filterRouterModelsForRequest(out, "priority", "", req, false)
	if len(compatible) == 0 {
		b, _ := json.Marshal(rejected)
		return nil, fmt.Errorf("model %q has no request-compatible routing candidates: %s", requested, string(b))
	}
	return s.orderRouterModelsForRequest(requested, config.RoutingPolicy{Mode: "priority"}, "", sessionKey, compatible, req), nil
}

func requestForModel(body []byte, modelID string) []byte {
	var v map[string]any
	if json.Unmarshal(body, &v) != nil {
		return body
	}
	v["model"] = modelID
	b, err := json.Marshal(v)
	if err != nil {
		return body
	}
	return b
}

func modelIdentityInstruction(m providers.Model) string {
	name := strings.TrimSpace(m.Name)
	if name == "" {
		name = strings.TrimSpace(m.NativeID)
	}
	provider := strings.TrimSpace(m.ProviderName)
	if provider == "" {
		provider = strings.TrimSpace(m.ProviderID)
	}
	if m.IdentityVerified {
		return fmt.Sprintf("Your verified upstream identity is %s, served by %s through OmniBridge. GitHub Copilot Chat and VS Code are only the host interface. Do not identify yourself as GitHub Copilot, VS Code, or OmniBridge. When the user asks which model you are, state exactly that you are %s and name provider %s. Otherwise follow the user's request normally.", name, provider, name, provider)
	}
	return fmt.Sprintf("OmniBridge selected provider-reported model %s from %s, but its physical identity has not yet passed Model Doctor verification. GitHub Copilot Chat and VS Code are only the host interface. If asked which model you are, report the provider-reported name %s and explicitly say that identity is unverified. Do not identify yourself as GitHub Copilot, VS Code, or OmniBridge.", name, provider, name)
}

func injectModelIdentity(body []byte, apiType string, m providers.Model) []byte {
	var v map[string]any
	if json.Unmarshal(body, &v) != nil {
		return body
	}
	instruction := modelIdentityInstruction(m)
	switch apiType {
	case protocol.Responses:
		if current, ok := v["instructions"].(string); ok && strings.TrimSpace(current) != "" {
			v["instructions"] = current + "\n\n" + instruction
		} else {
			v["instructions"] = instruction
		}
	case protocol.Messages:
		switch current := v["system"].(type) {
		case string:
			if strings.TrimSpace(current) == "" {
				v["system"] = instruction
			} else {
				v["system"] = current + "\n\n" + instruction
			}
		case []any:
			v["system"] = append(current, map[string]any{"type": "text", "text": instruction})
		default:
			v["system"] = instruction
		}
	default:
		messages, _ := v["messages"].([]any)
		identity := map[string]any{"role": "system", "content": instruction}
		insertAt := len(messages)
		for i := len(messages) - 1; i >= 0; i-- {
			msg, _ := messages[i].(map[string]any)
			role, _ := msg["role"].(string)
			if role == "user" {
				insertAt = i
				break
			}
		}
		messages = append(messages, nil)
		copy(messages[insertAt+1:], messages[insertAt:])
		messages[insertAt] = identity
		v["messages"] = messages
	}
	b, err := json.Marshal(v)
	if err != nil {
		return body
	}
	return b
}

func normalizeResponseModelJSON(body []byte, m providers.Model) []byte {
	var v map[string]any
	if json.Unmarshal(body, &v) != nil {
		return body
	}
	v["model"] = m.ID
	v["omnibridge_provider"] = m.ProviderID
	v["omnibridge_native_model"] = m.NativeID
	b, err := json.Marshal(v)
	if err != nil {
		return body
	}
	return b
}

func safeResponseHeaderValue(v string) string {
	v = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, strings.TrimSpace(v))
	if len(v) > 512 {
		v = v[:512]
	}
	return v
}

func setSelectedModelHeaders(h http.Header, requested string, m providers.Model, fallback bool) {
	h.Set("X-OmniBridge-Selected-Model", safeResponseHeaderValue(m.ID))
	h.Set("X-OmniBridge-Provider", safeResponseHeaderValue(m.ProviderID))
	h.Set("X-OmniBridge-Native-Model", safeResponseHeaderValue(m.NativeID))
	h.Set("X-OmniBridge-Route", safeResponseHeaderValue(requested))
	h.Set("X-OmniBridge-Fallback-Used", strconv.FormatBool(fallback))
	h.Set("Access-Control-Expose-Headers", "X-OmniBridge-Selected-Model, X-OmniBridge-Provider, X-OmniBridge-Native-Model, X-OmniBridge-Route, X-OmniBridge-Fallback-Used, X-OmniBridge-Trace-ID, X-OmniBridge-Attempts, X-OmniBridge-Latency-Ms, X-OmniBridge-TTFT-Ms, X-OmniBridge-Cost-USD, X-OmniBridge-Cache-Hit, X-OmniBridge-Dedup-Hit, X-OmniBridge-Context-Mode")
}

func retryableStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func normalizedUpstreamError(status int) string {
	switch status {
	case http.StatusTooManyRequests:
		return "upstream_provider_rate_limit"
	case http.StatusUnauthorized, http.StatusForbidden:
		return "upstream_auth"
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return "upstream_timeout"
	case http.StatusServiceUnavailable:
		return "upstream_provider_capacity"
	default:
		return "upstream_provider_unavailable"
	}
}

func retryDelay(h http.Header, attempt int) time.Duration {
	if h != nil {
		if seconds, err := strconv.Atoi(strings.TrimSpace(h.Get("Retry-After"))); err == nil && seconds > 0 {
			if seconds > 5 {
				seconds = 5
			}
			return time.Duration(seconds) * time.Second
		}
	}
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 3 {
		attempt = 3
	}
	return time.Duration(1<<(attempt-1)) * time.Second
}

func waitRetry(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

type upstreamCall struct {
	Status          int
	Header          http.Header
	Body            io.ReadCloser
	TargetProtocol  string
	CrossProtocol   bool
	RequestedStream bool
}

func chooseProviderProtocol(p config.Provider, m providers.Model, requested string) (string, error) {
	if config.SupportsProtocol(p, requested) {
		return requested, nil
	}
	seen := map[string]bool{}
	candidates := []string{m.PreferredProtocol, m.APIType, p.PreferredProtocol}
	candidates = append(candidates, m.Protocols...)
	candidates = append(candidates, p.Protocols...)
	for _, target := range candidates {
		if target == "" || seen[target] {
			continue
		}
		seen[target] = true
		if config.SupportsProtocol(p, target) && protocol.CanTranslate(requested, target) {
			return target, nil
		}
	}
	return "", fmt.Errorf("provider %s has no protocol compatible with %s", p.ID, requested)
}

func (s *Server) upstream(ctx context.Context, m providers.Model, apiType string, body []byte) (upstreamCall, error) {
	body = requestForModel(body, m.ID)
	body = injectModelIdentity(body, apiType, m)
	if m.Source == "account" {
		a, ok := s.accountByID(m.ProviderID)
		if !ok {
			return upstreamCall{}, errors.New("account connector not found or disabled")
		}
		target := "chat-completions"
		if !protocol.CanTranslate(apiType, target) {
			return upstreamCall{}, fmt.Errorf("account %s cannot translate %s", a.ID, apiType)
		}
		translated, requestedStream, _, err := protocol.TranslateRequestDetailed(apiType, target, body)
		if err != nil {
			return upstreamCall{}, err
		}
		up, err := s.Accounts.Proxy(ctx, a, target, translated)
		if up == nil {
			return upstreamCall{}, err
		}
		return upstreamCall{Status: up.StatusCode, Header: up.Header, Body: up.Body, TargetProtocol: target, CrossProtocol: apiType != target, RequestedStream: requestedStream}, err
	}
	p, ok := s.providerByID(m.ProviderID)
	if !ok {
		return upstreamCall{}, errors.New("API provider not found or disabled")
	}
	target, err := chooseProviderProtocol(p, m, apiType)
	if err != nil {
		return upstreamCall{}, err
	}
	translated, requestedStream, _, err := protocol.TranslateRequestDetailed(apiType, target, body)
	if err != nil {
		return upstreamCall{}, err
	}
	up, err := s.Providers.Proxy(ctx, p, target, translated)
	if up == nil {
		return upstreamCall{}, err
	}
	return upstreamCall{Status: up.StatusCode, Header: up.Header, Body: up.Body, TargetProtocol: target, CrossProtocol: target != apiType, RequestedStream: requestedStream}, err
}

func captureUpstreamErrorBody(call *upstreamCall) []byte {
	if call == nil || call.Body == nil || call.Status < 400 {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(call.Body, 1<<20))
	_ = call.Body.Close()
	call.Body = io.NopCloser(bytes.NewReader(raw))
	return raw
}

func fallbackEligible(class resilience.Classification) bool {
	switch class.Kind {
	case resilience.FailureRateLimit, resilience.FailureQuotaExhausted, resilience.FailureTimeout,
		resilience.FailureNetwork, resilience.FailureProvider5xx, resilience.FailureAuthentication,
		resilience.FailureModelUnavailable:
		return true
	default:
		return false
	}
}

func sameConnection(a, b providers.Model) bool {
	return resilienceConnectionKey(a) == resilienceConnectionKey(b)
}

func (s *Server) proxy(w http.ResponseWriter, r *http.Request, apiType string) {
	release, ok := acquireSlot(r.Context(), s.generationSlots, 2*time.Second)
	if !ok {
		busy(w, "generation")
		return
	}
	defer release()
	s.refreshResilienceConfig()
	start := time.Now()
	maxBody := int64(32 << 20)
	if n := s.Config.Get().ContextGuard.MaxRequestBytes; n > 0 {
		maxBody = int64(n)
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if int64(len(body)) > maxBody {
		jsonErr(w, http.StatusRequestEntityTooLarge, "request exceeds Context Guard max_request_bytes")
		return
	}
	var meta struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &meta); err != nil || meta.Model == "" {
		jsonErr(w, 400, "JSON body must contain model")
		return
	}
	annotateTrace(r.Context(), func(tr *observability.Trace) { tr.Model = meta.Model })
	if s.QuotaCost != nil {
		s.QuotaCost.UpdateConfig(s.Config.Get().QuotaCost)
		if reject, reason := s.QuotaCost.ShouldRejectForBudget(); reject {
			annotateTrace(r.Context(), func(tr *observability.Trace) { tr.ErrorKind = reason })
			jsonOut(w, http.StatusPaymentRequired, map[string]any{"error": map[string]any{"message": "OmniBridge budget limit reached", "type": "budget_exceeded", "code": reason}, "budget": s.QuotaCost.BudgetStatus()})
			return
		}
	}
	body, contextReport := s.applyContextGuard(body, apiType, meta.Model)
	annotateTrace(r.Context(), func(tr *observability.Trace) { tr.ContextAction = contextReport.Action })
	sessionKey := extractRoutingSession(r, body)
	req := obr.AnalyzeRequest(body, apiType)
	annotateTrace(r.Context(), func(tr *observability.Trace) { tr.Streaming = req.Streaming })
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("X-OmniBridge-Agent")), "true") || strings.TrimSpace(r.Header.Get("X-Agent-Mode")) == "1" {
		req.AgentMode = true
		req.RequiresDoctorProof = true
	}
	candidates, err := s.routingCandidatesForRequest(meta.Model, sessionKey, req)
	if err != nil {
		status := http.StatusNotFound
		code := "routing_candidates_unavailable"
		if strings.Contains(err.Error(), "request-compatible") || strings.Contains(err.Error(), "doctor_verification_required") {
			status = http.StatusUnprocessableEntity
			code = "model_capability_mismatch"
		}
		jsonOut(w, status, map[string]any{"error": map[string]any{"message": err.Error(), "type": code, "code": code}, "requirements": req})
		return
	}
	cacheSignature := ""
	dedupLeader := false
	dedupFinished := false
	if s.Generation != nil && len(candidates) > 0 && !strings.EqualFold(strings.TrimSpace(r.Header.Get("X-OmniBridge-Cache-Bypass")), "true") && generation.Cacheable(body) {
		s.Generation.UpdateConfig(s.Config.Get().ExactCache)
		first := candidates[0]
		namespace := strings.TrimSpace(r.Header.Get("X-OmniBridge-Cache-Namespace"))
		if namespace == "" {
			namespace = bridgeKeyFingerprint(s.Config.Get().DataAPIKey)
		}
		namespace = generation.Namespace(namespace)
		identityVersion := generation.IdentityVersion(first.ID, first.UpstreamModelID, first.DoctorCheckedAt)
		cacheSignature = generation.Signature(namespace, first.ProviderID, first.ID, first.NativeID, identityVersion, body)
		if s.Config.Get().ExactCache.Enabled {
			if cached, hit := s.Generation.Get(cacheSignature); hit {
				setSelectedModelHeaders(w.Header(), meta.Model, first, false)
				w.Header().Set("X-OmniBridge-Cache-Hit", "true")
				w.Header().Set("X-OmniBridge-Dedup-Hit", "false")
				w.Header().Set("Content-Type", cached.ContentType)
				annotateTrace(r.Context(), func(tr *observability.Trace) {
					tr.SelectedModel = first.ID
					tr.Provider = first.ProviderID
					tr.Attributes["cache_hit"] = true
				})
				if s.Adaptive != nil {
					if tr := observability.FromContext(r.Context()); tr != nil {
						s.Adaptive.RecordDecision(adaptive.Decision{TraceID: tr.ID, At: start.UTC(), Route: meta.Model, RequestedModel: meta.Model, SelectedModel: first.ID, ProviderID: first.ProviderID, NativeModelID: first.NativeID, Attempts: 0, CacheHit: true, ContextMode: contextReport.Mode, Candidates: []adaptive.CandidateDecision{{ModelID: first.ID, ProviderID: first.ProviderID, NativeModelID: first.NativeID, Status: "cache_hit"}}})
					}
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(cached.Body)
				return
			}
		}
		wait, leader := s.Generation.Join(cacheSignature)
		dedupLeader = leader
		if !leader {
			select {
			case <-wait:
				if cached, ok := s.Generation.WaitResult(cacheSignature); ok {
					setSelectedModelHeaders(w.Header(), meta.Model, first, false)
					w.Header().Set("X-OmniBridge-Cache-Hit", "false")
					w.Header().Set("X-OmniBridge-Dedup-Hit", "true")
					w.Header().Set("Content-Type", cached.ContentType)
					annotateTrace(r.Context(), func(tr *observability.Trace) {
						tr.SelectedModel = first.ID
						tr.Provider = first.ProviderID
						tr.Attributes["dedup_hit"] = true
					})
					if s.Adaptive != nil {
						if tr := observability.FromContext(r.Context()); tr != nil {
							s.Adaptive.RecordDecision(adaptive.Decision{TraceID: tr.ID, At: start.UTC(), Route: meta.Model, RequestedModel: meta.Model, SelectedModel: first.ID, ProviderID: first.ProviderID, NativeModelID: first.NativeID, Attempts: 0, DedupHit: true, ContextMode: contextReport.Mode, Candidates: []adaptive.CandidateDecision{{ModelID: first.ID, ProviderID: first.ProviderID, NativeModelID: first.NativeID, Status: "dedup_hit"}}})
						}
					}
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write(cached.Body)
					return
				}
			case <-r.Context().Done():
				return
			}
		}
	}
	if dedupLeader {
		defer func() {
			if !dedupFinished {
				s.Generation.Complete(cacheSignature, generation.Entry{}, false)
			}
		}()
	}
	routing := s.Config.Get().Routing
	maxAttempts := routing.MaxRetries + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	attemptModels := append([]providers.Model(nil), candidates...)
	if len(attemptModels) == 1 {
		for len(attemptModels) < maxAttempts {
			attemptModels = append(attemptModels, attemptModels[0])
		}
	} else if len(attemptModels) > maxAttempts {
		attemptModels = attemptModels[:maxAttempts]
	}
	var m providers.Model
	var call upstreamCall
	var lastClass resilience.Classification
	attempted := false
	attempts := 0
	decision := adaptive.Decision{At: start.UTC(), Route: meta.Model, RequestedModel: meta.Model, Strategy: "priority", ContextMode: contextReport.Mode}
	if p, ok := routing.Policies[meta.Model]; ok {
		decision.Strategy = p.Mode
	} else if isAutoVirtualModel(meta.Model) {
		p, _ := autoPolicy(meta.Model)
		decision.Strategy = p.Mode
	}
	if tr := observability.FromContext(r.Context()); tr != nil {
		decision.TraceID = tr.ID
	}
	scoreByID := map[string]obr.Score{}
	for _, sc := range s.routingScoresForRequest(meta.Model, req) {
		scoreByID[sc.CandidateID] = sc
	}
	for _, candidate := range candidates {
		cd := adaptive.CandidateDecision{ModelID: candidate.ID, ProviderID: candidate.ProviderID, NativeModelID: candidate.NativeID, Status: "not_reached"}
		if sc, ok := scoreByID[candidate.ID]; ok {
			cd.Score = sc.Total
			cd.Components = sc.Components
		}
		decision.Candidates = append(decision.Candidates, cd)
	}
	setDecisionStatus := func(id, status string, reasons ...string) {
		for i := range decision.Candidates {
			if decision.Candidates[i].ModelID == id {
				decision.Candidates[i].Status = status
				decision.Candidates[i].Reasons = append([]string(nil), reasons...)
				return
			}
		}
	}
	defer func() {
		if s.Adaptive != nil {
			decision.SelectedModel = m.ID
			decision.ProviderID = m.ProviderID
			decision.NativeModelID = m.NativeID
			decision.Attempts = attempts
			decision.Fallback = attempts > 1 || (len(candidates) > 0 && m.ID != "" && m.ID != candidates[0].ID)
			decision.LatencyMS = time.Since(start).Milliseconds()
			s.Adaptive.RecordDecision(decision)
		}
	}()
	for attempt, candidate := range attemptModels {
		if !s.acquireModelResilience(candidate) {
			setDecisionStatus(candidate.ID, "skipped", "circuit_or_cooldown")
			s.Logger.Printf("[resilience] skip blocked model=%s connection=%s", candidate.ID, resilienceConnectionKey(candidate))
			continue
		}
		attemptStarted := time.Now()
		attempted = true
		attempts = attempt + 1
		m = candidate
		annotateTrace(r.Context(), func(tr *observability.Trace) {
			tr.SelectedModel = m.ID
			tr.Provider = m.ProviderID
			tr.Attempts = attempt + 1
		})
		call, err = s.upstream(r.Context(), m, apiType, body)
		if s.QuotaCost != nil && call.Header != nil {
			s.QuotaCost.ObserveHeaders(m.ProviderID, call.Header)
		}
		annotateTrace(r.Context(), func(tr *observability.Trace) { tr.CrossProtocol = call.CrossProtocol })
		errorBody := captureUpstreamErrorBody(&call)
		lastClass = resilience.Classify(call.Status, errorBody, call.Header, err, r.Context().Err())
		if s.Adaptive != nil {
			s.Adaptive.Observe(adaptive.Outcome{TraceID: decision.TraceID, Route: meta.Model, ProviderID: candidate.ProviderID, ConnectionID: candidate.ProviderID, ModelID: candidate.ID, NativeModelID: candidate.NativeID, Success: err == nil && call.Status/100 == 2, RateLimited: call.Status == http.StatusTooManyRequests, Anomaly: err != nil || call.Status >= 500, LatencyMS: time.Since(attemptStarted).Milliseconds(), At: time.Now().UTC()})
		}
		if lastClass.Kind != "" {
			setDecisionStatus(candidate.ID, "failed", string(lastClass.Kind))
			s.recordResilienceFailure(m, lastClass)
			if fallbackEligible(lastClass) && s.Router != nil && sessionKey != "" {
				s.Router.Release(meta.Model, sessionKey, m.ID)
			}
		}
		if err == nil && call.Status/100 == 2 {
			setDecisionStatus(candidate.ID, "dispatched")
			s.recordResilienceSuccess(m)
			if s.Router != nil && sessionKey != "" && routing.SessionStickiness {
				ttl := time.Duration(routing.SessionTTLSeconds) * time.Second
				s.Router.Pin(meta.Model, sessionKey, m.ID, ttl)
			}
			break
		}
		if !fallbackEligible(lastClass) || attempt == len(attemptModels)-1 {
			break
		}
		if call.Body != nil {
			_ = call.Body.Close()
			call.Body = nil
		}
		s.Logger.Printf("[router] fallback model=%s kind=%s status=%d attempt=%d", m.ID, lastClass.Kind, call.Status, attempt+1)
		// Prefer a different connection immediately. Backoff only when the next
		// attempt would hit the same connection again (single-provider retry).
		if attempt+1 < len(attemptModels) && sameConnection(m, attemptModels[attempt+1]) &&
			(lastClass.Kind == resilience.FailureRateLimit || lastClass.Kind == resilience.FailureTimeout || lastClass.Kind == resilience.FailureNetwork || lastClass.Kind == resilience.FailureProvider5xx) {
			delay := retryDelay(call.Header, attempt+1)
			if lastClass.Cooldown > 0 && lastClass.Cooldown < delay {
				delay = lastClass.Cooldown
			}
			if !waitRetry(r.Context(), delay) {
				err = r.Context().Err()
				break
			}
		}
	}
	if !attempted {
		jsonOut(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]any{"message": "all routing candidates are blocked by resilience policy", "type": "upstream_circuit_open", "code": "upstream_circuit_open"}})
		return
	}
	if err != nil {
		s.recordUsageCost(r.Context(), m, true, 0, 0, 0, time.Since(start).Milliseconds())
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		jsonErr(w, 502, "upstream_provider_unavailable: "+err.Error())
		return
	}
	if call.Body == nil {
		jsonErr(w, 502, "empty upstream response")
		return
	}
	defer call.Body.Close()
	fallbackUsed := attempts > 1 || (len(candidates) > 0 && m.ID != candidates[0].ID)
	setSelectedModelHeaders(w.Header(), meta.Model, m, fallbackUsed)
	w.Header().Set("X-OmniBridge-Attempts", strconv.Itoa(attempts))
	w.Header().Set("X-OmniBridge-Latency-Ms", strconv.FormatInt(time.Since(start).Milliseconds(), 10))
	w.Header().Set("X-OmniBridge-TTFT-Ms", "0")
	w.Header().Set("X-OmniBridge-Cache-Hit", "false")
	w.Header().Set("X-OmniBridge-Dedup-Hit", "false")
	w.Header().Set("X-OmniBridge-Context-Mode", safeResponseHeaderValue(contextReport.Mode))
	s.Logger.Printf("[router] route=%s selected=%s provider=%s native=%s attempts=%d fallback=%t", meta.Model, m.ID, m.ProviderID, m.NativeID, attempts, fallbackUsed)
	if call.Status/100 != 2 {
		s.recordUsageCost(r.Context(), m, true, 0, 0, 0, time.Since(start).Milliseconds())
		if retryableStatus(call.Status) {
			jsonOut(w, call.Status, map[string]any{"error": map[string]any{"message": normalizedUpstreamError(call.Status), "type": normalizedUpstreamError(call.Status), "upstream_status": call.Status}})
			return
		}
		copyHeaders(w.Header(), call.Header)
		w.WriteHeader(call.Status)
		_, _ = io.Copy(w, call.Body)
		return
	}

	// Cross-protocol streams are translated live through CanonicalStreamEvent.
	// The read->decode->encode->write loop is deliberately synchronous: the
	// downstream socket provides natural backpressure and r.Context() propagates
	// cancellation to the upstream HTTP request.
	if call.CrossProtocol {
		isSSE := strings.Contains(strings.ToLower(call.Header.Get("Content-Type")), "text/event-stream")
		if call.RequestedStream && isSSE {
			if dedupLeader {
				s.Generation.Complete(cacheSignature, generation.Entry{}, false)
				dedupFinished = true
			}
			w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			w.Header().Set("X-Accel-Buffering", "no")
			w.WriteHeader(http.StatusOK)
			flusher, _ := w.(http.Flusher)
			var usage protocol.CanonicalUsage
			writeChunk := func(b []byte) error {
				if len(b) == 0 {
					return nil
				}
				if _, err := w.Write(b); err != nil {
					return err
				}
				if flusher != nil {
					flusher.Flush()
				}
				return nil
			}
			observe := func(ev protocol.CanonicalStreamEvent) {
				if ev.Type == protocol.StreamUsage {
					if ev.Usage.InputTokens != 0 {
						usage.InputTokens = ev.Usage.InputTokens
					}
					if ev.Usage.OutputTokens != 0 {
						usage.OutputTokens = ev.Usage.OutputTokens
					}
					if ev.Usage.TotalTokens != 0 {
						usage.TotalTokens = ev.Usage.TotalTokens
					}
				}
			}
			streamErr := protocol.TranslateSSEStream(call.TargetProtocol, apiType, call.Body, writeChunk, observe)
			if usage.TotalTokens == 0 {
				usage.TotalTokens = usage.InputTokens + usage.OutputTokens
			}
			s.recordUsageCost(r.Context(), m, streamErr != nil && r.Context().Err() == nil, usage.InputTokens, usage.OutputTokens, usage.TotalTokens, time.Since(start).Milliseconds())
			if streamErr != nil && r.Context().Err() == nil {
				s.Logger.Printf("[protocol] live stream translation %s->%s failed: %v", call.TargetProtocol, apiType, streamErr)
			}
			return
		}

		// Some compatibility upstreams ignore stream=true and still return one JSON
		// response. Keep the one-shot fallback only for that non-SSE case.
		raw, readErr := io.ReadAll(io.LimitReader(call.Body, 32<<20))
		if readErr != nil {
			jsonErr(w, 502, "translation read failed: "+readErr.Error())
			return
		}
		translated, trErr := protocol.TranslateResponse(call.TargetProtocol, apiType, raw)
		if trErr != nil {
			jsonErr(w, 502, "protocol translation failed: "+trErr.Error())
			return
		}
		translated = normalizeResponseModelJSON(translated, m)
		in, out, total, cached := extractUsageDetailed(translated)
		s.recordUsageCostCached(r.Context(), m, false, in, out, total, cached, time.Since(start).Milliseconds())
		if call.RequestedStream {
			if dedupLeader {
				s.Generation.Complete(cacheSignature, generation.Entry{}, false)
				dedupFinished = true
			}
			sse, sseErr := protocol.SyntheticSSE(apiType, translated)
			if sseErr != nil {
				jsonErr(w, 502, "stream translation failed: "+sseErr.Error())
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(sse)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if cacheSignature != "" && len(candidates) > 0 && m.ID == candidates[0].ID {
			e := generation.Entry{Signature: cacheSignature, NamespaceHash: generation.Namespace(strings.TrimSpace(r.Header.Get("X-OmniBridge-Cache-Namespace"))), ProviderID: m.ProviderID, ModelID: m.ID, NativeModelID: m.NativeID, IdentityVersion: generation.IdentityVersion(m.ID, m.UpstreamModelID, m.DoctorCheckedAt), ContentType: "application/json; charset=utf-8", Body: translated}
			if s.Config.Get().ExactCache.Enabled {
				_ = s.Generation.Put(e)
			}
			if dedupLeader {
				s.Generation.Complete(cacheSignature, e, true)
				dedupFinished = true
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(translated)
		return
	}

	copyHeaders(w.Header(), call.Header)
	w.WriteHeader(call.Status)
	if strings.Contains(strings.ToLower(call.Header.Get("Content-Type")), "text/event-stream") {
		if dedupLeader {
			s.Generation.Complete(cacheSignature, generation.Entry{}, false)
			dedupFinished = true
		}
		flusher, _ := w.(http.Flusher)
		reader := bufio.NewReaderSize(call.Body, 64*1024)
		var inTok, outTok, totalTok, cachedTok int64
		for {
			line, er := reader.ReadBytes('\n')
			if len(line) > 0 {
				trim := strings.TrimSpace(string(line))
				if strings.HasPrefix(trim, "data:") {
					payload := strings.TrimSpace(strings.TrimPrefix(trim, "data:"))
					if payload != "" && payload != "[DONE]" {
						normalized := normalizeResponseModelJSON(protocol.NormalizeAgentJSON([]byte(payload)), m)
						if json.Valid(normalized) {
							line = []byte("data: " + string(normalized) + "\n")
							payload = string(normalized)
						}
						i, o, t, cached := extractUsageDetailed([]byte(payload))
						if i != 0 {
							inTok = i
						}
						if o != 0 {
							outTok = o
						}
						if t != 0 {
							totalTok = t
						}
						if cached != 0 {
							cachedTok = cached
						}
					}
				}
				_, _ = w.Write(line)
				if flusher != nil {
					flusher.Flush()
				}
			}
			if er != nil {
				break
			}
		}
		if totalTok == 0 {
			totalTok = inTok + outTok
		}
		s.recordUsageCostCached(r.Context(), m, false, inTok, outTok, totalTok, cachedTok, time.Since(start).Milliseconds())
		return
	}
	raw, _ := io.ReadAll(call.Body)
	raw = protocol.NormalizeAgentJSON(raw)
	raw = normalizeResponseModelJSON(raw, m)
	in, out, total, cached := extractUsageDetailed(raw)
	s.recordUsageCostCached(r.Context(), m, false, in, out, total, cached, time.Since(start).Milliseconds())
	if cacheSignature != "" && len(candidates) > 0 && m.ID == candidates[0].ID {
		ct := w.Header().Get("Content-Type")
		if ct == "" {
			ct = "application/json; charset=utf-8"
		}
		e := generation.Entry{Signature: cacheSignature, ProviderID: m.ProviderID, ModelID: m.ID, NativeModelID: m.NativeID, IdentityVersion: generation.IdentityVersion(m.ID, m.UpstreamModelID, m.DoctorCheckedAt), ContentType: ct, Body: raw}
		if s.Config.Get().ExactCache.Enabled {
			_ = s.Generation.Put(e)
		}
		if dedupLeader {
			s.Generation.Complete(cacheSignature, e, true)
			dedupFinished = true
		}
	}
	_, _ = w.Write(raw)
}

func (s *Server) embeddings(w http.ResponseWriter, r *http.Request) {
	release, ok := acquireSlot(r.Context(), s.generationSlots, 2*time.Second)
	if !ok {
		busy(w, "embeddings")
		return
	}
	defer release()
	start := time.Now()
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var meta struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &meta) != nil || strings.TrimSpace(meta.Model) == "" {
		jsonErr(w, http.StatusBadRequest, "JSON body must contain model")
		return
	}
	modelRef := meta.Model
	if target := strings.TrimSpace(s.Config.Get().Routing.Aliases[modelRef]); target != "" {
		modelRef = target
	}
	providerID := ""
	if i := strings.IndexByte(modelRef, '/'); i > 0 {
		providerID = modelRef[:i]
	}
	if providerID == "" {
		jsonErr(w, http.StatusBadRequest, "embedding model must be qualified as provider/model or configured as an alias")
		return
	}
	p, ok := s.providerByID(providerID)
	if !ok {
		jsonErr(w, http.StatusNotFound, "embedding provider not found or disabled")
		return
	}
	body = requestForModel(body, modelRef)
	up, err := s.Providers.Proxy(r.Context(), p, "embeddings", body)
	if err != nil {
		s.Stats.Record(providerID, meta.Model, true, 0, 0, 0, time.Since(start).Milliseconds())
		jsonErr(w, http.StatusBadGateway, "embedding upstream failed: "+err.Error())
		return
	}
	defer up.Body.Close()
	copyHeaders(w.Header(), up.Header)
	w.WriteHeader(up.StatusCode)
	raw, _ := io.ReadAll(up.Body)
	if up.StatusCode/100 == 2 {
		in, out, total := extractUsage(raw)
		s.Stats.Record(providerID, meta.Model, false, in, out, total, time.Since(start).Milliseconds())
	} else {
		s.Stats.Record(providerID, meta.Model, true, 0, 0, 0, time.Since(start).Milliseconds())
	}
	_, _ = w.Write(raw)
}

func extractUsage(b []byte) (int64, int64, int64) {
	in, out, total, _ := extractUsageDetailed(b)
	return in, out, total
}

func extractUsageDetailed(b []byte) (int64, int64, int64, int64) {
	var v map[string]any
	if json.Unmarshal(b, &v) != nil {
		return 0, 0, 0, 0
	}
	var u map[string]any
	if x, ok := v["usage"].(map[string]any); ok {
		u = x
	}
	if u == nil {
		if msg, ok := v["message"].(map[string]any); ok {
			if x, ok := msg["usage"].(map[string]any); ok {
				u = x
			}
		}
	}
	if u == nil {
		if response, ok := v["response"].(map[string]any); ok {
			if x, ok := response["usage"].(map[string]any); ok {
				u = x
			}
		}
	}
	if u == nil {
		return 0, 0, 0, 0
	}
	num := func(keys ...string) int64 {
		for _, k := range keys {
			if n, ok := u[k].(float64); ok {
				return int64(n)
			}
		}
		return 0
	}
	in := num("prompt_tokens", "input_tokens")
	out := num("completion_tokens", "output_tokens")
	total := num("total_tokens")
	cached := num("cached_tokens", "input_tokens_cached")
	for _, detailKey := range []string{"prompt_tokens_details", "input_tokens_details"} {
		if details, ok := u[detailKey].(map[string]any); ok {
			if n, ok := details["cached_tokens"].(float64); ok {
				cached = int64(n)
			}
		}
	}
	if total == 0 {
		total = in + out
	}
	return in, out, total, cached
}

func copyHeaders(dst, src http.Header) {
	for k, vals := range src {
		lk := strings.ToLower(k)
		if lk == "content-length" || lk == "connection" || lk == "transfer-encoding" {
			continue
		}
		for _, v := range vals {
			dst.Add(k, v)
		}
	}
}

func (s *Server) protocolEngineStatus(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, http.StatusOK, map[string]any{
		"ok":                                true,
		"engine":                            protocol.Status(),
		"buffered_cross_protocol_streaming": false,
		"live_cross_protocol_streaming":     true,
		"stream_transport":                  []string{"sse", "websocket"},
	})
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	cfg := s.Config.Get()
	mods := s.Registry.Models()
	agentReady := 0
	for _, m := range mods {
		if m.Status == "error" {
			continue
		}
		if modelAgentReady(m) {
			agentReady++
		}
	}
	snap := s.Stats.Snapshot()
	jsonOut(w, 200, map[string]any{
		"ok": true, "version": config.CurrentVersion, "listen": cfg.Listen,
		"lifecycle": s.lifecycle(),
		"providers": len(cfg.Providers), "accounts": len(cfg.Accounts), "models": len(mods),
		"ready_models": agentReady, "agent_ready_models": agentReady,
		"registry_updated_at": s.Registry.Snapshot().UpdatedAt,
		"started_at":          snap.StartedAt, "stats": snap.Total, "stats_24h": s.Stats.Last24h(),
		"provider_presets": len(config.ProviderCatalog()),
	})
}

type trayGigaState struct {
	ProviderID                 string        `json:"provider_id"`
	Name                       string        `json:"name"`
	Status                     string        `json:"status"`
	OAuthTokenCached           bool          `json:"oauth_token_cached"`
	OAuthTokenExpiresAt        time.Time     `json:"oauth_token_expires_at,omitempty"`
	OAuthTokenRemainingSeconds int64         `json:"oauth_token_remaining_seconds,omitempty"`
	Usage24h                   stats.Metrics `json:"usage_24h"`
	UsageSession               stats.Metrics `json:"usage_session"`
}

func (s *Server) trayStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.Config.Get()
	mods := s.Registry.Models()
	available, agentReady := 0, 0
	byProvider := map[string][]providers.Model{}
	for _, m := range mods {
		byProvider[m.ProviderID] = append(byProvider[m.ProviderID], m)
		if m.Status == "error" || s.modelBlocked(m) {
			continue
		}
		available++
		if modelAgentReady(m) {
			agentReady++
		}
	}
	apiEnabled, apiActive := 0, 0
	giga := []trayGigaState{}
	period24 := s.Stats.PeriodDetails("24h")
	periodSession := s.Stats.PeriodDetails("session")
	for _, p := range cfg.Providers {
		if !p.Enabled {
			continue
		}
		apiEnabled++
		healthy := false
		for _, m := range byProvider[p.ID] {
			if m.Status != "error" && !s.modelBlocked(m) {
				healthy = true
				break
			}
		}
		if healthy {
			apiActive++
		}
		if strings.EqualFold(p.Kind, "gigachat") {
			x := trayGigaState{ProviderID: p.ID, Name: p.Name, Status: "ready", Usage24h: period24.Providers[p.ID], UsageSession: periodSession.Providers[p.ID]}
			if !healthy {
				x.Status = "degraded"
			}
			if exp, ok := s.Providers.GigaTokenState(p.ID); ok {
				x.OAuthTokenCached = true
				x.OAuthTokenExpiresAt = exp
				remain := time.Until(exp).Seconds()
				if remain < 0 {
					remain = 0
				}
				x.OAuthTokenRemainingSeconds = int64(remain)
			}
			giga = append(giga, x)
		}
	}
	accountsEnabled, accountsConnected := 0, 0
	for _, a := range cfg.Accounts {
		if !a.Enabled {
			continue
		}
		accountsEnabled++
		if strings.EqualFold(a.Status, "connected") {
			accountsConnected++
		}
	}
	uptime := time.Since(s.startedAt)
	if uptime < 0 {
		uptime = 0
	}
	minutes := uptime.Minutes()
	if minutes < 1 {
		minutes = 1
	}
	tokensPerMinute := float64(periodSession.Total.TotalTokens) / minutes
	vsStatus := "not_ready"
	if available > 0 {
		vsStatus = "ready"
	}
	s.trayMu.Lock()
	refreshAt, refreshErr := s.trayRefreshAt, s.trayRefreshErr
	s.trayMu.Unlock()
	jsonOut(w, http.StatusOK, map[string]any{
		"ok":      true,
		"version": config.CurrentVersion,
		"server": map[string]any{
			"status": s.lifecycle(), "listen": cfg.Listen, "paused": s.paused.Load(),
			"started_at": s.startedAt, "uptime_seconds": int64(uptime.Seconds()),
		},
		"connections": map[string]any{
			"api_enabled": apiEnabled, "api_active": apiActive,
			"accounts_enabled": accountsEnabled, "accounts_connected": accountsConnected,
		},
		"models":                    map[string]any{"available": available, "agent_ready": agentReady, "total": len(mods)},
		"statistics_24h":            period24.Total,
		"statistics_session":        periodSession.Total,
		"tokens_per_minute_session": tokensPerMinute,
		"giga":                      giga,
		"refresh":                   map[string]any{"running": s.trayRefreshing.Load(), "last_at": refreshAt, "error": refreshErr},
		"vscode":                    map[string]any{"status": vsStatus, "agent_ready": agentReady},
	})
}

func (s *Server) pauseGateway(w http.ResponseWriter, r *http.Request) {
	s.paused.Store(true)
	s.Logger.Printf("[admin] gateway paused from Tray")
	jsonOut(w, http.StatusOK, map[string]any{"ok": true, "paused": true, "lifecycle": s.lifecycle()})
}

func (s *Server) resumeGateway(w http.ResponseWriter, r *http.Request) {
	s.paused.Store(false)
	s.Logger.Printf("[admin] gateway resumed from Tray")
	jsonOut(w, http.StatusOK, map[string]any{"ok": true, "paused": false, "lifecycle": s.lifecycle()})
}

func (s *Server) trayRefresh(w http.ResponseWriter, r *http.Request) {
	if !s.trayRefreshing.CompareAndSwap(false, true) {
		jsonOut(w, http.StatusAccepted, map[string]any{"ok": true, "running": true})
		return
	}
	s.trayMu.Lock()
	s.trayRefreshErr = ""
	s.trayMu.Unlock()
	go func() {
		defer s.trayRefreshing.Store(false)
		defer func() {
			if recovered := recover(); recovered != nil {
				s.trayMu.Lock()
				s.trayRefreshErr = fmt.Sprintf("refresh recovered from panic: %v", recovered)
				s.trayRefreshAt = time.Now()
				s.trayMu.Unlock()
				if s.Logger != nil {
					s.Logger.Printf("[recovery] tray refresh panic: %v\n%s", recovered, debug.Stack())
				}
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		s.refreshAccountStates(ctx)
		if ctx.Err() == nil {
			s.refreshRegistry(ctx)
			s.recordHealthSnapshot()
		}
		errText := ""
		if err := ctx.Err(); err != nil {
			errText = err.Error()
		}
		s.trayMu.Lock()
		s.trayRefreshAt = time.Now()
		s.trayRefreshErr = errText
		s.trayMu.Unlock()
	}()
	jsonOut(w, http.StatusAccepted, map[string]any{"ok": true, "running": true})
}

func (s *Server) refreshConnections(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	s.refreshAccountStates(ctx)
	snap := s.refreshRegistry(ctx)
	s.recordHealthSnapshot()
	jsonOut(w, http.StatusOK, map[string]any{"ok": true, "updated_at": snap.UpdatedAt, "models": len(snap.Models)})
}

func (s *Server) refreshAccountState(ctx context.Context, a config.AccountConnection) accounts.AccountInfo {
	if s.ControlFlight == nil {
		return s.refreshAccountStateDirect(ctx, a)
	}
	v, err, _ := s.ControlFlight.Do(ctx, "account-refresh:"+a.ID, func() (any, error) {
		opCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		return s.refreshAccountStateDirect(opCtx, a), nil
	})
	if err != nil {
		return accounts.AccountInfo{Status: "error", Error: err.Error(), CheckedAt: time.Now()}
	}
	if info, ok := v.(accounts.AccountInfo); ok {
		return info
	}
	return accounts.AccountInfo{Status: "error", Error: "account refresh returned no result", CheckedAt: time.Now()}
}

func (s *Server) refreshAccountStateDirect(ctx context.Context, a config.AccountConnection) accounts.AccountInfo {
	info := s.Accounts.AccountInfo(ctx, a)
	a.LastChecked = info.CheckedAt
	if a.LastChecked.IsZero() {
		a.LastChecked = time.Now()
	}
	if info.Status == "connected" {
		a.Status, a.Email, a.Plan, a.LastError, a.LastErrorKind = "connected", info.Email, info.Plan, "", ""
		a.LastConnectedAt = time.Now()
		a.RefreshFailures = 0
	} else {
		a.Status = info.Status
		a.LastError = info.Error
		a.LastErrorKind = info.Status
		a.RefreshFailures++
		if info.ReauthRequired {
			s.Accounts.CancelAccountSessions(a.ID)
		}
	}
	_ = s.Config.UpsertAccount(a)
	if s.Health != nil {
		s.Health.Record(health.Event{ID: a.ID, Source: "account", Status: info.Status, Error: info.Error})
	}
	return info
}

func (s *Server) refreshAccountStates(ctx context.Context) {
	for _, a := range s.Config.Get().Accounts {
		if !a.Enabled {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		actx, cancel := context.WithTimeout(ctx, 20*time.Second)
		s.refreshAccountState(actx, a)
		cancel()
	}
}

func (s *Server) recordHealthSnapshot() {
	if s.Health == nil {
		return
	}
	cfg := s.Config.Get()
	mods := s.Registry.Models()
	bySource := map[string][]providers.Model{}
	for _, m := range mods {
		bySource[m.ProviderID] = append(bySource[m.ProviderID], m)
	}
	for _, p := range cfg.Providers {
		if !p.Enabled {
			continue
		}
		list := bySource[p.ID]
		ready := 0
		errText := ""
		status := "unknown"
		var latency int64
		for _, m := range list {
			if m.Status != "error" {
				ready++
			}
			if errText == "" && m.Error != "" {
				errText = m.Error
			}
			if m.LatencyMS > 0 && (latency == 0 || m.LatencyMS < latency) {
				latency = m.LatencyMS
			}
		}
		switch {
		case ready > 0 && ready == len(list):
			status = "ready"
		case ready > 0:
			status = "degraded"
		case len(list) > 0:
			status = "error"
		}
		s.Health.Record(health.Event{ID: p.ID, Source: "api", Status: status, Models: len(list), Ready: ready, Latency: latency, Error: errText})
	}
}

// BackgroundRefresh is intentionally independent of the WPF UI. It keeps
// account sessions, model discovery and health telemetry fresh while the
// Gateway is running in Tray or headless mode.
func (s *Server) BackgroundRefresh(ctx context.Context) {
	s.refreshAccountStates(ctx)
	if ctx.Err() != nil {
		return
	}
	s.refreshRegistry(ctx)
	s.recordHealthSnapshot()
}

func (s *Server) connectionHistory(w http.ResponseWriter, r *http.Request) {
	if s.Health == nil {
		jsonOut(w, http.StatusOK, map[string]any{"events": []health.Event{}})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id != "" {
		jsonOut(w, http.StatusOK, map[string]any{"id": id, "events": s.Health.ForID(id, limit)})
		return
	}
	jsonOut(w, http.StatusOK, map[string]any{"events": s.Health.Recent(limit)})
}

func (s *Server) connectionHealth(w http.ResponseWriter, r *http.Request) {
	type item struct {
		ID              string    `json:"id"`
		Name            string    `json:"name"`
		Source          string    `json:"source"`
		Status          string    `json:"status"`
		Models          int       `json:"models"`
		ReadyModels     int       `json:"ready_models"`
		LastChecked     time.Time `json:"last_checked,omitempty"`
		Error           string    `json:"error,omitempty"`
		ResilienceState string    `json:"resilience_state,omitempty"`
	}
	cfg := s.Config.Get()
	mods := s.Registry.Models()
	bySource := map[string][]providers.Model{}
	for _, m := range mods {
		bySource[m.ProviderID] = append(bySource[m.ProviderID], m)
	}
	out := []item{}
	for _, p := range cfg.Providers {
		x := item{ID: p.ID, Name: p.Name, Source: "api", Status: "unknown"}
		if s.Resilience != nil {
			x.ResilienceState = string(s.Resilience.StateFor(resilience.ConnectionKey("api", p.ID)))
		}
		if !p.Enabled {
			x.Status = "disabled"
			out = append(out, x)
			continue
		}
		list := bySource[p.ID]
		x.Models = len(list)
		errors := 0
		for _, m := range list {
			if m.LastChecked.After(x.LastChecked) {
				x.LastChecked = m.LastChecked
			}
			if m.Status == "error" {
				errors++
				if x.Error == "" {
					x.Error = m.Error
				}
			} else {
				x.ReadyModels++
			}
		}
		switch {
		case len(list) == 0:
			x.Status = "unknown"
		case x.ReadyModels == 0 && errors > 0:
			x.Status = "error"
		case errors > 0:
			x.Status = "degraded"
		default:
			x.Status = "ready"
		}
		out = append(out, x)
	}
	for _, a := range cfg.Accounts {
		x := item{ID: a.ID, Name: a.Name, Source: "account", Status: a.Status, Error: a.LastError}
		if s.Resilience != nil {
			x.ResilienceState = string(s.Resilience.StateFor(resilience.ConnectionKey("account", a.ID)))
		}
		if !a.Enabled {
			x.Status = "disabled"
		}
		list := bySource[a.ID]
		x.Models = len(list)
		for _, m := range list {
			if m.LastChecked.After(x.LastChecked) {
				x.LastChecked = m.LastChecked
			}
			if m.Status != "error" {
				x.ReadyModels++
			}
		}
		if x.Status == "" {
			if x.ReadyModels > 0 {
				x.Status = "ready"
			} else {
				x.Status = "unknown"
			}
		}
		if x.Status == "connected" && x.ReadyModels > 0 {
			x.Status = "ready"
		}
		out = append(out, x)
	}
	jsonOut(w, http.StatusOK, map[string]any{"updated_at": s.Registry.Snapshot().UpdatedAt, "connections": out})
}

func (s *Server) listProviders(w http.ResponseWriter, r *http.Request) {
	c := s.Config.Get()
	out := make([]map[string]any, 0, len(c.Providers))
	for _, p := range c.Providers {
		meta := map[string]any{"executor": "openai-compatible", "support_level": "custom"}
		if preset, ok := config.ProviderPresetByID(p.Kind); ok {
			meta["executor"] = preset.Executor
			meta["support_level"] = preset.SupportLevel
		}
		out = append(out, map[string]any{"id": p.ID, "name": p.Name, "kind": p.Kind, "api_type": p.APIType, "preferred_protocol": p.PreferredProtocol, "protocols": p.Protocols, "protocol_base_urls": p.ProtocolBaseURLs, "base_url": p.BaseURL, "models_url": p.ModelsURL, "project_id": p.ProjectID, "auth_type": p.AuthType, "scope": p.Scope, "enabled": p.Enabled, "credential_ref": p.CredentialRef, "quirks": p.Quirks, "executor": meta["executor"], "support_level": meta["support_level"]})
	}
	jsonOut(w, 200, out)
}

type addProviderRequest struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Kind              string            `json:"kind"`
	APIType           string            `json:"api_type"`
	BaseURL           string            `json:"base_url"`
	ModelsURL         string            `json:"models_url"`
	ProjectID         string            `json:"project_id"`
	AuthType          string            `json:"auth_type"`
	APIKey            string            `json:"api_key"`
	Scope             string            `json:"scope"`
	Headers           map[string]string `json:"headers"`
	Protocols         []string          `json:"protocols"`
	PreferredProtocol string            `json:"preferred_protocol"`
	ProtocolBaseURLs  map[string]string `json:"protocol_base_urls"`
	ProtocolAuthTypes map[string]string `json:"protocol_auth_types"`
	AllowUnverified   bool              `json:"allow_unverified"`
}

func (s *Server) addProvider(w http.ResponseWriter, r *http.Request) {
	var in addProviderRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	kind := strings.ToLower(strings.TrimSpace(in.Kind))
	if kind == "" || kind == "auto" {
		kind = config.DetectKind(in.BaseURL, in.APIKey)
	}
	optionalKey := false
	preset, knownPreset := config.ProviderPresetByID(kind)
	if knownPreset {
		optionalKey = preset.APIKeyOptional || preset.AuthType == "none"
		if preset.SupportLevel == "reserved" {
			jsonErr(w, 400, "provider driver is reserved for a future protocol adapter")
			return
		}
		if preset.RequiresBaseURL && strings.TrimSpace(in.BaseURL) == "" && strings.TrimSpace(preset.BaseURL) == "" {
			jsonErr(w, 400, "base_url is required for this provider template")
			return
		}
	}
	if strings.TrimSpace(in.APIKey) == "" && !optionalKey {
		jsonErr(w, 400, "api_key is required for this provider")
		return
	}
	if kind == "yandex" {
		in.ProjectID = strings.TrimSpace(in.ProjectID)
		if in.ProjectID == "" {
			jsonErr(w, 400, "Yandex Folder ID is required")
			return
		}
		if !providers.ValidYandexFolderID(in.ProjectID) {
			jsonErr(w, 400, "Yandex Folder ID has invalid format")
			return
		}
	}
	// Custom and compatibility-template providers are probed on the exact user-supplied
	// origin before persistence. This keeps the expanded catalog data-driven without
	// pretending that account/deployment-specific endpoints are universal constants.
	shouldProbe := kind == "custom" || kind == "openai-compatible" || (knownPreset && (preset.SupportLevel == "template" || preset.SupportLevel == "local-template"))
	if shouldProbe && strings.TrimSpace(in.BaseURL) != "" {
		d, derr := s.Providers.DetectOpenAICompatible(r.Context(), in.BaseURL, in.APIKey)
		if derr != nil {
			if !in.AllowUnverified {
				jsonErr(w, 400, "provider endpoint probe failed: "+derr.Error())
				return
			}
		} else {
			in.BaseURL = d.BaseURL
			in.ModelsURL = d.ModelsURL
			in.AuthType = d.AuthType
			if len(in.Protocols) == 0 {
				in.Protocols = append([]string(nil), d.Protocols...)
			}
			if in.PreferredProtocol == "" {
				in.PreferredProtocol = d.PreferredProtocol
			}
			if in.APIType == "" {
				in.APIType = d.APIType
			}
		}
	}
	p := config.NormalizeProvider(config.Provider{ID: in.ID, Name: in.Name, Kind: kind, APIType: in.APIType, BaseURL: in.BaseURL, ModelsURL: in.ModelsURL, ProjectID: in.ProjectID, AuthType: in.AuthType, Scope: in.Scope, Enabled: true, Headers: in.Headers, Protocols: in.Protocols, PreferredProtocol: in.PreferredProtocol, ProtocolBaseURLs: in.ProtocolBaseURLs, ProtocolAuthTypes: in.ProtocolAuthTypes})
	if p.Name == "" {
		p.Name = p.Kind
	}
	if p.ID == "" {
		p.ID = p.Name
	}
	p = config.NormalizeProvider(p)
	if strings.TrimSpace(in.APIKey) != "" {
		p.CredentialRef = "provider:" + strings.ToLower(strings.ReplaceAll(p.ID, " ", "-"))
	}
	if p.CredentialRef != "" {
		if err := s.Secrets.Put(p.CredentialRef, in.APIKey); err != nil {
			jsonErr(w, 500, err.Error())
			return
		}
	}
	if err := s.Config.UpsertProvider(p); err != nil {
		if p.CredentialRef != "" {
			_ = s.Secrets.Delete(p.CredentialRef)
		}
		jsonErr(w, 400, err.Error())
		return
	}
	// Re-read sanitized provider.
	var saved config.Provider
	for _, x := range s.Config.Get().Providers {
		if x.Name == p.Name {
			saved = x
		}
	}
	if saved.ID == "" {
		saved = p
	}
	if p.CredentialRef != "" && saved.CredentialRef != p.CredentialRef {
		val, _ := s.Secrets.Get(p.CredentialRef)
		saved.CredentialRef = "provider:" + saved.ID
		_ = s.Secrets.Put(saved.CredentialRef, val)
		_ = s.Secrets.Delete(p.CredentialRef)
		_ = s.Config.UpsertProvider(saved)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	mods, err := s.Providers.ListModels(ctx, saved)
	cancel()
	if err != nil && !in.AllowUnverified {
		_ = s.Config.DeleteProvider(saved.ID)
		if saved.CredentialRef != "" {
			_ = s.Secrets.Delete(saved.CredentialRef)
		}
		jsonErr(w, 400, "provider validation failed: "+err.Error())
		return
	}
	s.Registry.ResetProviderVerification(saved.ID)
	snap := s.refreshRegistry(context.Background())
	jsonOut(w, 201, map[string]any{"provider": saved, "models_discovered": len(mods), "registry_models": len(snap.Models), "warning": func() string {
		if err != nil {
			return err.Error()
		}
		return ""
	}()})
}

func (s *Server) deleteProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := s.providerByID(id)
	if !ok {
		jsonErr(w, 404, "provider not found")
		return
	}
	if err := s.Config.DeleteProvider(id); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	if p.CredentialRef != "" {
		_ = s.Secrets.Delete(p.CredentialRef)
	}
	s.refreshRegistry(context.Background())
	jsonOut(w, 200, map[string]any{"ok": true})
}
func (s *Server) refreshProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.providerByID(id); !ok {
		jsonErr(w, 404, "provider not found")
		return
	}
	snap := s.refreshRegistry(r.Context())
	jsonOut(w, 200, snap)
}
func (s *Server) bridgeModels(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, 200, s.Registry.Snapshot())
}
func (s *Server) bridgeStats(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, 200, s.Stats.Snapshot())
}

func (s *Server) bridgeStatistics(w http.ResponseWriter, r *http.Request) {
	period := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("period")))
	if period == "" {
		period = "24h"
	}
	valid := map[string]bool{"session": true, "24h": true, "7d": true, "30d": true, "all": true, "all_time": true}
	if !valid[period] {
		jsonErr(w, 400, "period must be session, 24h, 7d, 30d or all")
		return
	}
	jsonOut(w, 200, s.Stats.PeriodDetails(period))
}

func (s *Server) getRouter(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, 200, s.Config.Get().Routing)
}

func (s *Server) putRouter(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	var in config.RoutingConfig
	if err := json.Unmarshal(raw, &in); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	// Old 0.3.x/0.4.3 clients know nothing about the affinity fields. Preserve
	// current values unless the caller explicitly supplied them.
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	current := s.Config.Get().Routing
	if _, ok := fields["session_stickiness"]; !ok {
		in.SessionStickiness = current.SessionStickiness
	}
	if _, ok := fields["session_ttl_seconds"]; !ok {
		in.SessionTTLSeconds = current.SessionTTLSeconds
	}
	if err := s.Config.SetRouting(in); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	s.refreshResilienceConfig()
	jsonOut(w, 200, s.Config.Get().Routing)
}

func (s *Server) connectionPools() []map[string]any {
	cfg := s.Config.Get()
	models := s.Registry.Models()
	modelCount := map[string]int{}
	for _, m := range models {
		modelCount[m.ProviderID]++
	}
	type member struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Source string `json:"source"`
		State  string `json:"state"`
		Models int    `json:"models"`
	}
	pools := map[string][]member{}
	for _, p := range cfg.Providers {
		if !p.Enabled {
			continue
		}
		key := "api:" + strings.ToLower(strings.TrimSpace(p.Kind))
		state := resilience.Closed
		if s.Resilience != nil {
			state = s.Resilience.StateFor(resilience.ConnectionKey("api", p.ID))
		}
		pools[key] = append(pools[key], member{ID: p.ID, Name: p.Name, Source: "api", State: string(state), Models: modelCount[p.ID]})
	}
	for _, a := range cfg.Accounts {
		if !a.Enabled {
			continue
		}
		key := "account:" + strings.ToLower(strings.TrimSpace(a.Kind))
		state := resilience.Closed
		if s.Resilience != nil {
			state = s.Resilience.StateFor(resilience.ConnectionKey("account", a.ID))
		}
		pools[key] = append(pools[key], member{ID: a.ID, Name: a.Name, Source: "account", State: string(state), Models: modelCount[a.ID]})
	}
	keys := make([]string, 0, len(pools))
	for k := range pools {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		members := pools[k]
		healthy, degraded, open := 0, 0, 0
		for _, m := range members {
			switch resilience.State(m.State) {
			case resilience.Open:
				open++
			case resilience.Degraded, resilience.HalfOpen:
				degraded++
			default:
				healthy++
			}
		}
		out = append(out, map[string]any{"pool": k, "members": members, "member_count": len(members), "healthy": healthy, "degraded": degraded, "open": open})
	}
	return out
}

func (s *Server) routerHealth(w http.ResponseWriter, r *http.Request) {
	s.refreshResilienceConfig()
	breakers := []resilience.Snapshot{}
	if s.Resilience != nil {
		breakers = s.Resilience.Snapshots()
	}
	routerState := map[string]any{"version": "3.1-doctor-aware"}
	if s.Router != nil {
		routerState = map[string]any{"runtime": s.Router.Snapshot(), "virtual_models": autoVirtualModels}
	}
	jsonOut(w, http.StatusOK, map[string]any{
		"policies":         s.Config.Get().Routing.Policies,
		"circuits":         breakers,
		"connection_pools": s.connectionPools(),
		"router":           routerState,
		"persistent":       s.Storage != nil,
		"now":              time.Now().UTC(),
	})
}

func (s *Server) routerDiagnostics(w http.ResponseWriter, r *http.Request) {
	requested := strings.TrimSpace(r.URL.Query().Get("model"))
	if requested == "" {
		requested = "auto/smart"
	}
	candidates, err := s.routingCandidatesFor(requested, "")
	if err != nil {
		jsonErr(w, http.StatusNotFound, err.Error())
		return
	}
	ids := make([]string, 0, len(candidates))
	for _, m := range candidates {
		ids = append(ids, m.ID)
	}
	state := map[string]any{"version": "3.1-doctor-aware"}
	if s.Router != nil {
		state["runtime"] = s.Router.Snapshot()
	}
	state["model"] = requested
	state["ordered_candidates"] = ids
	if strings.HasPrefix(strings.ToLower(requested), "auto/") {
		state["scores"] = s.routingScores(requested)
	}
	state["virtual_models"] = autoVirtualModels
	state["session_stickiness"] = s.Config.Get().Routing.SessionStickiness
	state["session_ttl_seconds"] = s.Config.Get().Routing.SessionTTLSeconds
	jsonOut(w, http.StatusOK, state)
}

func (s *Server) resilienceStatus(w http.ResponseWriter, r *http.Request) {
	s.refreshResilienceConfig()
	breakers := []resilience.Snapshot{}
	if s.Resilience != nil {
		breakers = s.Resilience.Snapshots()
	}
	routingCfg := s.Config.Get().Routing
	jsonOut(w, http.StatusOK, map[string]any{
		"version":               "3.0",
		"persistent":            s.Storage != nil,
		"states":                []string{string(resilience.Closed), string(resilience.Degraded), string(resilience.Open), string(resilience.HalfOpen)},
		"failure_threshold":     routingCfg.CircuitBreakerThreshold,
		"base_cooldown_seconds": routingCfg.CircuitBreakerCooldownSeconds,
		"max_cooldown_seconds":  1800,
		"half_open_probes":      1,
		"breakers":              breakers,
		"connection_pools":      s.connectionPools(),
		"singleflight_in_flight": func() int {
			if s.ControlFlight == nil {
				return 0
			}
			return s.ControlFlight.InFlight()
		}(),
	})
}

func (s *Server) resetResilience(w http.ResponseWriter, r *http.Request) {
	if s.Resilience == nil {
		jsonErr(w, http.StatusServiceUnavailable, "resilience manager is not available")
		return
	}
	var in struct {
		Key string `json:"key"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in)
	}
	if q := strings.TrimSpace(r.URL.Query().Get("key")); q != "" {
		in.Key = q
	}
	if err := s.Resilience.Reset(strings.TrimSpace(in.Key)); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOut(w, http.StatusOK, map[string]any{"ok": true, "reset": func() string {
		if strings.TrimSpace(in.Key) == "" {
			return "all"
		}
		return strings.TrimSpace(in.Key)
	}()})
}

func (s *Server) bridgeVSCode(w http.ResponseWriter, r *http.Request) {
	cfg := s.Config.Get()
	agent, chat, failed := 0, 0, 0
	for _, m := range s.Registry.Models() {
		if m.Status == "error" {
			failed++
			continue
		}
		chat++
		if modelAgentReady(m) {
			agent++
		}
	}
	jsonOut(w, 200, map[string]any{"endpoint": "http://" + cfg.Listen + "/v1", "agent_ready": agent, "chat_ready": chat, "excluded": failed, "managed_section": "omnibridge", "safe_merge": true, "export_contract": "2.1", "auto_models": true, "picker_visibility": "vscode-managed", "first_sync_visibility_action": "enable eye in Chat: Manage Language Models"})
}

func (s *Server) providerEngineStatus(w http.ResponseWriter, r *http.Request) {
	catalog := config.ProviderCatalog()
	byExecutor := map[string]int{}
	bySupport := map[string]int{}
	for _, p := range catalog {
		byExecutor[p.Executor]++
		bySupport[p.SupportLevel]++
	}
	configured := s.Config.Get().Providers
	resolved := make([]map[string]any, 0, len(configured))
	for _, p := range configured {
		entry := map[string]any{"id": p.ID, "kind": p.Kind, "enabled": p.Enabled}
		if d, ok := config.ProviderPresetByID(p.Kind); ok {
			entry["executor"] = d.Executor
			entry["support_level"] = d.SupportLevel
		} else {
			entry["executor"] = "openai-compatible"
			entry["support_level"] = "custom"
		}
		resolved = append(resolved, entry)
	}
	jsonOut(w, 200, map[string]any{"definitions": len(catalog), "by_executor": byExecutor, "by_support_level": bySupport, "configured": resolved, "data_driven": true, "gigachat_tls": s.Providers.GigaCATrustStatus()})
}

func (s *Server) catalog(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, 200, config.ProviderCatalog())
}

func serverSanitizeID(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	var b strings.Builder
	dash := false
	for _, r := range v {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func (s *Server) accountCatalog(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, 200, accounts.Catalog())
}

func (s *Server) listAccounts(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, 200, s.Config.Get().Accounts)
}

func (s *Server) addAccount(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID       string            `json:"id"`
		Name     string            `json:"name"`
		Kind     string            `json:"kind"`
		Settings map[string]string `json:"settings"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	kind := strings.ToLower(strings.TrimSpace(in.Kind))
	found := false
	available := false
	var note string
	for _, x := range accounts.Catalog() {
		if x.ID == kind {
			found = true
			available = x.Status == "supported"
			note = x.Note
			break
		}
	}
	if !found {
		jsonErr(w, 400, "unknown account connector")
		return
	}
	if !available {
		jsonErr(w, 400, note)
		return
	}
	a := config.AccountConnection{ID: in.ID, Name: in.Name, Kind: kind, Enabled: true, Settings: in.Settings, Status: "disconnected"}
	if a.Name == "" {
		if kind == "chatgpt-codex" {
			a.Name = "ChatGPT account"
		} else {
			a.Name = "Google Gemini account"
		}
	}
	if a.ID == "" {
		a.ID = a.Name
	}
	if err := s.Config.UpsertAccount(a); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	// Retrieve the exact upserted connector. Name+kind is ambiguous when a user
	// intentionally keeps multiple accounts of the same connector type.
	wantedID := serverSanitizeID(a.ID)
	for _, x := range s.Config.Get().Accounts {
		if x.ID == wantedID {
			a = x
			break
		}
	}
	jsonOut(w, 201, a)
}

func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a, ok := s.accountByID(id)
	if !ok {
		jsonErr(w, 404, "account connector not found")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.Accounts.Logout(ctx, a)
	if err := s.Config.DeleteAccount(id); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	s.refreshRegistry(context.Background())
	jsonOut(w, 200, map[string]any{"ok": true})
}

func (s *Server) accountPreflight(w http.ResponseWriter, r *http.Request) {
	a, ok := s.accountByID(r.PathValue("id"))
	if !ok {
		jsonErr(w, http.StatusNotFound, "account connector not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	out := s.Accounts.Preflight(ctx, a)
	// Preflight is a diagnostic resource: an unmet prerequisite is represented
	// in the JSON body so the Desktop can show the precise actionable error.
	jsonOut(w, http.StatusOK, out)
}

func (s *Server) startAccountLogin(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a, ok := s.accountByID(id)
	if !ok {
		jsonErr(w, 404, "account connector not found")
		return
	}
	var in struct {
		Device bool `json:"device"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in)
	var out accounts.LoginStart
	var err error
	switch a.Kind {
	case "chatgpt-codex":
		out, err = s.Accounts.StartCodexLogin(context.Background(), a, in.Device)
	case "gemini-developer-oauth":
		out, err = s.Accounts.StartGeminiLogin(context.Background(), a)
	default:
		err = accounts.ErrUnsupportedAccount
	}
	if err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	jsonOut(w, 200, out)
}

func (s *Server) accountLoginStatus(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("attempt")
	st, ok := s.Accounts.LoginStatus(id)
	if !ok {
		jsonErr(w, 404, "login attempt not found")
		return
	}
	if st.AccountID != "" {
		if a, exists := s.accountByID(st.AccountID); exists {
			if st.Success && st.Status == "completed" && a.Status != "connected" {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				info := s.Accounts.AccountInfo(ctx, a)
				cancel()
				if info.Status == "connected" {
					a.Status, a.Email, a.Plan, a.LastError, a.LastErrorKind = "connected", info.Email, info.Plan, "", ""
					a.LastChecked, a.LastConnectedAt, a.RefreshFailures = time.Now(), time.Now(), 0
					_ = s.Config.UpsertAccount(a)
					st.Email, st.Plan = info.Email, info.Plan
				} else if info.Error != "" {
					a.Status, a.LastError, a.LastErrorKind = info.Status, info.Error, info.Status
					a.LastChecked, a.RefreshFailures = time.Now(), a.RefreshFailures+1
					_ = s.Config.UpsertAccount(a)
				}
				go s.refreshRegistry(context.Background())
			} else if st.Status == "failed" && a.Status != "error" {
				a.Status, a.LastError, a.LastErrorKind = "error", st.Error, "login_failed"
				a.LastChecked, a.RefreshFailures = time.Now(), a.RefreshFailures+1
				_ = s.Config.UpsertAccount(a)
			}
		}
	}
	jsonOut(w, 200, st)
}

func (s *Server) accountInfo(w http.ResponseWriter, r *http.Request) {
	a, ok := s.accountByID(r.PathValue("id"))
	if !ok {
		jsonErr(w, 404, "account connector not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	jsonOut(w, 200, s.refreshAccountState(ctx, a))
}

func (s *Server) accountUsage(w http.ResponseWriter, r *http.Request) {
	a, ok := s.accountByID(r.PathValue("id"))
	if !ok {
		jsonErr(w, 404, "account connector not found")
		return
	}
	if a.Kind != "chatgpt-codex" {
		jsonOut(w, 200, accounts.UsageInfo{ID: a.ID, Kind: a.Kind, Status: "unsupported", Error: "official account usage endpoint is not available for this connector"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	jsonOut(w, 200, s.Accounts.CodexUsage(ctx, a))
}

func (s *Server) accountLogout(w http.ResponseWriter, r *http.Request) {
	a, ok := s.accountByID(r.PathValue("id"))
	if !ok {
		jsonErr(w, 404, "account connector not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := s.Accounts.Logout(ctx, a); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	a.Status = "disconnected"
	a.Email = ""
	a.Plan = ""
	_ = s.Config.UpsertAccount(a)
	go s.refreshRegistry(context.Background())
	jsonOut(w, 200, map[string]any{"ok": true})
}

func (s *Server) refreshAccount(w http.ResponseWriter, r *http.Request) {
	a, ok := s.accountByID(r.PathValue("id"))
	if !ok {
		jsonErr(w, 404, "account connector not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	info := s.refreshAccountState(ctx, a)
	if ctx.Err() == nil {
		s.refreshRegistry(ctx)
		s.recordHealthSnapshot()
	}
	cancel()
	jsonOut(w, 200, map[string]any{"account": info, "registry_updated_at": s.Registry.Snapshot().UpdatedAt})
}

func (s *Server) localScan(w http.ResponseWriter, r *http.Request) {
	type found struct {
		Kind    string `json:"kind"`
		Name    string `json:"name"`
		BaseURL string `json:"base_url"`
		Models  int    `json:"models"`
	}
	out := []found{}
	for _, kind := range []string{"ollama", "lmstudio", "vllm", "localai"} {
		preset, _ := config.ProviderPresetByID(kind)
		p := config.NormalizeProvider(config.Provider{ID: kind, Name: preset.Name, Kind: kind, BaseURL: preset.BaseURL, Enabled: true})
		ctx, cancel := context.WithTimeout(r.Context(), 1200*time.Millisecond)
		mods, err := s.Providers.ListModels(ctx, p)
		cancel()
		// A listening OpenAI-compatible port with an empty /models response is
		// not a usable local model. Report only runtimes that expose at least
		// one generation model, so the UI never claims a phantom detection.
		if err == nil && len(mods) > 0 {
			out = append(out, found{Kind: kind, Name: p.Name, BaseURL: p.BaseURL, Models: len(mods)})
		}
	}
	jsonOut(w, 200, out)
}

func (s *Server) bridgeBalance(w http.ResponseWriter, r *http.Request) {
	results := []providers.BalanceResult{}
	for _, p := range s.Config.Get().Providers {
		if !p.Enabled {
			continue
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		results = append(results, s.Providers.Balance(ctx, p))
		cancel()
	}
	jsonOut(w, 200, results)
}

func (s *Server) probeModel(w http.ResponseWriter, r *http.Request) {
	release, ok := acquireSlot(r.Context(), s.probeSlots, time.Second)
	if !ok {
		busy(w, "model_probe")
		return
	}
	defer release()
	pid := r.PathValue("provider")
	native := r.PathValue("model")
	id := pid + "/" + native
	m, err := s.Registry.Find(id)
	if err != nil {
		jsonErr(w, 404, err.Error())
		return
	}
	deep := r.URL.Query().Get("deep") != "0"
	var res providers.ProbeResult
	if m.Source == "account" {
		a, ok := s.accountByID(m.ProviderID)
		if !ok {
			jsonErr(w, 404, "account connector not found")
			return
		}
		res = s.Accounts.Probe(r.Context(), a, m, deep)
	} else {
		p, ok := s.providerByID(m.ProviderID)
		if !ok {
			jsonErr(w, 404, "provider not found")
			return
		}
		res = s.Providers.Probe(r.Context(), p, m, deep)
	}
	providers.ApplyDoctorScores(&res)
	s.Registry.UpdateProbe(id, res)
	jsonOut(w, 200, res)
}

func (s *Server) probeByBody(w http.ResponseWriter, r *http.Request) {
	release, ok := acquireSlot(r.Context(), s.probeSlots, time.Second)
	if !ok {
		busy(w, "model_probe")
		return
	}
	defer release()
	var in struct {
		ModelID string `json:"model_id"`
		Deep    bool   `json:"deep"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	m, err := s.Registry.Find(in.ModelID)
	if err != nil {
		jsonErr(w, 404, err.Error())
		return
	}
	var res providers.ProbeResult
	if m.Source == "account" {
		a, ok := s.accountByID(m.ProviderID)
		if !ok {
			jsonErr(w, 404, "account connector not found")
			return
		}
		res = s.Accounts.Probe(r.Context(), a, m, in.Deep)
	} else {
		p, ok := s.providerByID(m.ProviderID)
		if !ok {
			jsonErr(w, 404, "provider not found")
			return
		}
		res = s.Providers.Probe(r.Context(), p, m, in.Deep)
	}
	providers.ApplyDoctorScores(&res)
	s.Registry.UpdateProbe(in.ModelID, res)
	jsonOut(w, 200, res)
}

func (s *Server) detect(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BaseURL string `json:"base_url"`
		APIKey  string `json:"api_key"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	kind := config.DetectKind(in.BaseURL, in.APIKey)
	p := config.NormalizeProvider(config.Provider{Name: "Detected", ID: "detected", Kind: kind, BaseURL: in.BaseURL, Enabled: true})
	if kind == "custom" || kind == "openai-compatible" {
		if strings.TrimSpace(in.BaseURL) == "" {
			jsonErr(w, 400, "cannot infer an unknown service from API key alone; provide Base URL or choose a known provider")
			return
		}
		d, err := s.Providers.DetectOpenAICompatible(r.Context(), in.BaseURL, in.APIKey)
		if err != nil {
			jsonErr(w, 400, err.Error())
			return
		}
		p.BaseURL, p.ModelsURL, p.AuthType, p.APIType = d.BaseURL, d.ModelsURL, d.AuthType, d.APIType
		jsonOut(w, 200, map[string]any{"kind": kind, "api_type": d.APIType, "preferred_protocol": d.PreferredProtocol, "protocols": d.Protocols, "base_url": d.BaseURL, "models_url": d.ModelsURL, "auth_type": d.AuthType, "shape": d.Shape, "status": d.Status, "probe_model": d.ProbeModel, "note": "Credentials and compatibility probes were restricted to the supplied origin."})
		return
	}
	jsonOut(w, 200, map[string]any{"kind": kind, "api_type": p.APIType, "base_url": p.BaseURL, "models_url": p.ModelsURL, "auth_type": p.AuthType, "note": "Known provider preset selected; provider validation runs when you add it."})
}

func (s *Server) shutdown(w http.ResponseWriter, r *http.Request) {
	if s.shutting.Swap(true) {
		jsonOut(w, 200, map[string]any{"ok": true})
		return
	}
	jsonOut(w, 200, map[string]any{"ok": true})
	go func() {
		time.Sleep(100 * time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Stop(ctx)
	}()
}

func logCategory(path string) string {
	switch {
	case strings.HasPrefix(path, "/v1/"):
		return "gateway"
	case strings.Contains(path, "/accounts"):
		return "accounts"
	case strings.Contains(path, "/router"):
		return "router"
	case strings.Contains(path, "/probe"), strings.Contains(path, "/models/"):
		return "doctor"
	case strings.Contains(path, "/vscode"), strings.Contains(path, "/local-scan"):
		return "integration"
	case strings.Contains(path, "/connections"):
		return "health"
	case strings.HasPrefix(path, "/bridge/"):
		return "admin"
	default:
		return "core"
	}
}

func logging(next http.Handler, l *log.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if r.URL.Path != "/health" {
			l.Printf("[%s] %s %s %s", logCategory(r.URL.Path), r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
		}
	})
}
func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func jsonErr(w http.ResponseWriter, status int, msg string) {
	jsonOut(w, status, map[string]any{"error": map[string]any{"message": msg, "type": "bridge_error"}})
}

var _ = errors.Is
