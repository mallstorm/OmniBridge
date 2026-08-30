package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"omnibridge/internal/config"
	"omnibridge/internal/contextguard"
	"omnibridge/internal/mcpcontrol"
	"omnibridge/internal/observability"
	"omnibridge/internal/providers"
	"omnibridge/internal/quotacost"
)

func (s *Server) quotaCostStatus(w http.ResponseWriter, r *http.Request) {
	if s.QuotaCost == nil {
		jsonErr(w, 503, "quota & cost engine is not attached")
		return
	}
	s.QuotaCost.UpdateConfig(s.Config.Get().QuotaCost)
	jsonOut(w, 200, s.QuotaCost.Summary())
}
func (s *Server) putQuotaCostConfig(w http.ResponseWriter, r *http.Request) {
	var c config.QuotaCostConfig
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&c); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if err := s.Config.SetQuotaCost(c); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if s.QuotaCost != nil {
		s.QuotaCost.UpdateConfig(c)
	}
	jsonOut(w, 200, map[string]any{"version": quotacost.Version, "config": s.Config.Get().QuotaCost})
}
func (s *Server) refreshQuotaCost(w http.ResponseWriter, r *http.Request) {
	if s.QuotaCost == nil {
		jsonErr(w, 503, "quota & cost engine is not attached")
		return
	}
	results := []any{}
	for _, a := range s.Config.Get().Accounts {
		if !a.Enabled || a.Kind != "chatgpt-codex" {
			continue
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		info := s.Accounts.CodexUsage(ctx, a)
		cancel()
		if info.Status == "ok" {
			s.QuotaCost.ObserveAccountUsage(a.ID, info.Details)
		}
		results = append(results, info)
	}
	for _, p := range s.Config.Get().Providers {
		if !p.Enabled {
			continue
		}
		if p.Kind == "gigachat" {
			ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
			b := s.Providers.Balance(ctx, p)
			cancel()
			results = append(results, b)
		}
	}
	jsonOut(w, 200, map[string]any{"ok": true, "results": results, "summary": s.QuotaCost.Summary()})
}
func (s *Server) setQuotaSnapshot(w http.ResponseWriter, r *http.Request) {
	if s.QuotaCost == nil {
		jsonErr(w, 503, "quota & cost engine is not attached")
		return
	}
	var q quotacost.Snapshot
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&q); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if err := s.QuotaCost.SetQuota(q); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	got, _ := s.QuotaCost.Quota(q.ConnectionID)
	jsonOut(w, 200, got)
}
func (s *Server) setPricing(w http.ResponseWriter, r *http.Request) {
	if s.QuotaCost == nil {
		jsonErr(w, 503, "quota & cost engine is not attached")
		return
	}
	var p quotacost.Price
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&p); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if err := s.QuotaCost.UpsertPrice(p); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	got, _ := s.QuotaCost.Price(p.Provider, p.Model)
	jsonOut(w, 200, got)
}

func (s *Server) contextGuardStatus(w http.ResponseWriter, r *http.Request) {
	stats := map[string]int64{"events": 0, "saved_tokens": 0, "before_tokens": 0, "after_tokens": 0}
	if s.Storage != nil {
		if got, err := s.Storage.ContextStats(); err == nil {
			stats = got
		}
	}
	jsonOut(w, 200, map[string]any{"version": contextguard.Version, "config": s.Config.Get().ContextGuard, "stats": stats})
}
func (s *Server) putContextGuard(w http.ResponseWriter, r *http.Request) {
	var c config.ContextGuardConfig
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&c); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if err := s.Config.SetContextGuard(c); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	jsonOut(w, 200, map[string]any{"version": contextguard.Version, "config": s.Config.Get().ContextGuard})
}
func (s *Server) contextGuardPreview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		APIType       string          `json:"api_type"`
		ContextWindow int             `json:"context_window"`
		Request       json.RawMessage `json:"request"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(&in); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if len(in.Request) == 0 {
		jsonErr(w, 400, "request is required")
		return
	}
	if in.APIType == "" {
		in.APIType = "chat-completions"
	}
	out, rep, err := contextguard.Apply(in.Request, in.APIType, in.ContextWindow, s.Config.Get().ContextGuard)
	if err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	var preview any
	_ = json.Unmarshal(out, &preview)
	jsonOut(w, 200, map[string]any{"report": rep, "request": preview})
}

func (s *Server) integrationHubStatus(w http.ResponseWriter, r *http.Request) {
	if s.Integrations == nil {
		jsonErr(w, 503, "integration hub is not attached")
		return
	}
	jsonOut(w, 200, map[string]any{"version": "1.0", "integrations": s.Integrations.Detect(s.Config.Get().Listen)})
}
func (s *Server) integrationPlan(w http.ResponseWriter, r *http.Request) {
	if s.Integrations == nil {
		jsonErr(w, 503, "integration hub is not attached")
		return
	}
	p, ok := s.Integrations.Plan(r.PathValue("id"), s.Config.Get().Listen)
	if !ok {
		jsonErr(w, 404, "integration not found")
		return
	}
	jsonOut(w, 200, p)
}
func (s *Server) integrationConfigured(w http.ResponseWriter, r *http.Request) {
	if s.Integrations == nil {
		jsonErr(w, 503, "integration hub is not attached")
		return
	}
	var in struct {
		Profile string `json:"profile"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in)
	st, err := s.Integrations.MarkConfigured(r.PathValue("id"), in.Profile, s.Config.Get().Listen)
	if err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	jsonOut(w, 200, st)
}

func (s *Server) mcpStatus(w http.ResponseWriter, r *http.Request) {
	if s.MCP == nil {
		jsonErr(w, 503, "mcp control plane is not attached")
		return
	}
	jsonOut(w, 200, s.MCP.Snapshot())
}
func (s *Server) mcpUpsert(w http.ResponseWriter, r *http.Request) {
	if s.MCP == nil {
		jsonErr(w, 503, "mcp control plane is not attached")
		return
	}
	var in mcpcontrol.Server
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	out, err := s.MCP.Upsert(in)
	if err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	jsonOut(w, 200, out)
}
func (s *Server) mcpDelete(w http.ResponseWriter, r *http.Request) {
	if s.MCP == nil {
		jsonErr(w, 503, "mcp control plane is not attached")
		return
	}
	if err := s.MCP.Delete(r.PathValue("id")); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	jsonOut(w, 200, map[string]any{"ok": true})
}
func (s *Server) mcpProbe(w http.ResponseWriter, r *http.Request) {
	if s.MCP == nil {
		jsonErr(w, 503, "mcp control plane is not attached")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	out, err := s.MCP.Probe(ctx, r.PathValue("id"))
	if err != nil {
		jsonErr(w, 404, err.Error())
		return
	}
	jsonOut(w, 200, out)
}
func (s *Server) mcpProbeAll(w http.ResponseWriter, r *http.Request) {
	if s.MCP == nil {
		jsonErr(w, 503, "mcp control plane is not attached")
		return
	}
	out := []mcpcontrol.Server{}
	for _, x := range s.MCP.List() {
		if !x.Enabled {
			continue
		}
		ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
		st, _ := s.MCP.Probe(ctx, x.ID)
		cancel()
		out = append(out, st)
	}
	jsonOut(w, 200, map[string]any{"ok": true, "servers": out})
}
func (s *Server) mcpDataServers(w http.ResponseWriter, r *http.Request) {
	if s.MCP == nil {
		jsonOut(w, 200, map[string]any{"object": "list", "data": []any{}})
		return
	}
	data := []map[string]any{}
	for _, x := range s.MCP.List() {
		if !x.Enabled {
			continue
		}
		data = append(data, map[string]any{"id": x.ID, "name": x.Name, "transport": x.Transport, "url": x.URL, "status": x.Status})
	}
	jsonOut(w, 200, map[string]any{"object": "list", "data": data})
}

func (s *Server) observabilityStatus(w http.ResponseWriter, r *http.Request) {
	if s.Observability == nil {
		jsonErr(w, 503, "observability is not attached")
		return
	}
	jsonOut(w, 200, map[string]any{"snapshot": s.Observability.Snapshot(), "recent": s.Observability.Recent(25), "persistent": s.Storage != nil})
}
func (s *Server) observabilityTraces(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	if s.Storage != nil {
		rows, err := s.Storage.RecentTraces(limit)
		if err == nil {
			jsonOut(w, 200, rows)
			return
		}
	}
	if s.Observability == nil {
		jsonOut(w, 200, []any{})
		return
	}
	jsonOut(w, 200, s.Observability.Recent(limit))
}
func (s *Server) remoteStatus(w http.ResponseWriter, r *http.Request) {
	c := s.Config.Get()
	jsonOut(w, 200, map[string]any{"config": c.Remote, "listen": c.Listen, "effective_remote": isNonLoopbackListen(c.Listen), "restart_required_after_change": true})
}
func (s *Server) putRemote(w http.ResponseWriter, r *http.Request) {
	var c config.RemoteConfig
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&c); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if c.Enabled {
		if strings.TrimSpace(c.TLSCertPath) == "" || strings.TrimSpace(c.TLSKeyPath) == "" {
			jsonErr(w, 400, "remote mode requires TLS certificate and key paths")
			return
		}
		if len(c.AllowedCIDRs) == 0 {
			jsonErr(w, 400, "remote mode requires at least one allowed CIDR")
			return
		}
	}
	if err := s.Config.SetRemote(c); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	jsonOut(w, 200, map[string]any{"config": s.Config.Get().Remote, "restart_required": true})
}

func (s *Server) metricsHandler(w http.ResponseWriter, r *http.Request) {
	if s.Observability == nil {
		http.Error(w, "observability unavailable", 503)
		return
	}
	c := s.Config.Get()
	if !c.Remote.MetricsPublic && !authOK(r, c.AdminAPIKey) {
		jsonErr(w, 401, "invalid admin key")
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = io.WriteString(w, s.Observability.Prometheus())
}

type observedWriter struct {
	http.ResponseWriter
	status int
}

func (w *observedWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}
func (w *observedWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	return w.ResponseWriter.Write(p)
}
func (w *observedWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
func (w *observedWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijacking unsupported")
	}
	if w.status == 0 {
		w.status = http.StatusSwitchingProtocols
	}
	return h.Hijack()
}
func (w *observedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (s *Server) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Observability == nil {
			next.ServeHTTP(w, r)
			return
		}
		ctx, tr := s.Observability.Begin(r)
		ow := &observedWriter{ResponseWriter: w}
		ow.Header().Set("X-OmniBridge-Trace-ID", tr.ID)
		next.ServeHTTP(ow, r.WithContext(ctx))
		status := ow.status
		if status == 0 {
			status = 200
		}
		s.Observability.Finish(tr, status)
	})
}

func remoteIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(strings.Trim(host, "[]"))
}
func ipAllowed(ip net.IP, cidrs []string) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	for _, raw := range cidrs {
		_, n, err := net.ParseCIDR(strings.TrimSpace(raw))
		if err == nil && n.Contains(ip) {
			return true
		}
	}
	return false
}
func (s *Server) remoteGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := s.Config.Get()
		ip := remoteIP(r)
		if ip != nil && !ip.IsLoopback() {
			if !c.Remote.Enabled || !ipAllowed(ip, c.Remote.AllowedCIDRs) {
				jsonErr(w, http.StatusForbidden, "remote client is not allowed by OmniBridge Remote policy")
				return
			}
		}
		if s.dispatchTeamAdmin(w, r) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isNonLoopbackListen(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return true
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return true
	}
	return !ip.IsLoopback()
}
func (s *Server) validateRemoteBind(c config.Config) error {
	if !isNonLoopbackListen(c.Listen) {
		return nil
	}
	if !c.Remote.Enabled {
		return errors.New("non-loopback listen requires remote.enabled=true")
	}
	if strings.TrimSpace(c.Remote.TLSCertPath) == "" || strings.TrimSpace(c.Remote.TLSKeyPath) == "" {
		return errors.New("non-loopback remote mode requires TLS certificate and key")
	}
	if len(c.Remote.AllowedCIDRs) == 0 {
		return errors.New("non-loopback remote mode requires allowed_cidrs")
	}
	if _, err := os.Stat(c.Remote.TLSCertPath); err != nil {
		return fmt.Errorf("remote TLS certificate: %w", err)
	}
	if _, err := os.Stat(c.Remote.TLSKeyPath); err != nil {
		return fmt.Errorf("remote TLS key: %w", err)
	}
	return nil
}

func (s *Server) contextWindowForRequest(model string) int {
	best := 0
	if isAutoVirtualModel(model) {
		for _, m := range s.Registry.Models() {
			if m.ContextWindow > best && m.Status != "error" {
				best = m.ContextWindow
			}
		}
		return best
	}
	mods, err := s.resolveRoutingID(model)
	if err != nil {
		return 0
	}
	for _, m := range mods {
		if m.ContextWindow > best {
			best = m.ContextWindow
		}
	}
	return best
}
func (s *Server) applyContextGuard(raw []byte, apiType, model string) ([]byte, contextguard.Report) {
	cfg := s.Config.Get().ContextGuard
	out, rep, err := contextguard.Apply(raw, apiType, s.contextWindowForRequest(model), cfg)
	if err != nil {
		rep.Action = "error"
		rep.Reason = err.Error()
		return raw, rep
	}
	if s.Storage != nil && (rep.Action == "compacted" || rep.Action == "would_compact") {
		_ = s.Storage.AppendContextEvent("ctx:"+strconv.FormatInt(time.Now().UnixNano(), 10), time.Now().UTC(), model, rep.BeforeTokens, rep.AfterTokens, rep.SavedTokens, rep.Action)
	}
	return out, rep
}

func annotateTrace(ctx context.Context, fn func(*observability.Trace)) {
	if tr := observability.FromContext(ctx); tr != nil {
		fn(tr)
	}
}

func (s *Server) recordUsageCost(ctx context.Context, m providers.Model, failed bool, in, out, total, latency int64) quotacost.Estimate {
	return s.recordUsageCostCached(ctx, m, failed, in, out, total, 0, latency)
}

func (s *Server) recordUsageCostCached(ctx context.Context, m providers.Model, failed bool, in, out, total, cached, latency int64) quotacost.Estimate {
	s.Stats.RecordCached(m.ProviderID, m.ID, failed, in, cached, out, total, latency)
	est := quotacost.Estimate{Known: false, Confidence: "unknown"}
	if !failed && s.QuotaCost != nil {
		eventID := "cost:" + strconv.FormatInt(time.Now().UnixNano(), 10)
		if tr := observability.FromContext(ctx); tr != nil {
			eventID = "cost:" + tr.ID
		}
		est = s.QuotaCost.RecordCost(eventID, m.ProviderID, m.NativeID, m.ProviderID, in, out, cached)
		annotateTrace(ctx, func(tr *observability.Trace) { tr.CostKnown = est.Known; tr.CostUSD = est.CostUSD })
	}
	return est
}
