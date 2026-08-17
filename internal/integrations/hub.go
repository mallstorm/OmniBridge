package integrations

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"omnibridge/internal/storage"
)

const Version = "1.0"

type Definition struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Kind          string   `json:"kind"`
	Commands      []string `json:"commands,omitempty"`
	AutoConfigure bool     `json:"auto_configure"`
	Note          string   `json:"note,omitempty"`
}

type State struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Kind           string    `json:"kind"`
	Detected       bool      `json:"detected"`
	DetectedPath   string    `json:"detected_path,omitempty"`
	Status         string    `json:"status"`
	AutoConfigure  bool      `json:"auto_configure"`
	Endpoint       string    `json:"endpoint"`
	ManagedProfile string    `json:"managed_profile,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
	Note           string    `json:"note,omitempty"`
}

type Plan struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Endpoint          string            `json:"endpoint"`
	APIKeyPlaceholder string            `json:"api_key_placeholder"`
	Environment       map[string]string `json:"environment"`
	Snippet           map[string]any    `json:"snippet"`
	SafeAutomatic     bool              `json:"safe_automatic"`
	Instructions      []string          `json:"instructions"`
}

var catalog = []Definition{
	{ID: "vscode", Name: "Visual Studio Code", Kind: "editor", Commands: []string{"code"}, AutoConfigure: true, Note: "Uses the existing OmniBridge VS Code Connector safe-merge path."},
	{ID: "cursor", Name: "Cursor", Kind: "editor", Commands: []string{"cursor"}, Note: "Expose OmniBridge as an OpenAI-compatible endpoint."},
	{ID: "continue", Name: "Continue", Kind: "extension", Note: "Generate an OpenAI-compatible provider profile; external config is not overwritten automatically."},
	{ID: "cline", Name: "Cline", Kind: "extension", Note: "Use OpenAI-compatible custom provider settings."},
	{ID: "roo-code", Name: "Roo Code", Kind: "extension", Note: "Use OpenAI-compatible custom provider settings."},
	{ID: "codex-cli", Name: "Codex CLI", Kind: "cli", Commands: []string{"codex"}, Note: "CLI detection only. OmniBridge does not replace official ChatGPT login credentials."},
	{ID: "claude-code", Name: "Claude Code", Kind: "cli", Commands: []string{"claude"}, Note: "Can consume compatible gateway settings when supported by the client version."},
	{ID: "opencode", Name: "OpenCode", Kind: "cli", Commands: []string{"opencode"}, Note: "Generate endpoint/environment plan without mutating user configuration."},
	{ID: "openclaw", Name: "OpenClaw", Kind: "agent", Commands: []string{"openclaw"}, Note: "Generate local gateway connection details; no unrelated OpenClaw settings are changed."},
}

func Catalog() []Definition { out := append([]Definition(nil), catalog...); return out }

type Hub struct {
	root string
	db   *storage.DB
}

func New(root string, db *storage.DB) *Hub { return &Hub{root: root, db: db} }

func detectCommand(names []string) string {
	for _, name := range names {
		if p, err := exec.LookPath(name); err == nil {
			if abs, e := filepath.Abs(p); e == nil {
				return abs
			}
			return p
		}
	}
	return ""
}

func windowsKnownPath(id string) string {
	if runtime.GOOS != "windows" {
		return ""
	}
	local := os.Getenv("LOCALAPPDATA")
	roaming := os.Getenv("APPDATA")
	candidates := map[string][]string{
		"vscode":   {filepath.Join(local, "Programs", "Microsoft VS Code", "Code.exe")},
		"cursor":   {filepath.Join(local, "Programs", "cursor", "Cursor.exe"), filepath.Join(local, "Programs", "Cursor", "Cursor.exe")},
		"openclaw": {filepath.Join(local, "OpenClawTray", "OpenClaw.Tray.WinUI.exe")},
		"continue": {filepath.Join(roaming, "Code", "User", "globalStorage", "continue.continue")},
		"cline":    {filepath.Join(roaming, "Code", "User", "globalStorage", "saoudrizwan.claude-dev")},
		"roo-code": {filepath.Join(roaming, "Code", "User", "globalStorage", "rooveterinaryinc.roo-cline")},
	}
	for _, p := range candidates[id] {
		if st, err := os.Stat(p); err == nil && (st.IsDir() || !st.IsDir()) {
			return p
		}
	}
	return ""
}

func (h *Hub) Detect(listen string) []State {
	endpoint := "http://" + listen + "/v1"
	persisted := map[string][]byte{}
	if h.db != nil {
		persisted, _ = h.db.LoadIntegrationStates()
	}
	out := make([]State, 0, len(catalog))
	for _, d := range catalog {
		path := detectCommand(d.Commands)
		if path == "" {
			path = windowsKnownPath(d.ID)
		}
		st := State{ID: d.ID, Name: d.Name, Kind: d.Kind, Detected: path != "", DetectedPath: path, Status: "not_detected", AutoConfigure: d.AutoConfigure, Endpoint: endpoint, UpdatedAt: time.Now().UTC(), Note: d.Note}
		if path != "" {
			st.Status = "detected"
		}
		if b := persisted[d.ID]; len(b) > 0 {
			var old State
			if json.Unmarshal(b, &old) == nil && old.ManagedProfile != "" {
				st.ManagedProfile = old.ManagedProfile
				if st.Detected {
					st.Status = "configured"
				}
			}
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func findDef(id string) (Definition, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, d := range catalog {
		if d.ID == id {
			return d, true
		}
	}
	return Definition{}, false
}

func (h *Hub) Plan(id, listen string) (Plan, bool) {
	d, ok := findDef(id)
	if !ok {
		return Plan{}, false
	}
	endpoint := "http://" + listen + "/v1"
	p := Plan{ID: d.ID, Name: d.Name, Endpoint: endpoint, APIKeyPlaceholder: "${OMNIBRIDGE_API_KEY}", Environment: map[string]string{"OPENAI_BASE_URL": endpoint, "OPENAI_API_KEY": "${OMNIBRIDGE_API_KEY}"}, SafeAutomatic: d.AutoConfigure, Instructions: []string{"Use the OmniBridge local /v1 endpoint.", "Use the local bridge key from the Integration page; do not copy upstream provider keys into the client.", "Select an explicit model or one of the auto/* virtual models."}}
	p.Snippet = map[string]any{"provider": "openai-compatible", "baseURL": endpoint, "apiKey": "${OMNIBRIDGE_API_KEY}", "model": "auto/smart"}
	if d.ID == "codex-cli" {
		p.Instructions = []string{"Codex CLI account authentication remains owned by the official Codex client.", "Use this plan only for workflows that explicitly support a custom OpenAI-compatible base URL."}
		p.SafeAutomatic = false
	}
	return p, true
}

func (h *Hub) MarkConfigured(id, profile, listen string) (State, error) {
	d, ok := findDef(id)
	if !ok {
		return State{}, os.ErrNotExist
	}
	st := State{ID: d.ID, Name: d.Name, Kind: d.Kind, Status: "configured", AutoConfigure: d.AutoConfigure, Endpoint: "http://" + listen + "/v1", ManagedProfile: strings.TrimSpace(profile), UpdatedAt: time.Now().UTC(), Note: d.Note}
	if st.ManagedProfile == "" {
		st.ManagedProfile = "omnibridge"
	}
	if h.db != nil {
		b, _ := json.Marshal(st)
		if err := h.db.SaveIntegrationState(id, b); err != nil {
			return State{}, err
		}
	}
	return st, nil
}
