package providerdefs

import (
	"embed"
	"encoding/json"
	"sort"
	"strings"
	"sync"
)

//go:embed catalog.json
var catalogFS embed.FS

type RequestPolicy struct {
	Remove []string          `json:"remove,omitempty"`
	Rename map[string]string `json:"rename,omitempty"`
}

type Definition struct {
	ID                string                   `json:"id"`
	Name              string                   `json:"name"`
	Category          string                   `json:"category"`
	BaseURL           string                   `json:"base_url,omitempty"`
	ModelsURL         string                   `json:"models_url,omitempty"`
	AuthType          string                   `json:"auth_type,omitempty"`
	Protocols         []string                 `json:"protocols"`
	PreferredProtocol string                   `json:"preferred_protocol"`
	ProtocolBaseURLs  map[string]string        `json:"protocol_base_urls,omitempty"`
	ProtocolAuthTypes map[string]string        `json:"protocol_auth_types,omitempty"`
	Headers           map[string]string        `json:"headers,omitempty"`
	Quirks            map[string]string        `json:"quirks,omitempty"`
	APIKeyOptional    bool                     `json:"api_key_optional,omitempty"`
	RequiresBaseURL   bool                     `json:"requires_base_url,omitempty"`
	Notes             string                   `json:"notes,omitempty"`
	Executor          string                   `json:"executor"`
	SupportLevel      string                   `json:"support_level"`
	DetectionHosts    []string                 `json:"detection_hosts,omitempty"`
	KeyPrefixes       []string                 `json:"key_prefixes,omitempty"`
	RequestPolicies   map[string]RequestPolicy `json:"request_policies,omitempty"`
}

var (
	once    sync.Once
	catalog []Definition
	byID    map[string]Definition
	loadErr error
)

func load() {
	b, err := catalogFS.ReadFile("catalog.json")
	if err != nil {
		loadErr = err
		return
	}
	if err := json.Unmarshal(b, &catalog); err != nil {
		loadErr = err
		return
	}
	byID = make(map[string]Definition, len(catalog))
	for i := range catalog {
		d := normalize(catalog[i])
		catalog[i] = d
		if d.ID != "" {
			byID[d.ID] = d
		}
	}
	sort.Slice(catalog, func(i, j int) bool {
		if catalog[i].Category == catalog[j].Category {
			return catalog[i].Name < catalog[j].Name
		}
		return catalog[i].Category < catalog[j].Category
	})
}

func normalize(d Definition) Definition {
	d.ID = strings.ToLower(strings.TrimSpace(d.ID))
	d.Executor = strings.ToLower(strings.TrimSpace(d.Executor))
	if d.Executor == "" {
		d.Executor = "openai-compatible"
	}
	d.SupportLevel = strings.ToLower(strings.TrimSpace(d.SupportLevel))
	if d.SupportLevel == "" {
		d.SupportLevel = "preset"
	}
	d.BaseURL = strings.TrimRight(strings.TrimSpace(d.BaseURL), "/")
	d.ModelsURL = strings.TrimSpace(d.ModelsURL)
	if d.AuthType == "" {
		d.AuthType = "bearer"
	}
	if len(d.Protocols) == 0 {
		d.Protocols = []string{"chat-completions"}
	}
	if d.PreferredProtocol == "" {
		d.PreferredProtocol = d.Protocols[0]
	}
	if d.ProtocolBaseURLs == nil {
		d.ProtocolBaseURLs = map[string]string{}
	}
	if d.ProtocolAuthTypes == nil {
		d.ProtocolAuthTypes = map[string]string{}
	}
	if d.Headers == nil {
		d.Headers = map[string]string{}
	}
	if d.Quirks == nil {
		d.Quirks = map[string]string{}
	}
	if d.RequestPolicies == nil {
		d.RequestPolicies = map[string]RequestPolicy{}
	}
	return d
}

func All() ([]Definition, error) {
	once.Do(load)
	if loadErr != nil {
		return nil, loadErr
	}
	out := make([]Definition, len(catalog))
	for i, d := range catalog {
		out[i] = Clone(d)
	}
	return out, nil
}

func ByID(id string) (Definition, bool) {
	once.Do(load)
	if loadErr != nil {
		return Definition{}, false
	}
	d, ok := byID[strings.ToLower(strings.TrimSpace(id))]
	return Clone(d), ok
}

func Detect(baseURL, key string) string {
	once.Do(load)
	base := strings.ToLower(strings.TrimSpace(baseURL))
	for _, d := range catalog {
		for _, host := range d.DetectionHosts {
			if host != "" && strings.Contains(base, strings.ToLower(host)) {
				return d.ID
			}
		}
	}
	key = strings.TrimSpace(key)
	bestID, bestLen := "", -1
	for _, d := range catalog {
		for _, prefix := range d.KeyPrefixes {
			if prefix != "" && strings.HasPrefix(key, prefix) && len(prefix) > bestLen {
				bestID, bestLen = d.ID, len(prefix)
			}
		}
	}
	if bestID != "" {
		return bestID
	}
	return "custom"
}

func Clone(d Definition) Definition {
	d.Protocols = append([]string(nil), d.Protocols...)
	d.DetectionHosts = append([]string(nil), d.DetectionHosts...)
	d.KeyPrefixes = append([]string(nil), d.KeyPrefixes...)
	d.ProtocolBaseURLs = cloneMap(d.ProtocolBaseURLs)
	d.ProtocolAuthTypes = cloneMap(d.ProtocolAuthTypes)
	d.Headers = cloneMap(d.Headers)
	d.Quirks = cloneMap(d.Quirks)
	if d.RequestPolicies != nil {
		rp := make(map[string]RequestPolicy, len(d.RequestPolicies))
		for k, v := range d.RequestPolicies {
			v.Remove = append([]string(nil), v.Remove...)
			v.Rename = cloneMap(v.Rename)
			rp[k] = v
		}
		d.RequestPolicies = rp
	}
	return d
}

func cloneMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
