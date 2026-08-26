from pathlib import Path

path = Path("internal/server/server.go")
text = path.read_text(encoding="utf-8")

old = '''\tmux.HandleFunc("POST /bridge/router/evaluate", s.withAdmin(s.routerEvaluate))\n\tmux.HandleFunc("GET /bridge/resilience", s.withAdmin(s.resilienceStatus))'''
new = '''\tmux.HandleFunc("POST /bridge/router/evaluate", s.withAdmin(s.routerEvaluate))\n\tmux.HandleFunc("GET /bridge/orchestration", s.withAdmin(s.orchestrationStatus))\n\tmux.HandleFunc("GET /bridge/orchestration/workflows", s.withAdmin(s.orchestrationWorkflows))\n\tmux.HandleFunc("POST /bridge/orchestration/evaluate", s.withAdmin(s.orchestrationEvaluate))\n\tmux.HandleFunc("GET /bridge/resilience", s.withAdmin(s.resilienceStatus))'''
if old not in text:
    raise SystemExit("handler anchor not found")
text = text.replace(old, new, 1)

old = '''\tmods := s.Registry.Models()\n\tdata := make([]map[string]any, 0, len(mods)+len(autoVirtualModels))\n\tfor _, v := range autoVirtualModels {\n\t\tdata = append(data, map[string]any{"id": v, "object": "model", "owned_by": "omnibridge-router", "type": "virtual"})\n\t}\n\tfor _, m := range mods {'''
new = '''\tmods := s.Registry.Models()\n\tworkflowModels := s.orchestrationModelEntries()\n\tdata := make([]map[string]any, 0, len(mods)+len(autoVirtualModels)+len(workflowModels))\n\tfor _, v := range autoVirtualModels {\n\t\tdata = append(data, map[string]any{"id": v, "object": "model", "owned_by": "omnibridge-router", "type": "virtual"})\n\t}\n\tdata = append(data, workflowModels...)\n\tfor _, m := range mods {'''
if old not in text:
    raise SystemExit("models anchor not found")
text = text.replace(old, new, 1)

old = '''\tbody, contextReport := s.applyContextGuard(body, apiType, meta.Model)\n\tannotateTrace(r.Context(), func(tr *observability.Trace) { tr.ContextAction = contextReport.Action })\n\tsessionKey := extractRoutingSession(r, body)'''
new = '''\tbody, contextReport := s.applyContextGuard(body, apiType, meta.Model)\n\tannotateTrace(r.Context(), func(tr *observability.Trace) { tr.ContextAction = contextReport.Action })\n\tif s.handleWorkflowProxy(w, r, apiType, body, meta.Model) {\n\t\treturn\n\t}\n\tsessionKey := extractRoutingSession(r, body)'''
if old not in text:
    raise SystemExit("proxy anchor not found")
text = text.replace(old, new, 1)

path.write_text(text, encoding="utf-8")
