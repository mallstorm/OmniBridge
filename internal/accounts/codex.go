package accounts

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"omnibridge/internal/config"
	"omnibridge/internal/protocol"
	"omnibridge/internal/providers"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type codexSession struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner
	nextID  int
}

type codexRuntime struct {
	bin       string
	home      string
	workspace string
	env       []string
}

func (m *Manager) prepareCodexRuntime(a config.AccountConnection) (codexRuntime, error) {
	bin, err := m.codexCommand(a)
	if err != nil {
		return codexRuntime{}, err
	}
	home := filepath.Join(m.accountDir(a), "codex-home")
	workspace := filepath.Join(m.accountDir(a), "workspace")
	if err := os.MkdirAll(home, 0700); err != nil {
		return codexRuntime{}, err
	}
	if err := os.MkdirAll(workspace, 0700); err != nil {
		return codexRuntime{}, fmt.Errorf("create isolated Codex workspace: %w", err)
	}
	codexConfig := "cli_auth_credentials_store = \"file\"\nforced_login_method = \"chatgpt\"\ncheck_for_update_on_startup = false\n\n[shell_environment_policy]\ninherit = \"core\"\nignore_default_excludes = false\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(codexConfig), 0600); err != nil {
		return codexRuntime{}, fmt.Errorf("write isolated Codex config: %w", err)
	}
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		upper := strings.ToUpper(kv)
		if strings.HasPrefix(upper, "CODEX_HOME=") || strings.HasPrefix(upper, "OPENAI_API_KEY=") || strings.HasPrefix(upper, "CODEX_API_KEY=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "CODEX_HOME="+home)
	return codexRuntime{bin: bin, home: home, workspace: workspace, env: env}, nil
}

func codexExecCommand(ctx context.Context, bin string, args ...string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		ext := strings.ToLower(filepath.Ext(bin))
		if ext == ".cmd" || ext == ".bat" {
			parts := []string{"/D", "/S", "/C", fmt.Sprintf(`"%s"`, bin)}
			parts = append(parts, args...)
			return exec.CommandContext(ctx, "cmd.exe", parts...)
		}
	}
	return exec.CommandContext(ctx, bin, args...)
}

func (m *Manager) codexCommand(a config.AccountConnection) (string, error) {
	// `command` is the canonical key. `codex_path` is accepted for migration
	// from the alpha27/alpha31 desktop controller, which used that name.
	for _, key := range []string{"command", "codex_path", "path"} {
		if v := strings.TrimSpace(a.Settings[key]); v != "" {
			if _, err := os.Stat(v); err == nil {
				return v, nil
			}
			if p, err := exec.LookPath(v); err == nil {
				return p, nil
			}
			return "", fmt.Errorf("Codex executable not found: %s", v)
		}
	}
	for _, name := range []string{"codex", "codex.exe", "codex.cmd"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", errors.New("Codex CLI not found; install the official Codex CLI or choose codex.exe/codex.cmd in Advanced settings")
}

func (m *Manager) newCodexSession(ctx context.Context, a config.AccountConnection) (*codexSession, error) {
	rt, err := m.prepareCodexRuntime(a)
	if err != nil {
		return nil, err
	}
	cmd := codexExecCommand(ctx, rt.bin, "app-server")
	cmd.Env = rt.env
	cmd.Dir = rt.workspace
	applyHiddenProcess(cmd)
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	s := &codexSession{cmd: cmd, stdin: stdin, scanner: sc, nextID: 1}
	if _, err := s.call(map[string]any{"method": "initialize", "params": map[string]any{"clientInfo": map[string]any{"name": "omnibridge", "title": "OmniBridge", "version": config.CurrentVersion}, "capabilities": map[string]any{"experimentalApi": true}}}); err != nil {
		_ = cmd.Process.Kill()
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("codex initialize failed: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return nil, err
	}
	if err := s.send(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
		_ = cmd.Process.Kill()
		return nil, err
	}
	return s, nil
}

func (s *codexSession) close() {
	if s.stdin != nil {
		_ = s.stdin.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_, _ = s.cmd.Process.Wait()
	}
}
func (s *codexSession) send(v map[string]any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = s.stdin.Write(b)
	return err
}
func (s *codexSession) call(req map[string]any) (map[string]any, error) {
	id := s.nextID
	s.nextID++
	req["id"] = id
	if err := s.send(req); err != nil {
		return nil, err
	}
	for s.scanner.Scan() {
		var msg map[string]any
		if json.Unmarshal(s.scanner.Bytes(), &msg) != nil {
			continue
		}
		if mid, ok := msg["id"].(float64); ok && int(mid) == id {
			if e, ok := msg["error"].(map[string]any); ok {
				return nil, fmt.Errorf("codex rpc: %v", e["message"])
			}
			if r, ok := msg["result"].(map[string]any); ok {
				return r, nil
			}
			return map[string]any{}, nil
		}
	}
	if err := s.scanner.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

func (m *Manager) StartCodexLogin(ctx context.Context, a config.AccountConnection, device bool) (LoginStart, error) {
	if device {
		return m.startCodexAppServerLogin(a, true)
	}
	backend := strings.ToLower(strings.TrimSpace(a.Settings["browser_backend"]))
	if backend == "app-server" || backend == "appserver" {
		return m.startCodexAppServerLogin(a, false)
	}
	return m.startCodexCLILogin(a)
}

func (m *Manager) startCodexAppServerLogin(a config.AccountConnection, device bool) (LoginStart, error) {
	loginCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	s, err := m.newCodexSession(loginCtx, a)
	if err != nil {
		cancel()
		return LoginStart{}, err
	}
	typ := "chatgpt"
	if device {
		typ = "chatgptDeviceCode"
	}
	result, err := s.call(map[string]any{"method": "account/login/start", "params": map[string]any{"type": typ}})
	if err != nil {
		s.close()
		cancel()
		return LoginStart{}, err
	}
	attempt := stringValue(result["loginId"])
	if attempt == "" {
		attempt = fmt.Sprintf("codex-%d", time.Now().UnixNano())
	}
	backend := "codex-app-server"
	st := LoginStatus{AccountID: a.ID, AttemptID: attempt, Status: "pending", Backend: backend}
	m.mu.Lock()
	m.pending[attempt] = &pendingLogin{accountID: a.ID, status: st}
	m.mu.Unlock()
	go func() { defer cancel(); m.watchCodexLogin(a, attempt, backend, s) }()
	out := LoginStart{AccountID: a.ID, AttemptID: attempt, Status: "pending", Backend: backend}
	if device {
		out.VerificationURL = stringValue(result["verificationUrl"])
		out.UserCode = stringValue(result["userCode"])
		if out.VerificationURL == "" || out.UserCode == "" {
			s.close()
			cancel()
			return LoginStart{}, errors.New("Codex device login did not return verification URL and user code")
		}
	} else {
		out.AuthURL = stringValue(result["authUrl"])
		if out.AuthURL == "" {
			s.close()
			cancel()
			return LoginStart{}, errors.New("Codex app-server browser login did not return authUrl")
		}
	}
	return out, nil
}

func (m *Manager) startCodexCLILogin(a config.AccountConnection) (LoginStart, error) {
	rt, err := m.prepareCodexRuntime(a)
	if err != nil {
		return LoginStart{}, err
	}
	loginCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	cmd := codexExecCommand(loginCtx, rt.bin, "login")
	cmd.Env = rt.env
	cmd.Dir = rt.workspace
	applyHiddenProcess(cmd)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		cancel()
		return LoginStart{}, fmt.Errorf("start official Codex browser login: %w", err)
	}
	attempt := fmt.Sprintf("codex-cli-%d", time.Now().UnixNano())
	backend := "codex-cli"
	m.mu.Lock()
	m.pending[attempt] = &pendingLogin{accountID: a.ID, status: LoginStatus{AccountID: a.ID, AttemptID: attempt, Status: "pending", Backend: backend}}
	m.mu.Unlock()
	go func() {
		defer cancel()
		err := cmd.Wait()
		status := LoginStatus{AccountID: a.ID, AttemptID: attempt, Status: "failed", Backend: backend}
		if err == nil {
			verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 20*time.Second)
			info, verifyErr := m.codexAccountInfo(verifyCtx, a)
			verifyCancel()
			if verifyErr == nil && info.Status == "connected" {
				status.Status, status.Success, status.Email, status.Plan = "completed", true, info.Email, info.Plan
			} else if verifyErr != nil {
				status.Error = "Codex login finished but account verification failed: " + verifyErr.Error()
			} else {
				status.Error = "Codex login finished but no ChatGPT account session was found"
			}
		} else {
			msg := strings.TrimSpace(output.String())
			if len(msg) > 1200 {
				msg = msg[len(msg)-1200:]
			}
			if msg == "" {
				msg = err.Error()
			}
			status.Error = "Codex browser login failed: " + msg
		}
		m.mu.Lock()
		if p := m.pending[attempt]; p != nil {
			p.status = status
		}
		m.mu.Unlock()
	}()
	return LoginStart{AccountID: a.ID, AttemptID: attempt, Status: "pending", Backend: backend, BrowserManaged: true}, nil
}

func (m *Manager) watchCodexLogin(a config.AccountConnection, attempt, backend string, s *codexSession) {
	defer s.close()
	status := LoginStatus{AccountID: a.ID, AttemptID: attempt, Status: "failed", Backend: backend}
	for s.scanner.Scan() {
		var msg map[string]any
		if json.Unmarshal(s.scanner.Bytes(), &msg) != nil {
			continue
		}
		if stringValue(msg["method"]) != "account/login/completed" {
			continue
		}
		p, _ := msg["params"].(map[string]any)
		success, _ := p["success"].(bool)
		status.Success = success
		if success {
			status.Status = "completed"
		} else {
			status.Error = stringValue(p["error"])
		}
		break
	}
	m.mu.Lock()
	if p := m.pending[attempt]; p != nil {
		p.status = status
	}
	m.mu.Unlock()
}

func (m *Manager) LoginStatus(id string) (LoginStatus, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.pending[id]
	if !ok {
		return LoginStatus{}, false
	}
	return p.status, true
}

func (m *Manager) codexAccountInfo(ctx context.Context, a config.AccountConnection) (AccountInfo, error) {
	s, err := m.newCodexSession(ctx, a)
	if err != nil {
		return AccountInfo{}, err
	}
	defer s.close()
	read := func(refresh bool) (map[string]any, error) {
		return s.call(map[string]any{"method": "account/read", "params": map[string]any{"refreshToken": refresh}})
	}
	r, err := read(false)
	if err != nil {
		return AccountInfo{}, err
	}
	acct, _ := r["account"].(map[string]any)
	if acct == nil {
		// Official Codex app-server owns its refresh material. Ask it once to
		// refresh before declaring the user logged out. This avoids unnecessary
		// browser login prompts for long-lived ChatGPT sessions.
		if refreshed, refreshErr := read(true); refreshErr == nil {
			r = refreshed
			acct, _ = r["account"].(map[string]any)
		} else {
			return AccountInfo{}, refreshErr
		}
	}
	out := AccountInfo{ID: a.ID, Kind: a.Kind, Status: "disconnected", Details: r}
	if acct != nil {
		out.Status = "connected"
		out.Email = stringValue(acct["email"])
		out.Plan = stringValue(acct["planType"])
	}
	return out, nil
}

func (m *Manager) CodexUsage(ctx context.Context, a config.AccountConnection) UsageInfo {
	s, err := m.newCodexSession(ctx, a)
	if err != nil {
		return UsageInfo{ID: a.ID, Kind: a.Kind, Status: "error", Error: err.Error()}
	}
	defer s.close()
	details := map[string]any{}
	if r, err := s.call(map[string]any{"method": "account/rateLimits/read"}); err == nil {
		details["rate_limits"] = r
	}
	if r, err := s.call(map[string]any{"method": "account/usage/read"}); err == nil {
		details["usage"] = r
	}
	return UsageInfo{ID: a.ID, Kind: a.Kind, Status: "ok", Details: details}
}

func (m *Manager) listCodexModels(ctx context.Context, a config.AccountConnection) ([]providers.Model, error) {
	info, err := m.codexAccountInfo(ctx, a)
	if err != nil {
		return nil, err
	}
	if info.Status != "connected" {
		return nil, errors.New("ChatGPT account is not connected")
	}
	s, err := m.newCodexSession(ctx, a)
	if err != nil {
		return nil, err
	}
	defer s.close()
	r, err := s.call(map[string]any{"method": "model/list", "params": map[string]any{"limit": 100, "includeHidden": false}})
	if err != nil {
		return nil, err
	}
	rows, _ := r["data"].([]any)
	out := make([]providers.Model, 0, len(rows))
	for _, row := range rows {
		x, _ := row.(map[string]any)
		native := stringValue(x["model"])
		if native == "" {
			native = stringValue(x["id"])
		}
		if native == "" {
			continue
		}
		vision := false
		if mods, ok := x["inputModalities"].([]any); ok {
			for _, mod := range mods {
				if strings.EqualFold(stringValue(mod), "image") {
					vision = true
				}
			}
		}
		out = append(out, providers.Model{ID: a.ID + "/" + native, NativeID: native, ProviderID: a.ID, ProviderName: a.Name, Name: firstNonEmpty(stringValue(x["displayName"]), native), APIType: "chat-completions", Protocols: []string{"chat-completions"}, PreferredProtocol: "chat-completions", Type: "chat", ToolCalling: true, ParallelTools: false, Streaming: true, Vision: vision, Reasoning: true, Status: "discovered", Source: "account"})
	}
	return out, nil
}

type codexPending struct {
	accountID       string
	model           string
	session         *codexSession
	cancel          context.CancelFunc
	rpcID           any
	callID          string
	aliasToOriginal map[string]string
	started         time.Time
}

type codexChatRequest struct {
	Model      string           `json:"model"`
	Messages   []map[string]any `json:"messages"`
	Stream     bool             `json:"stream"`
	Tools      []any            `json:"tools"`
	ToolChoice any              `json:"tool_choice"`
}

type codexTurnEvent struct {
	Kind       string
	Text       string
	RPCID      any
	CallID     string
	Tool       string
	Arguments  string
	TurnStatus string
	Error      string
}

const codexPendingTTL = 15 * time.Minute

func validCodexToolRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
}

func safeCodexToolName(name string, used map[string]bool) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(name) {
		if validCodexToolRune(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
		if b.Len() >= 112 {
			break
		}
	}
	base := strings.Trim(b.String(), "_-")
	if base == "" {
		base = "tool"
	}
	candidate := base
	for i := 2; used[candidate]; i++ {
		candidate = fmt.Sprintf("%s_%d", base, i)
	}
	used[candidate] = true
	return candidate
}

func codexDynamicTools(tools []any) ([]any, map[string]string) {
	out := make([]any, 0, len(tools))
	aliases := map[string]string{}
	used := map[string]bool{}
	for _, raw := range tools {
		t, _ := raw.(map[string]any)
		if stringValue(t["type"]) != "function" {
			continue
		}
		fn, _ := t["function"].(map[string]any)
		orig := stringValue(fn["name"])
		if orig == "" {
			continue
		}
		alias := safeCodexToolName(orig, used)
		aliases[alias] = orig
		schema, ok := fn["parameters"].(map[string]any)
		if !ok || schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		spec := map[string]any{
			"name":        alias,
			"description": firstNonEmpty(stringValue(fn["description"]), "Tool supplied by the OmniBridge VS Code client."),
			"inputSchema": schema,
		}
		out = append(out, spec)
	}
	return out, aliases
}

func codexPrompt(messages []map[string]any) string {
	var prompt strings.Builder
	for _, msg := range messages {
		role := strings.ToLower(stringValue(msg["role"]))
		if role == "tool" {
			continue
		}
		text := contentText(msg["content"])
		if strings.TrimSpace(text) == "" {
			continue
		}
		prompt.WriteString(strings.ToUpper(role))
		prompt.WriteString(": ")
		prompt.WriteString(text)
		prompt.WriteString("\n")
	}
	return prompt.String()
}

func codexToolChoiceInstruction(choice any) string {
	switch x := choice.(type) {
	case string:
		if strings.EqualFold(x, "required") {
			return "\nYou must call one of the tools provided for this turn before answering."
		}
	case map[string]any:
		if fn, ok := x["function"].(map[string]any); ok {
			if name := stringValue(fn["name"]); name != "" {
				return "\nYou must call the tool named " + name + " before answering."
			}
		}
	}
	return ""
}

func lastToolResult(messages []map[string]any) (string, string, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.ToLower(stringValue(messages[i]["role"])) != "tool" {
			continue
		}
		id := stringValue(messages[i]["tool_call_id"])
		if id == "" {
			continue
		}
		return id, contentText(messages[i]["content"]), true
	}
	return "", "", false
}

func (m *Manager) cleanupCodexPending() {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, p := range m.agents {
		if now.Sub(p.started) <= codexPendingTTL {
			continue
		}
		p.session.close()
		if p.cancel != nil {
			p.cancel()
		}
		delete(m.agents, id)
	}
}

func (m *Manager) storeCodexPending(p *codexPending) {
	m.cleanupCodexPending()
	m.mu.Lock()
	m.agents[p.callID] = p
	m.mu.Unlock()
}

func (m *Manager) takeCodexPending(callID string) (*codexPending, bool) {
	m.cleanupCodexPending()
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.agents[callID]
	if ok {
		delete(m.agents, callID)
	}
	return p, ok
}

func advanceCodexTurn(s *codexSession, aliasToOriginal map[string]string) (codexTurnEvent, error) {
	var answer strings.Builder
	var turnErr string
	for s.scanner.Scan() {
		var msg map[string]any
		if json.Unmarshal(s.scanner.Bytes(), &msg) != nil {
			continue
		}
		method := stringValue(msg["method"])
		p, _ := msg["params"].(map[string]any)
		switch method {
		case "item/agentMessage/delta":
			answer.WriteString(stringValue(p["delta"]))
		case "item/tool/call":
			callID := stringValue(p["callId"])
			if callID == "" {
				callID = fmt.Sprintf("codex-tool-%d", time.Now().UnixNano())
			}
			alias := stringValue(p["tool"])
			tool := alias
			if orig := aliasToOriginal[alias]; orig != "" {
				tool = orig
			}
			args := "{}"
			if raw, ok := p["arguments"]; ok {
				if b, err := json.Marshal(raw); err == nil {
					args = protocol.RepairArguments(string(b))
				}
			}
			return codexTurnEvent{Kind: "tool", Text: answer.String(), RPCID: msg["id"], CallID: callID, Tool: tool, Arguments: args}, nil
		case "error":
			if e, ok := p["error"].(map[string]any); ok {
				turnErr = stringValue(e["message"])
			}
		case "turn/completed":
			status := "completed"
			if t, ok := p["turn"].(map[string]any); ok {
				status = firstNonEmpty(stringValue(t["status"]), status)
				if status == "failed" && turnErr == "" {
					if e, ok := t["error"].(map[string]any); ok {
						turnErr = stringValue(e["message"])
					}
				}
			}
			if turnErr != "" {
				return codexTurnEvent{Kind: "error", Text: answer.String(), TurnStatus: status, Error: turnErr}, nil
			}
			return codexTurnEvent{Kind: "final", Text: answer.String(), TurnStatus: status}, nil
		}
	}
	if err := s.scanner.Err(); err != nil {
		return codexTurnEvent{}, err
	}
	return codexTurnEvent{}, io.EOF
}

func codexToolHTTPResponse(native string, stream bool, ev codexTurnEvent) *UpstreamResponse {
	if stream {
		id := fmt.Sprintf("omnibridge-codex-%d", time.Now().UnixNano())
		chunk1 := map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": native,
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{"index": 0, "id": ev.CallID, "type": "function", "function": map[string]any{"name": ev.Tool, "arguments": protocol.RepairArguments(ev.Arguments)}}}}, "finish_reason": nil}},
		}
		chunk2 := map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": native, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}}}
		b1, _ := json.Marshal(chunk1)
		b2, _ := json.Marshal(chunk2)
		var out bytes.Buffer
		fmt.Fprintf(&out, "data: %s\n\n", b1)
		fmt.Fprintf(&out, "data: %s\n\n", b2)
		out.WriteString("data: [DONE]\n\n")
		return &UpstreamResponse{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(bytes.NewReader(out.Bytes()))}
	}
	payload := map[string]any{
		"id": fmt.Sprintf("omnibridge-codex-%d", time.Now().UnixNano()), "object": "chat.completion", "created": time.Now().Unix(), "model": native,
		"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{"id": ev.CallID, "type": "function", "function": map[string]any{"name": ev.Tool, "arguments": protocol.RepairArguments(ev.Arguments)}}}}, "finish_reason": "tool_calls"}},
	}
	b, _ := json.Marshal(payload)
	return &UpstreamResponse{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(b))}
}

func codexFinalHTTPResponse(native string, stream bool, text string) *UpstreamResponse {
	if stream {
		id := fmt.Sprintf("omnibridge-codex-%d", time.Now().UnixNano())
		chunk1 := map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": native, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": text}, "finish_reason": nil}}}
		chunk2 := map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": native, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}}
		b1, _ := json.Marshal(chunk1)
		b2, _ := json.Marshal(chunk2)
		var out bytes.Buffer
		fmt.Fprintf(&out, "data: %s\n\n", b1)
		fmt.Fprintf(&out, "data: %s\n\n", b2)
		out.WriteString("data: [DONE]\n\n")
		return &UpstreamResponse{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(bytes.NewReader(out.Bytes()))}
	}
	payload := map[string]any{"id": fmt.Sprintf("omnibridge-codex-%d", time.Now().UnixNano()), "object": "chat.completion", "created": time.Now().Unix(), "model": native, "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": text}, "finish_reason": "stop"}}}
	b, _ := json.Marshal(payload)
	return &UpstreamResponse{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(b))}
}

func (m *Manager) proxyCodexChat(ctx context.Context, a config.AccountConnection, body []byte) (*UpstreamResponse, error) {
	var req codexChatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	_, native := nativeFromQualified(req.Model)

	if callID, toolOutput, ok := lastToolResult(req.Messages); ok {
		pending, found := m.takeCodexPending(callID)
		if !found {
			return nil, fmt.Errorf("Codex tool continuation %q expired or OmniBridge was restarted; retry the agent request", callID)
		}
		if pending.accountID != a.ID || (pending.model != "" && native != "" && pending.model != native) {
			pending.session.close()
			if pending.cancel != nil {
				pending.cancel()
			}
			return nil, errors.New("Codex tool continuation does not belong to this account/model")
		}
		result := map[string]any{"contentItems": []any{map[string]any{"type": "inputText", "text": toolOutput}}, "success": true}
		if err := pending.session.send(map[string]any{"id": pending.rpcID, "result": result}); err != nil {
			pending.session.close()
			if pending.cancel != nil {
				pending.cancel()
			}
			return nil, err
		}
		ev, err := advanceCodexTurn(pending.session, pending.aliasToOriginal)
		if err != nil {
			pending.session.close()
			if pending.cancel != nil {
				pending.cancel()
			}
			return nil, err
		}
		if ev.Kind == "tool" {
			pending.rpcID = ev.RPCID
			pending.callID = ev.CallID
			pending.started = time.Now()
			m.storeCodexPending(pending)
			return codexToolHTTPResponse(native, req.Stream, ev), nil
		}
		pending.session.close()
		if pending.cancel != nil {
			pending.cancel()
		}
		if ev.Kind == "error" {
			return nil, errors.New(ev.Error)
		}
		return codexFinalHTTPResponse(native, req.Stream, ev.Text), nil
	}

	dynamicTools, aliases := codexDynamicTools(req.Tools)
	var sessionCtx context.Context = ctx
	var cancel context.CancelFunc
	if len(dynamicTools) > 0 {
		sessionCtx, cancel = context.WithTimeout(context.Background(), codexPendingTTL)
	}
	s, err := m.newCodexSession(sessionCtx, a)
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, err
	}
	workspace := filepath.Join(m.accountDir(a), "workspace")
	_ = os.MkdirAll(workspace, 0700)
	threadParams := map[string]any{"model": native, "cwd": workspace, "approvalPolicy": "never", "sandbox": "readOnly", "serviceName": "omnibridge"}
	if len(dynamicTools) > 0 {
		threadParams["dynamicTools"] = dynamicTools
	}
	thread, err := s.call(map[string]any{"method": "thread/start", "params": threadParams})
	if err != nil {
		s.close()
		if cancel != nil {
			cancel()
		}
		return nil, err
	}
	th, _ := thread["thread"].(map[string]any)
	tid := stringValue(th["id"])
	if tid == "" {
		s.close()
		if cancel != nil {
			cancel()
		}
		return nil, errors.New("codex did not return thread id")
	}
	prompt := codexPrompt(req.Messages) + codexToolChoiceInstruction(req.ToolChoice)
	if strings.TrimSpace(prompt) == "" {
		prompt = "Continue the conversation."
	}
	turnID := s.nextID
	if err := s.send(map[string]any{"method": "turn/start", "id": turnID, "params": map[string]any{"threadId": tid, "input": []any{map[string]any{"type": "text", "text": prompt}}, "model": native, "cwd": workspace, "approvalPolicy": "never", "sandboxPolicy": map[string]any{"type": "readOnly", "access": map[string]any{"type": "restricted", "includePlatformDefaults": true, "readableRoots": []string{workspace}}}}}); err != nil {
		s.close()
		if cancel != nil {
			cancel()
		}
		return nil, err
	}
	s.nextID++
	ev, err := advanceCodexTurn(s, aliases)
	if err != nil {
		s.close()
		if cancel != nil {
			cancel()
		}
		return nil, err
	}
	if ev.Kind == "tool" {
		if len(dynamicTools) == 0 {
			s.close()
			if cancel != nil {
				cancel()
			}
			return nil, errors.New("Codex requested a dynamic tool but no VS Code tools were registered")
		}
		m.storeCodexPending(&codexPending{accountID: a.ID, model: native, session: s, cancel: cancel, rpcID: ev.RPCID, callID: ev.CallID, aliasToOriginal: aliases, started: time.Now()})
		return codexToolHTTPResponse(native, req.Stream, ev), nil
	}
	s.close()
	if cancel != nil {
		cancel()
	}
	if ev.Kind == "error" {
		return nil, errors.New(ev.Error)
	}
	return codexFinalHTTPResponse(native, req.Stream, ev.Text), nil
}

func (m *Manager) AccountInfo(ctx context.Context, a config.AccountConnection) AccountInfo {
	checked := time.Now()
	var out AccountInfo
	switch a.Kind {
	case "chatgpt-codex":
		x, err := m.codexAccountInfo(ctx, a)
		if err != nil {
			status := classifyAccountError(err)
			out = AccountInfo{ID: a.ID, Kind: a.Kind, Status: status, Error: err.Error(), ReauthRequired: status == "reauth_required"}
		} else {
			out = x
		}
	case "gemini-developer-oauth":
		out = m.geminiAccountInfo(ctx, a)
	default:
		out = AccountInfo{ID: a.ID, Kind: a.Kind, Status: "unavailable", Error: "account connector unavailable"}
	}
	out.CheckedAt = checked
	out.ReauthRequired = out.ReauthRequired || out.Status == "reauth_required"
	return out
}

func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
func firstNonEmpty(v ...string) string {
	for _, x := range v {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}
func contentText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		var b strings.Builder
		for _, it := range x {
			if m, ok := it.(map[string]any); ok {
				if t := stringValue(m["text"]); t != "" {
					b.WriteString(t)
				}
			}
		}
		return b.String()
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}
func nativeFromQualified(s string) (string, string) {
	if i := strings.IndexByte(s, '/'); i > 0 {
		return s[:i], s[i+1:]
	}
	return "", s
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, p := range m.agents {
		p.session.close()
		if p.cancel != nil {
			p.cancel()
		}
		delete(m.agents, id)
	}
}

func (m *Manager) CancelAccountSessions(accountID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, p := range m.agents {
		if p.accountID != accountID {
			continue
		}
		p.session.close()
		if p.cancel != nil {
			p.cancel()
		}
		delete(m.agents, id)
	}
}

func (m *Manager) Logout(ctx context.Context, a config.AccountConnection) error {
	m.CancelAccountSessions(a.ID)
	switch a.Kind {
	case "chatgpt-codex":
		s, err := m.newCodexSession(ctx, a)
		if err != nil {
			return err
		}
		defer s.close()
		_, err = s.call(map[string]any{"method": "account/logout"})
		return err
	case "gemini-developer-oauth":
		// Best-effort remote revocation, then always remove the local protected token.
		if raw, err := m.Secrets.Get("account:" + a.ID + ":oauth"); err == nil {
			var tok googleToken
			if json.Unmarshal([]byte(raw), &tok) == nil && tok.AccessToken != "" {
				req, _ := http.NewRequestWithContext(ctx, "POST", "https://oauth2.googleapis.com/revoke?token="+url.QueryEscape(tok.AccessToken), nil)
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				resp, _ := m.Client.Do(req)
				if resp != nil {
					resp.Body.Close()
				}
			}
		}
		return m.Secrets.Delete("account:" + a.ID + ":oauth")
	default:
		return ErrUnsupportedAccount
	}
}
