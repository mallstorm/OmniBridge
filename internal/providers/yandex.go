package providers

import (
	"context"
	"errors"
	"fmt"
	"omnibridge/internal/config"
	"strings"
)

const yandexAIStudioBaseURL = "https://ai.api.cloud.yandex.net/v1"

// yandexModelSpec carries human-readable metadata used to make the raw
// discovery output (plain ids like "yandexgpt/latest") usable in the Model
// Registry, Model Doctor and the VS Code export without hard-coding generation
// costs (pricing still lives only in Quota & Cost Engine with verified tariffs).
type yandexModelSpec struct {
	Display         string
	ContextWindow   int
	MaxOutputTokens int
	ToolCalling     bool
	Reasoning       bool
	Vision          bool
}

var yandexModelSpecs = map[string]yandexModelSpec{
	"yandexgpt":                  {Display: "YandexGPT", ContextWindow: 32768, MaxOutputTokens: 8192, ToolCalling: true},
	"yandexgpt/latest":           {Display: "YandexGPT", ContextWindow: 32768, MaxOutputTokens: 8192, ToolCalling: true},
	"yandexgpt-5":                {Display: "YandexGPT 5", ContextWindow: 32768, MaxOutputTokens: 8192, ToolCalling: true},
	"yandexgpt-5/latest":         {Display: "YandexGPT 5", ContextWindow: 32768, MaxOutputTokens: 8192, ToolCalling: true},
	"yandexgpt-5.1":              {Display: "YandexGPT 5.1", ContextWindow: 32768, MaxOutputTokens: 8192, ToolCalling: true},
	"yandexgpt-5.1/latest":       {Display: "YandexGPT 5.1", ContextWindow: 32768, MaxOutputTokens: 8192, ToolCalling: true},
	"yandexgpt-5-pro":            {Display: "YandexGPT 5 Pro", ContextWindow: 32768, MaxOutputTokens: 8192, ToolCalling: true},
	"yandexgpt-5-pro/latest":     {Display: "YandexGPT 5 Pro", ContextWindow: 32768, MaxOutputTokens: 8192, ToolCalling: true},
	"yandexgpt-5-lite":           {Display: "YandexGPT 5 Lite", ContextWindow: 32768, MaxOutputTokens: 8192, ToolCalling: true},
	"yandexgpt-5-lite/latest":    {Display: "YandexGPT 5 Lite", ContextWindow: 32768, MaxOutputTokens: 8192, ToolCalling: true},
	"yandexgpt-pro":              {Display: "YandexGPT Pro", ContextWindow: 32768, MaxOutputTokens: 8192, ToolCalling: true},
	"yandexgpt-pro/latest":       {Display: "YandexGPT Pro", ContextWindow: 32768, MaxOutputTokens: 8192, ToolCalling: true},
	"yandexgpt-lite":             {Display: "YandexGPT Lite", ContextWindow: 32768, MaxOutputTokens: 8192, ToolCalling: true},
	"yandexgpt-lite/latest":      {Display: "YandexGPT Lite", ContextWindow: 32768, MaxOutputTokens: 8192, ToolCalling: true},
	"deepseek-v4-flash":          {Display: "DeepSeek V4 Flash", ContextWindow: 1048576, MaxOutputTokens: 16384, ToolCalling: true, Reasoning: true},
	"deepseek-v4-flash/latest":   {Display: "DeepSeek V4 Flash", ContextWindow: 1048576, MaxOutputTokens: 16384, ToolCalling: true, Reasoning: true},
	"deepseek-v4":                {Display: "DeepSeek V4", ContextWindow: 1048576, MaxOutputTokens: 16384, ToolCalling: true, Reasoning: true},
	"deepseek-v4/latest":         {Display: "DeepSeek V4", ContextWindow: 1048576, MaxOutputTokens: 16384, ToolCalling: true, Reasoning: true},
	"deepseek-v3":                {Display: "DeepSeek V3", ContextWindow: 65536, MaxOutputTokens: 8192, ToolCalling: true, Reasoning: true},
	"qwen3-235b-a22b-fp8":        {Display: "Qwen3 235B A22B FP8", ContextWindow: 262144, MaxOutputTokens: 16384, ToolCalling: true, Reasoning: true},
	"qwen3-235b-a22b-fp8/latest": {Display: "Qwen3 235B A22B FP8", ContextWindow: 262144, MaxOutputTokens: 16384, ToolCalling: true, Reasoning: true},
	"qwen3-32b":                  {Display: "Qwen3 32B", ContextWindow: 131072, MaxOutputTokens: 16384, ToolCalling: true, Reasoning: true},
	"gpt-oss-120b":               {Display: "GPT-OSS 120B", ContextWindow: 131072, MaxOutputTokens: 16384, ToolCalling: true, Reasoning: true},
	"gpt-oss-120b/latest":        {Display: "GPT-OSS 120B", ContextWindow: 131072, MaxOutputTokens: 16384, ToolCalling: true, Reasoning: true},
	"gpt-oss-20b":                {Display: "GPT-OSS 20B", ContextWindow: 131072, MaxOutputTokens: 16384, ToolCalling: true, Reasoning: true},
	"gpt-oss-20b/latest":         {Display: "GPT-OSS 20B", ContextWindow: 131072, MaxOutputTokens: 16384, ToolCalling: true, Reasoning: true},
	"yavlan":                     {Display: "Yandex Verna", ContextWindow: 32768, MaxOutputTokens: 8192, ToolCalling: true},
	"yavlan/latest":              {Display: "Yandex Verna", ContextWindow: 32768, MaxOutputTokens: 8192, ToolCalling: true},
	"yandex-art":                 {Display: "YandexART", ContextWindow: 0, MaxOutputTokens: 0},
}

type yandexExecutor struct{ m *Manager }

func (e *yandexExecutor) Name() string { return "yandex" }

func prepareYandexProvider(p config.Provider) (config.Provider, error) {
	p = config.NormalizeProvider(p)
	projectID := strings.TrimSpace(p.ProjectID)
	if projectID == "" {
		projectID = strings.TrimSpace(p.Headers["OpenAI-Project"])
	}
	if projectID == "" {
		return config.Provider{}, errors.New("Yandex Folder ID is required (OpenAI-Project)")
	}
	if strings.ContainsAny(projectID, "\r\n\t ") || len(projectID) > 128 {
		return config.Provider{}, errors.New("Yandex Folder ID has invalid format")
	}
	if p.BaseURL == "" {
		p.BaseURL = yandexAIStudioBaseURL
	}
	if p.ModelsURL == "" {
		p.ModelsURL = strings.TrimRight(p.BaseURL, "/") + "/models"
	}
	if p.Headers == nil {
		p.Headers = map[string]string{}
	}
	p.ProjectID = projectID
	p.Headers["OpenAI-Project"] = projectID
	p.AuthType = "yandex-api-key"
	return p, nil
}

// yandexModelSlug strips the "gpt://<folder-id>/" prefix (when present) from a
// discovery id and returns the stable model slug, e.g. "yandexgpt/latest".
func yandexModelSlug(raw string) string {
	s := strings.TrimSpace(strings.TrimPrefix(raw, "models/"))
	if i := strings.Index(s, "gpt://"); i >= 0 {
		rest := s[i+len("gpt://"):]
		if j := strings.IndexByte(rest, '/'); j >= 0 {
			rest = rest[j+1:]
		}
		s = rest
	}
	return strings.Trim(strings.TrimPrefix(s, "/"), " ")
}

// NativeModelSlug exposes the slug derivation used by Yandex model URIs so the
// server routing layer can resolve short references ("yandexgpt/latest") to the
// full foldered native ids stored in the Model Registry.
func NativeModelSlug(nativeID string) string { return yandexModelSlug(nativeID) }

// YandexChatUsable reports whether a discovered Yandex model can be exported to
// the VS Code Copilot chat. AI Studio returns non-conversational endpoints too
// (speech/real-time/vision/embedding suites); those must stay in the Catalog
// for discovery/Model Doctor but are useless in the Copilot chat picker and would
// only produce agent noise.
func YandexChatUsable(nativeID string) bool {
	slug := strings.ToLower(yandexModelSlug(nativeID))
	if slug == "" {
		return false
	}
	// A hand-written list of non-conversational families. In contrast to the
	// generic clearlyNonGenerationModel patterns, "user/assistant"-style chat
	// models (e.g. deepseek-v4-flash) must survive this Yandex-specific gate.
	nonChat := []string{
		"speech-realtime", "speech_realtime",
		"realtime", "streaming-speech",
		"tts", "text-to-speech", "texttospeech",
		"stt", "speech-to-text", "speechtotext", "whisper",
		"embedding", "embeddings", "moderation",
		"image-generation", "text-to-image", "texttoimage",
		"image-to-text", "videogeneration", "video-generation",
	}
	for _, token := range nonChat {
		if strings.Contains(slug, token) {
			return false
		}
	}
	return true
}

// yandexModelURI builds the canonical model URI used by upstream requests. A
// discovery that returns already-foldered URIs is left untouched; short ids are
// prefixed with the current project so generation requests remain valid.
func yandexModelURI(projectID, nativeID string) string {
	trimmed := strings.TrimSpace(nativeID)
	if strings.HasPrefix(trimmed, "gpt://") {
		return trimmed
	}
	slug := yandexModelSlug(trimmed)
	if slug == "" {
		return trimmed
	}
	return "gpt://" + projectID + "/" + slug
}

func yandexSpecFor(slug string) (yandexModelSpec, bool) {
	if spec, ok := yandexModelSpecs[slug]; ok {
		return spec, true
	}
	base := strings.TrimSuffix(slug, "/latest")
	if base != slug {
		if spec, ok := yandexModelSpecs[base]; ok {
			return spec, true
		}
	}
	return yandexModelSpec{}, false
}

// yandexFriendlyName produces a human-readable name for the UI and the VS Code
// export. Known families get a curated display name; unknown slugs get a
// deterministic derived name so the interface never shows raw URIs.
func yandexFriendlyName(slug string) string {
	if spec, ok := yandexModelSpecs[slug]; ok && spec.Display != "" {
		return spec.Display
	}
	base := strings.TrimSuffix(slug, "/latest")
	if base != slug {
		if spec, ok := yandexModelSpecs[base]; ok && spec.Display != "" {
			return spec.Display
		}
		return base
	}
	if base == "" {
		return slug
	}
	parts := strings.Split(base, "-")
	words := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		words = append(words, strings.ToUpper(part[:1])+part[1:])
	}
	return strings.Join(words, " ")
}

func (e *yandexExecutor) ListModels(ctx context.Context, p config.Provider) ([]Model, error) {
	prepared, err := prepareYandexProvider(p)
	if err != nil {
		return nil, err
	}
	models, err := e.m.listOpenAIModels(ctx, prepared)
	if err != nil {
		return nil, fmt.Errorf("Yandex AI Studio model discovery failed: %w", err)
	}
	for i := range models {
		e.annotateYandexModel(&models[i], prepared.ProjectID)
	}
	if len(models) == 0 {
		// Never surface an empty registry for a valid key: current AI Studio
		// builds list known model families, but if the account exposes none yet,
		// offer the documented public models so the user can run Model Doctor.
		models = yandexCuratedModels(prepared.ProjectID)
	}
	for i := range models {
		if models[i].ProviderID == "" {
			models[i].ProviderID = prepared.ID
		}
		// The registry key must stay aligned with the canonical native URI so
		// routing, VS Code export and requests all use the same identifier.
		models[i].ID = prepared.ID + "/" + models[i].NativeID
		if models[i].ProviderName == "" {
			models[i].ProviderName = prepared.Name
			if models[i].ProviderName == "" {
				models[i].ProviderName = "Yandex AI Studio"
			}
		}
		if models[i].APIType == "" {
			models[i].APIType = prepared.PreferredProtocol
		}
		if models[i].PreferredProtocol == "" {
			models[i].PreferredProtocol = prepared.PreferredProtocol
		}
		if len(models[i].Protocols) == 0 {
			models[i].Protocols = append([]string(nil), prepared.Protocols...)
		}
		if models[i].Source == "" {
			models[i].Source = "api"
		}
	}
	return models, nil
}

// yandexRawSlugName reports whether a discovery-provided name is still the raw
// technical slug (lowercase letters/digits/hyphens with optional "/latest"),
// which means the upstream has not supplied a human-readable display name.
func yandexRawSlugName(name string) bool {
	s := strings.TrimSpace(name)
	if s == "" {
		return false
	}
	if strings.ContainsAny(s, " \t") {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		case r == '/':
		default:
			return false
		}
	}
	return true
}

func (e *yandexExecutor) annotateYandexModel(m *Model, projectID string) {
	m.NativeID = yandexModelURI(projectID, m.NativeID)
	slug := yandexModelSlug(m.NativeID)
	if spec, ok := yandexSpecFor(slug); ok {
		// For known families the curated display name wins: the upstream list
		// usually has no displayName at all and only returns the raw model slug.
		m.Name = spec.Display
		if m.ContextWindow <= 0 {
			m.ContextWindow = spec.ContextWindow
		}
		if m.MaxOutputTokens <= 0 {
			m.MaxOutputTokens = spec.MaxOutputTokens
		}
		if spec.ToolCalling {
			m.ToolCalling = true
		}
		if spec.Reasoning {
			m.Reasoning = true
		}
		if spec.Vision {
			m.Vision = true
		}
	} else if m.Name == "" || strings.Contains(m.Name, "gpt://") || yandexRawSlugName(m.Name) {
		// Unknown families can still come back as a plain slug (no displayName).
		// Replace the raw id with a deterministic derived name so the UI and the
		// VS Code export never show "yandexgpt-5-pro" as the model title.
		m.Name = yandexFriendlyName(slug)
	}
	if m.Name == "" {
		m.Name = slug
	}
}

// yandexCuratedModels is the documented model family used as a non-empty
// starting point when the discovery endpoint returns no generation models.
func yandexCuratedModels(projectID string) []Model {
	base := []struct {
		Slug string
		Spec yandexModelSpec
	}{
		{Slug: "yandexgpt/latest", Spec: yandexModelSpecs["yandexgpt/latest"]},
		{Slug: "yandexgpt-5.1", Spec: yandexModelSpecs["yandexgpt-5.1"]},
		{Slug: "deepseek-v4-flash/latest", Spec: yandexModelSpecs["deepseek-v4-flash/latest"]},
		{Slug: "qwen3-235b-a22b-fp8/latest", Spec: yandexModelSpecs["qwen3-235b-a22b-fp8/latest"]},
		{Slug: "gpt-oss-120b/latest", Spec: yandexModelSpecs["gpt-oss-120b/latest"]},
	}
	out := make([]Model, 0, len(base))
	for _, b := range base {
		out = append(out, Model{
			NativeID: "gpt://" + projectID + "/" + b.Slug, Name: b.Spec.Display,
			Streaming: true, ToolCalling: b.Spec.ToolCalling,
			Reasoning: b.Spec.Reasoning, ContextWindow: b.Spec.ContextWindow,
			MaxOutputTokens: b.Spec.MaxOutputTokens, Status: "discovered",
		})
	}
	return out
}

func (e *yandexExecutor) Proxy(ctx context.Context, p config.Provider, apiType string, body []byte) (*UpstreamResponse, error) {
	if apiType != "responses" && apiType != "chat-completions" {
		return nil, ErrUnsupportedAPI
	}
	prepared, err := prepareYandexProvider(p)
	if err != nil {
		return nil, err
	}
	clean, err := sanitizeProviderRequest(prepared, apiType, body)
	if err != nil {
		return nil, err
	}
	prepared.BaseURL = config.ProtocolBaseURL(prepared, apiType)
	prepared.AuthType = config.ProtocolAuthType(prepared, apiType)
	return e.m.proxyOpenAICompatible(ctx, prepared, apiType, clean)
}
