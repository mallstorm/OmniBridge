package mcpcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"omnibridge/internal/storage"
)

const Version = "1.0"

type Server struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Transport     string            `json:"transport"`
	URL           string            `json:"url,omitempty"`
	Command       string            `json:"command,omitempty"`
	Args          []string          `json:"args,omitempty"`
	EnvKeys       []string          `json:"env_keys,omitempty"`
	CredentialRef string            `json:"credential_ref,omitempty"`
	Enabled       bool              `json:"enabled"`
	AutoStart     bool              `json:"auto_start"`
	AllowProcess  bool              `json:"allow_process"`
	Status        string            `json:"status,omitempty"`
	LastError     string            `json:"last_error,omitempty"`
	LastChecked   time.Time         `json:"last_checked,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

type Control struct {
	mu      sync.RWMutex
	db      *storage.DB
	servers map[string]Server
	client  *http.Client
}

func New(db *storage.DB) *Control {
	c := &Control{db: db, servers: map[string]Server{}, client: &http.Client{Timeout: 5 * time.Second}}
	if db != nil {
		if rows, err := db.LoadMCPServers(); err == nil {
			for id, b := range rows {
				var s Server
				if json.Unmarshal(b, &s) == nil {
					if s.ID == "" {
						s.ID = id
					}
					c.servers[s.ID] = s
				}
			}
		}
	}
	return c
}

func cleanID(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	var b strings.Builder
	dash := false
	for _, r := range v {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func validate(s *Server) error {
	s.ID = cleanID(s.ID)
	if s.ID == "" {
		s.ID = cleanID(s.Name)
	}
	if s.ID == "" {
		return errors.New("mcp server id is required")
	}
	s.Transport = strings.ToLower(strings.TrimSpace(s.Transport))
	if s.Transport == "" {
		s.Transport = "http"
	}
	if s.Metadata == nil {
		s.Metadata = map[string]string{}
	}
	switch s.Transport {
	case "http", "streamable-http", "sse":
		u, err := url.Parse(strings.TrimSpace(s.URL))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("mcp http server requires a valid http/https URL")
		}
		s.URL = u.String()
		s.Command = ""
		s.Args = nil
		s.AllowProcess = false
	case "stdio":
		if strings.TrimSpace(s.Command) == "" {
			return errors.New("mcp stdio server requires command")
		}
		if s.AutoStart && !s.AllowProcess {
			return errors.New("stdio auto_start requires explicit allow_process=true")
		}
		s.URL = ""
	default:
		return errors.New("unsupported mcp transport")
	}
	return nil
}

func (c *Control) Upsert(s Server) (Server, error) {
	if err := validate(&s); err != nil {
		return Server{}, err
	}
	s.Status = "configured"
	s.LastError = ""
	b, _ := json.Marshal(s)
	if c.db != nil {
		if err := c.db.SaveMCPServer(s.ID, b); err != nil {
			return Server{}, err
		}
	}
	c.mu.Lock()
	c.servers[s.ID] = s
	c.mu.Unlock()
	return s, nil
}

func (c *Control) Delete(id string) error {
	id = cleanID(id)
	if id == "" {
		return errors.New("id required")
	}
	if c.db != nil {
		if err := c.db.DeleteMCPServer(id); err != nil {
			return err
		}
	}
	c.mu.Lock()
	delete(c.servers, id)
	c.mu.Unlock()
	return nil
}
func (c *Control) List() []Server {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Server, 0, len(c.servers))
	for _, s := range c.servers {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func (c *Control) Get(id string) (Server, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.servers[cleanID(id)]
	return s, ok
}

func (c *Control) Probe(ctx context.Context, id string) (Server, error) {
	s, ok := c.Get(id)
	if !ok {
		return Server{}, osErr("mcp server not found")
	}
	s.LastChecked = time.Now().UTC()
	s.LastError = ""
	s.Status = "configured"
	switch s.Transport {
	case "http", "streamable-http", "sse":
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, s.URL, nil)
		if err == nil {
			resp, e := c.client.Do(req)
			err = e
			if resp != nil {
				_ = resp.Body.Close()
				if resp.StatusCode < 500 {
					err = nil
				}
			}
		}
		if err != nil {
			s.Status = "unreachable"
			s.LastError = err.Error()
		} else {
			s.Status = "reachable"
		}
	case "stdio":
		if _, err := exec.LookPath(s.Command); err != nil {
			s.Status = "missing_command"
			s.LastError = err.Error()
		} else if s.AutoStart && !s.AllowProcess {
			s.Status = "blocked"
			s.LastError = "process launch is not allowed"
		} else {
			s.Status = "ready"
		}
	}
	b, _ := json.Marshal(s)
	if c.db != nil {
		_ = c.db.SaveMCPServer(s.ID, b)
	}
	c.mu.Lock()
	c.servers[s.ID] = s
	c.mu.Unlock()
	return s, nil
}

type controlError string

func (e controlError) Error() string { return string(e) }
func osErr(s string) error           { return controlError(s) }

func (c *Control) Snapshot() map[string]any {
	list := c.List()
	enabled := 0
	ready := 0
	for _, s := range list {
		if s.Enabled {
			enabled++
		}
		if s.Status == "reachable" || s.Status == "ready" {
			ready++
		}
	}
	return map[string]any{"version": Version, "servers": list, "count": len(list), "enabled": enabled, "ready": ready, "stdio_process_launch_default": false}
}
