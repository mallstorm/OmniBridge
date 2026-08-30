package modelcatalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"omnibridge/internal/config"
	"omnibridge/internal/storage"
)

const Version = "1.0"
const snapshotDocument = "models-dev-catalog.json"

type Entry struct {
	ProviderID          string  `json:"provider_id"`
	ModelID             string  `json:"model_id"`
	Name                string  `json:"name,omitempty"`
	ContextWindow       int     `json:"context_window,omitempty"`
	MaxOutputTokens     int     `json:"max_output_tokens,omitempty"`
	ToolCalling         bool    `json:"declared_tool_calling,omitempty"`
	InputUSDPerMillion  float64 `json:"input_usd_per_million,omitempty"`
	OutputUSDPerMillion float64 `json:"output_usd_per_million,omitempty"`
	Source              string  `json:"source"`
	Confidence          string  `json:"confidence"`
}
type Snapshot struct {
	Version       string    `json:"version"`
	SourceURL     string    `json:"source_url"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
	Hash          string    `json:"hash,omitempty"`
	Entries       []Entry   `json:"entries"`
	LastError     string    `json:"last_error,omitempty"`
	ReferenceOnly bool      `json:"reference_only"`
}
type Service struct {
	mu       sync.RWMutex
	cfg      config.CatalogSyncConfig
	client   *http.Client
	db       *storage.DB
	snapshot Snapshot
}

func validateCatalogURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Host == "" {
		return nil, errors.New("catalog URL must be an absolute HTTPS URL")
	}
	if u.User != nil {
		return nil, errors.New("catalog URL must not contain userinfo")
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || !strings.Contains(host, ".") {
		return nil, errors.New("catalog URL must use a public hostname")
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return nil, errors.New("catalog URL must not target a local or private address")
		}
	}
	return u, nil
}

func catalogHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("catalog redirect limit exceeded")
			}
			_, err := validateCatalogURL(req.URL.String())
			return err
		},
	}
}

func New(db *storage.DB, cfg config.CatalogSyncConfig) *Service {
	s := &Service{cfg: cfg, db: db, client: catalogHTTPClient(), snapshot: Snapshot{Version: Version, SourceURL: cfg.URL, Entries: []Entry{}, ReferenceOnly: true}}
	if db == nil {
		return s
	}
	raw, ok, err := db.GetDocument(snapshotDocument)
	if err != nil || !ok {
		return s
	}
	var saved Snapshot
	if json.Unmarshal(raw, &saved) != nil || !saved.ReferenceOnly || len(saved.Entries) == 0 {
		return s
	}
	// Preserve SourceURL from the persisted snapshot: it identifies the source
	// of the actual last-known-good data even if the configured URL has changed.
	saved.Version = Version
	saved.LastError = ""
	s.snapshot = saved
	return s
}
func (s *Service) UpdateConfig(c config.CatalogSyncConfig) {
	s.mu.Lock()
	s.cfg = c
	s.mu.Unlock()
}
func (s *Service) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := s.snapshot
	out.Entries = append([]Entry(nil), out.Entries...)
	return out
}
func number(m map[string]any, keys ...string) int {
	for _, k := range keys {
		if v, ok := m[k].(float64); ok {
			return int(v)
		}
	}
	return 0
}
func money(m map[string]any, key string) float64 {
	cost, _ := m["cost"].(map[string]any)
	if v, ok := cost[key].(float64); ok {
		return v * 1e6
	}
	return 0
}
func parse(raw []byte) []Entry {
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		return nil
	}
	out := []Entry{}
	for providerID, pv := range root {
		pm, ok := pv.(map[string]any)
		if !ok {
			continue
		}
		models, _ := pm["models"].(map[string]any)
		for modelID, mv := range models {
			mm, ok := mv.(map[string]any)
			if !ok {
				continue
			}
			name, _ := mm["name"].(string)
			tools, _ := mm["tool_call"].(bool)
			out = append(out, Entry{ProviderID: providerID, ModelID: modelID, Name: name, ContextWindow: number(mm, "limit", "context", "context_window"), MaxOutputTokens: number(mm, "output", "max_output_tokens"), ToolCalling: tools, InputUSDPerMillion: money(mm, "input"), OutputUSDPerMillion: money(mm, "output"), Source: "models.dev", Confidence: "declared"})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProviderID != out[j].ProviderID {
			return out[i].ProviderID < out[j].ProviderID
		}
		return out[i].ModelID < out[j].ModelID
	})
	return out
}
func (s *Service) Sync(ctx context.Context) (Snapshot, error) {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()
	if !cfg.Enabled {
		return s.Snapshot(), errors.New("catalog sync is disabled")
	}
	u, err := validateCatalogURL(cfg.URL)
	if err != nil {
		return s.Snapshot(), err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return s.Snapshot(), err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		s.setError(err)
		return s.Snapshot(), err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		err = errors.New("catalog returned non-success status")
		s.setError(err)
		return s.Snapshot(), err
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return s.Snapshot(), err
	}
	entries := parse(raw)
	if len(entries) == 0 {
		err = errors.New("catalog has no recognizable model entries")
		s.setError(err)
		return s.Snapshot(), err
	}
	h := sha256.Sum256(raw)
	snap := Snapshot{Version: Version, SourceURL: cfg.URL, UpdatedAt: time.Now().UTC(), Hash: hex.EncodeToString(h[:]), Entries: entries, ReferenceOnly: true}
	s.mu.Lock()
	s.snapshot = snap
	s.mu.Unlock()
	if s.db != nil {
		b, _ := json.Marshal(snap)
		_ = s.db.PutDocument(snapshotDocument, b)
	}
	return snap, nil
}
func (s *Service) setError(err error) { s.mu.Lock(); s.snapshot.LastError = err.Error(); s.mu.Unlock() }
