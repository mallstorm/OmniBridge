package accounts

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"omnibridge/internal/config"
)

// Preflight verifies the local prerequisites for an interactive account login.
// It never returns credentials or tokens and does not start a login attempt.
func (m *Manager) Preflight(ctx context.Context, a config.AccountConnection) AccountPreflight {
	out := AccountPreflight{AccountID: a.ID, Kind: a.Kind, Status: "not_ready", Diagnostics: map[string]any{}}
	switch a.Kind {
	case "chatgpt-codex":
		rt, err := m.prepareCodexRuntime(a)
		if err != nil {
			out.Error = err.Error()
			return out
		}
		out.Helper = rt.bin
		out.Backend = "codex-cli"
		if b := strings.ToLower(strings.TrimSpace(a.Settings["browser_backend"])); b == "app-server" || b == "appserver" {
			out.Backend = "codex-app-server"
		}
		out.Diagnostics["codex_home"] = rt.home
		out.Diagnostics["workspace"] = rt.workspace

		versionCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		cmd := codexExecCommand(versionCtx, rt.bin, "--version")
		cmd.Env, cmd.Dir = rt.env, rt.workspace
		applyHiddenProcess(cmd)
		b, versionErr := cmd.CombinedOutput()
		cancel()
		if versionErr != nil {
			out.Error = fmt.Sprintf("Codex CLI did not pass --version: %v", versionErr)
			return out
		}
		out.Version = strings.TrimSpace(string(b))

		probeCtx, probeCancel := context.WithTimeout(ctx, 8*time.Second)
		s, err := m.newCodexSession(probeCtx, a)
		if err == nil {
			s.close()
		}
		probeCancel()
		if err != nil {
			out.Error = "Codex app-server preflight failed: " + err.Error()
			return out
		}
		out.Ready, out.Status = true, "ready"
		return out

	case "gemini-developer-oauth":
		c, err := m.googleClient(a)
		if err != nil {
			out.Error = err.Error()
			return out
		}
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			out.Error = "Google OAuth loopback listener unavailable: " + err.Error()
			return out
		}
		addr := ln.Addr().String()
		_ = ln.Close()
		out.Backend = "google-desktop-oauth"
		out.Diagnostics["project_id"] = c.Installed.ProjectID
		out.Diagnostics["auth_uri"] = effectiveGoogleAuthURI(c)
		out.Diagnostics["token_uri"] = effectiveGoogleTokenURI(c)
		out.Diagnostics["loopback"] = addr
		out.Diagnostics["scopes"] = []string{
			"https://www.googleapis.com/auth/cloud-platform",
			"https://www.googleapis.com/auth/generative-language.retriever",
		}
		out.Ready, out.Status = true, "ready"
		return out
	default:
		out.Error = ErrUnsupportedAccount.Error()
		return out
	}
}
