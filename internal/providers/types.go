package providers

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"omnibridge/internal/config"
	"omnibridge/internal/secretstore"
	"strings"
	"time"
)

type Model struct {
	ID                string            `json:"id"`
	NativeID          string            `json:"native_id"`
	ProviderID        string            `json:"provider_id"`
	ProviderName      string            `json:"provider_name"`
	Name              string            `json:"name"`
	APIType           string            `json:"api_type"`
	Protocols         []string          `json:"protocols,omitempty"`
	PreferredProtocol string            `json:"preferred_protocol,omitempty"`
	OwnedBy           string            `json:"owned_by,omitempty"`
	Type              string            `json:"type,omitempty"`
	ToolCalling       bool              `json:"tool_calling"`
	ParallelTools     bool              `json:"parallel_tools,omitempty"`
	StructuredOutput  bool              `json:"structured_output,omitempty"`
	Reasoning         bool              `json:"reasoning,omitempty"`
	Streaming         bool              `json:"streaming"`
	Vision            bool              `json:"vision"`
	SystemPrompt      bool              `json:"system_prompt,omitempty"`
	AgentReady        bool              `json:"agent_ready,omitempty"`
	ChatVerified      bool              `json:"chat_verified,omitempty"`
	IdentityVerified  bool              `json:"identity_verified,omitempty"`
	UpstreamModelID   string            `json:"upstream_model_id,omitempty"`
	DoctorStatus      string            `json:"doctor_status,omitempty"`
	DoctorVerified    bool              `json:"doctor_verified"`
	DoctorScore       float64           `json:"doctor_score"`
	AgentScore        float64           `json:"agent_score"`
	CodingScore       float64           `json:"coding_score"`
	ReliabilityScore  float64           `json:"reliability_score"`
	DoctorCheckedAt   time.Time         `json:"doctor_checked_at,omitempty"`
	PDF               bool              `json:"pdf,omitempty"`
	ContextWindow     int               `json:"context_window,omitempty"`
	MaxOutputTokens   int               `json:"max_output_tokens,omitempty"`
	Status            string            `json:"status"`
	LatencyMS         int64             `json:"latency_ms,omitempty"`
	ProtocolStatus    map[string]string `json:"protocol_status,omitempty"`
	LastChecked       time.Time         `json:"last_checked,omitempty"`
	Error             string            `json:"error,omitempty"`
	Source            string            `json:"source,omitempty"`
}

type ProtocolProbe struct {
	APIType          string `json:"api_type"`
	Basic            bool   `json:"basic"`
	Streaming        bool   `json:"streaming"`
	SystemPrompt     bool   `json:"system_prompt,omitempty"`
	ToolCalling      bool   `json:"tool_calling"`
	ToolRoundTrip    bool   `json:"tool_round_trip,omitempty"`
	ParallelTools    bool   `json:"parallel_tools,omitempty"`
	StructuredOutput bool   `json:"structured_output,omitempty"`
	Vision           bool   `json:"vision,omitempty"`
	Reasoning        bool   `json:"reasoning,omitempty"`
	AgentCompatible  bool   `json:"agent_compatible,omitempty"`
	IdentityVerified bool   `json:"identity_verified,omitempty"`
	UpstreamModelID  string `json:"upstream_model_id,omitempty"`
	LatencyMS        int64  `json:"latency_ms"`
	Error            string `json:"error,omitempty"`
}

type ProbeResult struct {
	ModelID          string                   `json:"model_id"`
	Deep             bool                     `json:"deep"`
	Basic            bool                     `json:"basic"`
	Streaming        bool                     `json:"streaming"`
	SystemPrompt     bool                     `json:"system_prompt,omitempty"`
	ToolCalling      bool                     `json:"tool_calling"`
	ToolRoundTrip    bool                     `json:"tool_round_trip,omitempty"`
	ParallelTools    bool                     `json:"parallel_tools,omitempty"`
	StructuredOutput bool                     `json:"structured_output,omitempty"`
	Vision           bool                     `json:"vision,omitempty"`
	Reasoning        bool                     `json:"reasoning,omitempty"`
	AgentCompatible  bool                     `json:"agent_compatible,omitempty"`
	IdentityVerified bool                     `json:"identity_verified,omitempty"`
	UpstreamModelID  string                   `json:"upstream_model_id,omitempty"`
	DoctorStatus     string                   `json:"doctor_status,omitempty"`
	DoctorScore      float64                  `json:"doctor_score"`
	AgentScore       float64                  `json:"agent_score"`
	CodingScore      float64                  `json:"coding_score"`
	ReliabilityScore float64                  `json:"reliability_score"`
	LatencyMS        int64                    `json:"latency_ms"`
	SelectedProtocol string                   `json:"selected_protocol,omitempty"`
	Protocols        map[string]ProtocolProbe `json:"protocols,omitempty"`
	Error            string                   `json:"error,omitempty"`
}

type UpstreamResponse struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
}

type Manager struct {
	Secrets    *secretstore.Store
	Client     *http.Client
	CAFile     string
	gigaTokens *gigaTokenCache
	executors  *executorRegistry
}

func NewManager(secrets *secretstore.Store, caFile ...string) *Manager {
	m := &Manager{Secrets: secrets, Client: &http.Client{Timeout: 120 * time.Second}, gigaTokens: newGigaTokenCache()}
	m.executors = newExecutorRegistry(m)
	if len(caFile) > 0 {
		m.CAFile = caFile[0]
	}
	return m
}

func (m *Manager) gigaClient() *http.Client {
	roots, err := gigaCertPool(m.CAFile)
	if err != nil {
		// Fail safely: keep normal system validation rather than ever disabling TLS.
		return m.Client
	}
	var tr *http.Transport
	if base, ok := m.Client.Transport.(*http.Transport); ok && base != nil {
		tr = base.Clone()
	} else {
		base, _ := http.DefaultTransport.(*http.Transport)
		tr = base.Clone()
	}
	var tlsCfg *tls.Config
	if tr.TLSClientConfig != nil {
		tlsCfg = tr.TLSClientConfig.Clone()
	} else {
		tlsCfg = &tls.Config{}
	}
	tlsCfg.RootCAs = roots
	if tlsCfg.MinVersion < tls.VersionTLS12 {
		tlsCfg.MinVersion = tls.VersionTLS12
	}
	tr.TLSClientConfig = tlsCfg
	return &http.Client{Transport: tr, Timeout: m.Client.Timeout}
}

func (m *Manager) credential(p config.Provider) (string, error) {
	if strings.TrimSpace(p.CredentialRef) == "" {
		return "", nil
	}
	return m.Secrets.Get(p.CredentialRef)
}

func (m *Manager) ListModels(ctx context.Context, p config.Provider) ([]Model, error) {
	p = config.NormalizeProvider(p)
	return m.executors.resolve(p).ListModels(ctx, p)
}

func (m *Manager) Proxy(ctx context.Context, p config.Provider, apiType string, body []byte) (*UpstreamResponse, error) {
	p = config.NormalizeProvider(p)
	if apiType == "embeddings" {
		// Embeddings stay on the OpenAI-compatible lane for providers that advertise them.
		// Native account/Anthropic/GigaChat drivers are intentionally excluded.
		if p.Kind == "anthropic" || p.Kind == "gigachat" {
			return nil, ErrUnsupportedAPI
		}
		clean, err := sanitizeProviderRequest(p, apiType, body)
		if err != nil {
			return nil, err
		}
		p2 := p
		p2.BaseURL = config.ProtocolBaseURL(p, apiType)
		p2.AuthType = config.ProtocolAuthType(p, apiType)
		return m.proxyOpenAICompatible(ctx, p2, apiType, clean)
	}
	if !config.SupportsProtocol(p, apiType) {
		return nil, ErrUnsupportedAPI
	}
	return m.executors.resolve(p).Proxy(ctx, p, apiType, body)
}

func clearlyNonGenerationModel(id string) bool {
	x := strings.ToLower(strings.TrimSpace(id))
	patterns := []string{
		"embedding", "embeddings", "moderation", "rerank", "reranker",
		"dall-e", "gpt-image", "text-to-speech", "speech-to-text",
	}
	for _, p := range patterns {
		if strings.Contains(x, p) {
			return true
		}
	}
	if strings.HasPrefix(x, "tts-") || strings.HasPrefix(x, "whisper-") {
		return true
	}
	return false
}
