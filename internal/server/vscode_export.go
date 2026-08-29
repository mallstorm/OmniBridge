package server

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"omnibridge/internal/providers"
)

// vscodeExportModel is the stable Custom Endpoint contract consumed by
// scripts/sync-vscode.ps1. Field names intentionally match VS Code's
// chatLanguageModels.json schema so the PowerShell layer only has to merge the
// provider section instead of re-implementing capability logic.
type vscodeExportModel struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	URL             string `json:"url"`
	APIType         string `json:"apiType"`
	ToolCalling     bool   `json:"toolCalling"`
	Vision          bool   `json:"vision"`
	Streaming       bool   `json:"streaming"`
	Thinking        bool   `json:"thinking,omitempty"`
	ContextWindow   int    `json:"contextWindow,omitempty"`
	MaxInputTokens  int    `json:"maxInputTokens,omitempty"`
	MaxOutputTokens int    `json:"maxOutputTokens,omitempty"`
	AgentReady      bool   `json:"agentReady,omitempty"`
	Virtual         bool   `json:"virtual,omitempty"`
}

func bridgeKeyFingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}

// vscodeModelExcluded hides models that are physically reachable through the
// gateway but must not appear in the VS Code Custom Endpoint picker. The Chat
// (Copilot) surface expects conversational models only; Yandex AI Studio also
// lists speech/realtime/vision/embedding suites which would break agent flows.
func (s *Server) vscodeModelExcluded(m providers.Model) bool {
	return s.vscodeModelExcludedReason(m) != ""
}

func (s *Server) vscodeModelExcludedReason(m providers.Model) string {
	if m.Status == "error" {
		return "model unavailable"
	}
	if !m.ChatVerified {
		return "basic chat probe not passed"
	}
	if m.ProviderID == "" {
		return ""
	}
	for _, p := range s.Config.Get().Providers {
		if p.ID == m.ProviderID && p.Enabled {
			if p.Kind == "yandex" && !providers.YandexChatUsable(m.NativeID) {
				return "not a conversational Yandex model"
			}
			return ""
		}
	}
	// Account/Local sources keep their existing behaviour.
	return ""
}

func vscodeAPIPath(apiType string) string {
	switch strings.ToLower(strings.TrimSpace(apiType)) {
	case "messages":
		return "/v1/messages"
	case "responses":
		return "/v1/responses"
	default:
		return "/v1/chat/completions"
	}
}

func vscodeTokenLimits(contextWindow, maxOutput int) (context, maxInput, output int) {
	if contextWindow > 0 {
		context = contextWindow
	}
	if maxOutput > 0 {
		output = maxOutput
	}
	if contextWindow > 0 && maxOutput > 0 && contextWindow > maxOutput {
		maxInput = contextWindow - maxOutput
	}
	return
}

func (s *Server) vscodeModelFromRegistry(base string, m providers.Model) (vscodeExportModel, bool) {
	apiType := strings.ToLower(strings.TrimSpace(m.APIType))
	switch apiType {
	case "messages", "responses", "chat-completions":
	default:
		return vscodeExportModel{}, false
	}
	if strings.EqualFold(strings.TrimSpace(m.Status), "error") {
		return vscodeExportModel{}, false
	}
	context, maxInput, maxOutput := vscodeTokenLimits(m.ContextWindow, m.MaxOutputTokens)
	return vscodeExportModel{
		ID:          m.ID,
		Name:        strings.TrimSpace(m.ProviderName + " · " + m.Name),
		URL:         base + vscodeAPIPath(apiType),
		APIType:     apiType,
		ToolCalling: modelAgentReady(m),
		Vision:      m.Vision,
		// VS Code sees the OmniBridge gateway endpoint, not the native upstream.
		// Protocol Engine provides a streaming-capable downstream transport and
		// safely falls back to one-shot SSE when an upstream ignores stream=true.
		Streaming:       true,
		Thinking:        m.Reasoning,
		ContextWindow:   context,
		MaxInputTokens:  maxInput,
		MaxOutputTokens: maxOutput,
		AgentReady:      modelAgentReady(m),
	}, true
}

func (s *Server) vscodeVirtualModel(base, id string, all []providers.Model) (vscodeExportModel, bool) {
	policy, profile := autoPolicy(id)
	candidates := s.filterRouterModels(all, policy.Mode, profile)
	if len(candidates) == 0 {
		return vscodeExportModel{}, false
	}

	var toolCalling, vision, streaming, thinking, agentReady bool
	contextWindow, maxOutput := 0, 0
	for _, m := range candidates {
		if m.Status == "error" || s.modelBlocked(m) {
			continue
		}
		toolCalling = toolCalling || modelAgentReady(m)
		vision = vision || m.Vision
		streaming = streaming || m.Streaming
		thinking = thinking || m.Reasoning
		agentReady = agentReady || modelAgentReady(m)
		if m.ContextWindow > contextWindow {
			contextWindow = m.ContextWindow
		}
		if m.MaxOutputTokens > maxOutput {
			maxOutput = m.MaxOutputTokens
		}
	}
	if !streaming {
		// Every OmniBridge virtual route is transported through the gateway's
		// own streaming-capable endpoint. If a selected upstream ignores
		// streaming, Protocol Engine 3.1 safely falls back to one-shot SSE.
		streaming = true
	}

	names := map[string]string{
		"auto/smart":     "Auto · Smart",
		"auto/coding":    "Auto · Coding",
		"auto/fast":      "Auto · Fast",
		"auto/cheap":     "Auto · Cheap",
		"auto/local":     "Auto · Local",
		"auto/reasoning": "Auto · Reasoning",
		"auto/vision":    "Auto · Vision",
		"auto/agent":     "Auto · Agent",
	}
	context, maxInput, output := vscodeTokenLimits(contextWindow, maxOutput)
	return vscodeExportModel{
		ID:              id,
		Name:            names[id],
		URL:             base + "/v1/chat/completions",
		APIType:         "chat-completions",
		ToolCalling:     toolCalling,
		Vision:          vision,
		Streaming:       streaming,
		Thinking:        thinking,
		ContextWindow:   context,
		MaxInputTokens:  maxInput,
		MaxOutputTokens: output,
		AgentReady:      agentReady,
		Virtual:         true,
	}, true
}

func (s *Server) bridgeVSCodeExport(w http.ResponseWriter, r *http.Request) {
	cfg := s.Config.Get()
	base := "http://" + cfg.Listen
	all := s.Registry.Models()
	models := make([]vscodeExportModel, 0, len(all)+len(autoVirtualModels))
	skipped := map[string]string{}

	// Put virtual routes first. In current VS Code builds the Custom Endpoint
	// picker can display model IDs instead of names, so the stable auto/* IDs
	// are deliberately human-readable and immediately useful in Agent mode.
	for _, id := range autoVirtualModels {
		if m, ok := s.vscodeVirtualModel(base, id, all); ok {
			models = append(models, m)
		}
	}
	for _, m := range all {
		if reason := s.vscodeModelExcludedReason(m); reason != "" {
			skipped[m.ID] = reason
			continue
		}
		if x, ok := s.vscodeModelFromRegistry(base, m); ok {
			models = append(models, x)
		}
	}

	agent := 0
	for _, m := range models {
		if m.ToolCalling {
			agent++
		}
	}
	jsonOut(w, http.StatusOK, map[string]any{
		"version":                  "3.0-verified",
		"provider_name":            "OmniBridge",
		"bridge_key_fingerprint":   bridgeKeyFingerprint(cfg.DataAPIKey),
		"recommended_auth_header":  "Authorization",
		"recommended_auth_scheme":  "Bearer",
		"explicit_auth_headers":    true,
		"vendor":                   "customendpoint",
		"default_api_type":         "chat-completions",
		"models":                   models,
		"models_total":             len(models),
		"agent_picker_models":      agent,
		"models_skipped":           len(skipped),
		"skipped_reasons":          skipped,
		"requires_vscode_reload":   true,
		"agent_host_setting":       "chat.agentHost.byokModels.enabled",
		"agent_host_setting_value": true,
	})
}
