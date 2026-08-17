package accounts

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omnibridge/internal/config"
	"omnibridge/internal/secretstore"
)

func TestMain(m *testing.M) {
	if os.Getenv("OMNIBRIDGE_CODEX_TEST_HELPER") == "1" {
		runCodexTestHelper()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runCodexTestHelper() {
	for _, arg := range os.Args[1:] {
		if arg == "--version" {
			fmt.Fprintln(os.Stdout, "codex-cli 0.test")
			return
		}
		if arg == "login" {
			// Simulate the official CLI owning browser + callback and writing its
			// session into the isolated CODEX_HOME used by app-server verification.
			fmt.Fprintln(os.Stdout, "Login successful")
			return
		}
	}
	sc := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	hasDynamicTools := false
	waitingDynamicResult := false
	for sc.Scan() {
		var req map[string]any
		if json.Unmarshal(sc.Bytes(), &req) != nil {
			continue
		}
		id := req["id"]
		method, _ := req["method"].(string)
		respond := func(result any) {
			if id != nil {
				_ = enc.Encode(map[string]any{"id": id, "result": result})
			}
		}
		switch method {
		case "initialize":
			respond(map[string]any{})
		case "initialized":
			// notification: no response
		case "account/read":
			respond(map[string]any{"account": map[string]any{"type": "chatgpt", "email": "tester@example.com", "planType": "plus"}, "requiresOpenaiAuth": true})
		case "account/login/start":
			params, _ := req["params"].(map[string]any)
			typ, _ := params["type"].(string)
			if typ == "chatgpt" && len(params) != 1 {
				_ = enc.Encode(map[string]any{"id": id, "error": map[string]any{"code": -32602, "message": "browser login must use minimal compatibility payload"}})
				continue
			}
			result := map[string]any{"type": typ, "loginId": "login-test"}
			if typ == "chatgptDeviceCode" {
				result["verificationUrl"] = "https://auth.openai.com/codex/device"
				result["userCode"] = "ABCD-1234"
			} else {
				result["authUrl"] = "https://chatgpt.com/mock-login"
			}
			respond(result)
			_ = enc.Encode(map[string]any{"method": "account/login/completed", "params": map[string]any{"loginId": "login-test", "success": true, "error": nil}})
		case "account/logout":
			respond(map[string]any{})
		case "model/list":
			respond(map[string]any{"data": []any{map[string]any{"id": "gpt-test", "model": "gpt-test", "displayName": "GPT Test", "inputModalities": []any{"text", "image"}, "isDefault": true}}, "nextCursor": nil})
		case "account/rateLimits/read":
			respond(map[string]any{"rateLimits": map[string]any{"limitId": "codex", "primary": map[string]any{"usedPercent": 25}}})
		case "account/usage/read":
			respond(map[string]any{"summary": map[string]any{"lifetimeTokens": 12345}})
		case "thread/start":
			params, _ := req["params"].(map[string]any)
			tools, _ := params["dynamicTools"].([]any)
			hasDynamicTools = len(tools) > 0
			respond(map[string]any{"thread": map[string]any{"id": "thr_test"}})
		case "turn/start":
			respond(map[string]any{"turn": map[string]any{"id": "turn_test", "status": "inProgress"}})
			if hasDynamicTools {
				waitingDynamicResult = true
				_ = enc.Encode(map[string]any{"method": "item/started", "params": map[string]any{"threadId": "thr_test", "turnId": "turn_test", "item": map[string]any{"id": "call_123", "type": "dynamicToolCall", "status": "inProgress", "tool": "test_probe", "arguments": map[string]any{"value": 7}}}})
				_ = enc.Encode(map[string]any{"method": "item/tool/call", "id": 60, "params": map[string]any{"threadId": "thr_test", "turnId": "turn_test", "callId": "call_123", "tool": "test_probe", "arguments": map[string]any{"value": 7}}})
			} else {
				_ = enc.Encode(map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{"threadId": "thr_test", "turnId": "turn_test", "delta": "OK"}})
				_ = enc.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thr_test", "turn": map[string]any{"id": "turn_test", "status": "completed"}}})
			}
		default:
			if waitingDynamicResult && method == "" {
				if f, ok := id.(float64); ok && int(f) == 60 {
					waitingDynamicResult = false
					_ = enc.Encode(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": "thr_test", "turnId": "turn_test", "item": map[string]any{"id": "call_123", "type": "dynamicToolCall", "status": "completed", "success": true}}})
					_ = enc.Encode(map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{"threadId": "thr_test", "turnId": "turn_test", "delta": "TOOL_OK"}})
					_ = enc.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thr_test", "turn": map[string]any{"id": "turn_test", "status": "completed"}}})
					continue
				}
			}

			if id != nil {
				_ = enc.Encode(map[string]any{"id": id, "error": map[string]any{"code": -32601, "message": "unknown " + method}})
			}
		}
	}
}

func newCodexTestManager(t *testing.T) (*Manager, config.AccountConnection) {
	t.Helper()
	t.Setenv("OMNIBRIDGE_CODEX_TEST_HELPER", "1")
	root := t.TempDir()
	sec := secretstore.New(filepath.Join(root, "data", "secrets.json"))
	m := NewManager(root, sec)
	a := config.AccountConnection{ID: "chatgpt-test", Name: "ChatGPT test", Kind: "chatgpt-codex", Enabled: true, Settings: map[string]string{"command": os.Args[0]}}
	return m, a
}

func TestCodexOfficialAccountSurface(t *testing.T) {
	m, a := newCodexTestManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	info, err := m.codexAccountInfo(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != "connected" || info.Email != "tester@example.com" || info.Plan != "plus" {
		t.Fatalf("bad account info: %+v", info)
	}

	mods, err := m.listCodexModels(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 1 || mods[0].ID != "chatgpt-test/gpt-test" || !mods[0].ToolCalling || !mods[0].Vision {
		t.Fatalf("bad models: %+v", mods)
	}

	usage := m.CodexUsage(ctx, a)
	if usage.Status != "ok" || usage.Details["usage"] == nil || usage.Details["rate_limits"] == nil {
		t.Fatalf("bad usage: %+v", usage)
	}

	cfgBytes, err := os.ReadFile(filepath.Join(m.accountDir(a), "codex-home", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfgText := string(cfgBytes)
	if !strings.Contains(cfgText, `forced_login_method = "chatgpt"`) || !strings.Contains(cfgText, `cli_auth_credentials_store = "file"`) {
		t.Fatalf("auth isolation config missing: %s", cfgText)
	}
}

func TestCodexBrowserLoginUsesOfficialCLIByDefault(t *testing.T) {
	m, a := newCodexTestManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	login, err := m.StartCodexLogin(ctx, a, false)
	if err != nil {
		t.Fatal(err)
	}
	if !login.BrowserManaged || login.Backend != "codex-cli" || login.AuthURL != "" || login.VerificationURL != "" {
		t.Fatalf("bad CLI browser login start: %+v", login)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		st, ok := m.LoginStatus(login.AttemptID)
		if ok && st.Success && st.Status == "completed" {
			if st.Backend != "codex-cli" {
				t.Fatalf("bad login backend: %+v", st)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("CLI login did not complete: %+v", st)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCodexBrowserLoginAppServerCompatibilityBackend(t *testing.T) {
	m, a := newCodexTestManager(t)
	a.Settings["browser_backend"] = "app-server"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	login, err := m.StartCodexLogin(ctx, a, false)
	if err != nil {
		t.Fatal(err)
	}
	if login.AuthURL == "" || login.VerificationURL != "" || login.Backend != "codex-app-server" {
		t.Fatalf("bad app-server browser login start: %+v", login)
	}
}

func TestCodexLoginAndChatProxy(t *testing.T) {
	m, a := newCodexTestManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	login, err := m.StartCodexLogin(ctx, a, true)
	if err != nil {
		t.Fatal(err)
	}
	if login.AccountID != a.ID || login.UserCode != "ABCD-1234" || login.VerificationURL == "" {
		t.Fatalf("bad login start: %+v", login)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		st, ok := m.LoginStatus(login.AttemptID)
		if ok && st.Success && st.Status == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("login did not complete: %+v", st)
		}
		time.Sleep(10 * time.Millisecond)
	}

	body := []byte(`{"model":"chatgpt-test/gpt-test","messages":[{"role":"user","content":"Say OK"}]}`)
	resp, err := m.proxyCodexChat(ctx, a, body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), `"content":"OK"`) {
		t.Fatalf("bad proxy response status=%d body=%s", resp.StatusCode, b)
	}

	streamBody := []byte(`{"model":"chatgpt-test/gpt-test","stream":true,"messages":[{"role":"user","content":"Say OK"}]}`)
	streamResp, err := m.proxyCodexChat(ctx, a, streamBody)
	if err != nil {
		t.Fatal(err)
	}
	sb, _ := io.ReadAll(streamResp.Body)
	streamResp.Body.Close()
	if !strings.Contains(string(sb), "data: [DONE]") || !strings.Contains(streamResp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("bad stream: %s", sb)
	}
}

func TestCodexDynamicToolRoundTrip(t *testing.T) {
	m, a := newCodexTestManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	first := []byte(`{"model":"chatgpt-test/gpt-test","messages":[{"role":"user","content":"Call test_probe with value 7"}],"tools":[{"type":"function","function":{"name":"test_probe","description":"probe","parameters":{"type":"object","properties":{"value":{"type":"integer"}},"required":["value"]}}}]}`)
	resp, err := m.proxyCodexChat(ctx, a, first)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("decode tool response: %v body=%s", err, b)
	}
	choices, _ := decoded["choices"].([]any)
	if len(choices) != 1 {
		t.Fatalf("missing choices: %s", b)
	}
	choice, _ := choices[0].(map[string]any)
	if choice["finish_reason"] != "tool_calls" {
		t.Fatalf("expected tool_calls: %s", b)
	}
	msg, _ := choice["message"].(map[string]any)
	calls, _ := msg["tool_calls"].([]any)
	if len(calls) != 1 {
		t.Fatalf("missing tool call: %s", b)
	}
	call, _ := calls[0].(map[string]any)
	callID, _ := call["id"].(string)
	fn, _ := call["function"].(map[string]any)
	if callID != "call_123" || fn["name"] != "test_probe" {
		t.Fatalf("bad tool bridge: %s", b)
	}

	second := []byte(fmt.Sprintf(`{"model":"chatgpt-test/gpt-test","messages":[{"role":"user","content":"Call test_probe"},{"role":"assistant","content":null,"tool_calls":[{"id":%q,"type":"function","function":{"name":"test_probe","arguments":"{\"value\":7}"}}]},{"role":"tool","tool_call_id":%q,"content":"value=7"}]}`, callID, callID))
	resp2, err := m.proxyCodexChat(ctx, a, second)
	if err != nil {
		t.Fatal(err)
	}
	b2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if !strings.Contains(string(b2), `"content":"TOOL_OK"`) {
		t.Fatalf("tool continuation did not resume same turn: %s", b2)
	}
}

func TestCodexDynamicToolStreamingRoundTrip(t *testing.T) {
	m, a := newCodexTestManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first := []byte(`{"model":"chatgpt-test/gpt-test","stream":true,"messages":[{"role":"user","content":"Call test_probe"}],"tools":[{"type":"function","function":{"name":"test_probe","parameters":{"type":"object","properties":{"value":{"type":"integer"}}}}}]}`)
	resp, err := m.proxyCodexChat(ctx, a, first)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), `"finish_reason":"tool_calls"`) || !strings.Contains(string(b), `"id":"call_123"`) {
		t.Fatalf("bad streaming tool response: %s", b)
	}
	second := []byte(`{"model":"chatgpt-test/gpt-test","stream":true,"messages":[{"role":"tool","tool_call_id":"call_123","content":"7"}]}`)
	resp2, err := m.proxyCodexChat(ctx, a, second)
	if err != nil {
		t.Fatal(err)
	}
	b2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if !strings.Contains(string(b2), "TOOL_OK") || !strings.Contains(string(b2), "data: [DONE]") {
		t.Fatalf("bad streaming continuation: %s", b2)
	}
}

var _ = fmt.Sprintf

func TestCodexCommandAcceptsLegacyCodexPathSetting(t *testing.T) {
	m, a := newCodexTestManager(t)
	a.Settings = map[string]string{"codex_path": os.Args[0]}
	got, err := m.codexCommand(a)
	if err != nil {
		t.Fatal(err)
	}
	if got != os.Args[0] {
		t.Fatalf("legacy codex_path was not honored: got %q want %q", got, os.Args[0])
	}
}

func TestCodexPreflightReportsHelperVersionAndBackend(t *testing.T) {
	m, a := newCodexTestManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pre := m.Preflight(ctx, a)
	if !pre.Ready || pre.Status != "ready" || pre.Backend != "codex-cli" {
		t.Fatalf("bad preflight: %+v", pre)
	}
	if pre.Helper == "" || !strings.Contains(pre.Version, "codex-cli") {
		t.Fatalf("missing helper diagnostics: %+v", pre)
	}
	if pre.Diagnostics["codex_home"] == nil {
		t.Fatalf("missing codex home: %+v", pre)
	}
}
