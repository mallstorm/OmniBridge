package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"omnibridge/internal/observability"
	"omnibridge/internal/orchestration"
)

// StartOrchestrated preserves the existing Server lifecycle and transport
// policy while using a thin routing wrapper for workflow virtual models.
func (s *Server) StartOrchestrated() error {
	s.shutting.Store(false)
	c := s.Config.Get()
	if err := s.validateRemoteBind(c); err != nil {
		return err
	}
	s.httpServer = &http.Server{
		Addr:              c.Listen,
		Handler:           s.OrchestrationHandler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	s.Logger.Printf("OmniBridge %s listening on %s (orchestration enabled)", currentVersion(), c.Listen)
	if isNonLoopbackListen(c.Listen) && c.Remote.Enabled {
		s.Logger.Printf("[remote] TLS remote mode enabled; allowed CIDRs=%d", len(c.Remote.AllowedCIDRs))
		return s.httpServer.ListenAndServeTLS(c.Remote.TLSCertPath, c.Remote.TLSKeyPath)
	}
	return s.httpServer.ListenAndServe()
}

// currentVersion is kept as a tiny indirection so this additive file does not
// duplicate version constants or create a dependency outside package server.
func currentVersion() string {
	return configCurrentVersion()
}

// configCurrentVersion is declared in orchestration_version.go to keep this
// file focused on HTTP concerns.

// OrchestrationHandler delegates every ordinary request to the unchanged base
// Handler. Only explicit workflow models and orchestration control endpoints
// are intercepted.
func (s *Server) OrchestrationHandler() http.Handler {
	base := s.Handler()

	specialMux := http.NewServeMux()
	specialMux.HandleFunc("GET /v1/models", s.withData(s.modelsWithOrchestration))
	specialMux.HandleFunc("POST /v1/chat/completions", s.withData(func(w http.ResponseWriter, r *http.Request) {
		s.orchestrationProxyHTTP(w, r, "chat-completions")
	}))
	specialMux.HandleFunc("POST /v1/responses", s.withData(func(w http.ResponseWriter, r *http.Request) {
		s.orchestrationProxyHTTP(w, r, "responses")
	}))
	specialMux.HandleFunc("POST /v1/messages", s.withData(func(w http.ResponseWriter, r *http.Request) {
		s.orchestrationProxyHTTP(w, r, "messages")
	}))
	specialMux.HandleFunc("GET /bridge/orchestration", s.withAdmin(s.orchestrationStatus))
	specialMux.HandleFunc("GET /bridge/orchestration/workflows", s.withAdmin(s.orchestrationWorkflows))
	specialMux.HandleFunc("POST /bridge/orchestration/evaluate", s.withAdmin(s.orchestrationEvaluate))

	var special http.Handler = specialMux
	special = s.recoverPanics(special)
	special = s.remoteGuard(special)
	special = s.observe(special)
	special = logging(special, s.Logger)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isOrchestrationControlPath(r.Method, r.URL.Path) || (r.Method == http.MethodGet && r.URL.Path == "/v1/models") {
			special.ServeHTTP(w, r)
			return
		}
		if isGenerationPath(r.Method, r.URL.Path) {
			workflow, body, err := peekWorkflowRequest(r, s.Config.Get().ContextGuard.MaxRequestBytes)
			if err != nil {
				// Let the established handler produce the canonical request-size/JSON error.
				base.ServeHTTP(w, r)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			if workflow {
				special.ServeHTTP(w, r)
				return
			}
		}
		base.ServeHTTP(w, r)
	})
}

func isGenerationPath(method, path string) bool {
	if method != http.MethodPost {
		return false
	}
	switch path {
	case "/v1/chat/completions", "/v1/responses", "/v1/messages":
		return true
	default:
		return false
	}
}

func isOrchestrationControlPath(method, path string) bool {
	if method == http.MethodGet && (path == "/bridge/orchestration" || path == "/bridge/orchestration/workflows") {
		return true
	}
	return method == http.MethodPost && path == "/bridge/orchestration/evaluate"
}

func peekWorkflowRequest(r *http.Request, configuredMax int) (bool, []byte, error) {
	maxBody := int64(32 << 20)
	if configuredMax > 0 {
		maxBody = int64(configuredMax)
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		return false, nil, err
	}
	var meta struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		return false, body, err
	}
	return orchestration.IsWorkflowModel(meta.Model), body, nil
}

func (s *Server) modelsWithOrchestration(w http.ResponseWriter, r *http.Request) {
	mods := s.Registry.Models()
	workflowModels := s.orchestrationModelEntries()
	data := make([]map[string]any, 0, len(mods)+len(autoVirtualModels)+len(workflowModels))
	for _, v := range autoVirtualModels {
		data = append(data, map[string]any{"id": v, "object": "model", "owned_by": "omnibridge-router", "type": "virtual"})
	}
	data = append(data, workflowModels...)
	for _, m := range mods {
		data = append(data, map[string]any{"id": m.ID, "object": "model", "owned_by": m.ProviderID, "type": m.Type})
	}
	jsonOut(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (s *Server) orchestrationProxyHTTP(w http.ResponseWriter, r *http.Request, apiType string) {
	release, ok := acquireSlot(r.Context(), s.generationSlots, 2*time.Second)
	if !ok {
		busy(w, "orchestration")
		return
	}
	defer release()

	maxBody := int64(32 << 20)
	if n := s.Config.Get().ContextGuard.MaxRequestBytes; n > 0 {
		maxBody = int64(n)
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if int64(len(body)) > maxBody {
		jsonErr(w, http.StatusRequestEntityTooLarge, "request exceeds Context Guard max_request_bytes")
		return
	}
	var meta struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &meta); err != nil || strings.TrimSpace(meta.Model) == "" {
		jsonErr(w, http.StatusBadRequest, "JSON body must contain model")
		return
	}
	annotateTrace(r.Context(), func(tr *observability.Trace) {
		tr.Model = meta.Model
		tr.SelectedModel = meta.Model
	})

	if s.QuotaCost != nil {
		s.QuotaCost.UpdateConfig(s.Config.Get().QuotaCost)
		if reject, reason := s.QuotaCost.ShouldRejectForBudget(); reject {
			annotateTrace(r.Context(), func(tr *observability.Trace) { tr.ErrorKind = reason })
			jsonOut(w, http.StatusPaymentRequired, map[string]any{
				"error":  map[string]any{"message": "OmniBridge budget limit reached", "type": "budget_exceeded", "code": reason},
				"budget": s.QuotaCost.BudgetStatus(),
			})
			return
		}
	}

	if !s.handleWorkflowProxy(w, r, apiType, body, meta.Model) {
		jsonOut(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"message": "request is not a workflow model", "type": "workflow_request_invalid", "code": "workflow_request_invalid"}})
	}
}

// Keep the compile-time dependency on log explicit; StartOrchestrated mirrors
// Server.Start's logging contract without changing existing code.
var _ *log.Logger
