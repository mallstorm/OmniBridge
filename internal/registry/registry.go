package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"omnibridge/internal/accounts"
	"omnibridge/internal/config"
	"omnibridge/internal/providers"
	"omnibridge/internal/storage"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type Snapshot struct {
	UpdatedAt time.Time         `json:"updated_at"`
	Models    []providers.Model `json:"models"`
}

type Registry struct {
	path string
	db   *storage.DB
	cfg  *config.Store
	pm   *providers.Manager
	am   *accounts.Manager
	mu   sync.RWMutex
	snap Snapshot
}

func New(path string, cfg *config.Store, pm *providers.Manager, am *accounts.Manager) *Registry {
	return newRegistry(path, nil, cfg, pm, am)
}

func NewPersistent(path string, db *storage.DB, cfg *config.Store, pm *providers.Manager, am *accounts.Manager) *Registry {
	return newRegistry(path, db, cfg, pm, am)
}

func newRegistry(path string, db *storage.DB, cfg *config.Store, pm *providers.Manager, am *accounts.Manager) *Registry {
	r := &Registry{path: path, db: db, cfg: cfg, pm: pm, am: am}
	_ = r.load()
	return r
}

func (r *Registry) load() error {
	var b []byte
	var err error
	if r.db != nil {
		if persisted, ok, dbErr := r.db.GetDocument("model-registry.json"); dbErr != nil {
			return dbErr
		} else if ok {
			b = persisted
		}
	}
	if len(b) == 0 {
		b, err = os.ReadFile(r.path)
		if err != nil {
			return err
		}
	}
	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	r.mu.Lock()
	r.snap = s
	r.mu.Unlock()
	return nil
}
func (r *Registry) save() error {
	r.mu.RLock()
	s := r.snap
	r.mu.RUnlock()
	if err := os.MkdirAll(filepath.Dir(r.path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if r.db != nil {
		records := make([]storage.ModelRecord, 0, len(s.Models))
		for _, m := range s.Models {
			mb, _ := json.Marshal(m)
			records = append(records, storage.ModelRecord{ID: m.ID, ProviderID: m.ProviderID, NativeID: m.NativeID, Name: m.Name, Source: m.Source, Status: m.Status, DoctorStatus: m.DoctorStatus, PreferredProtocol: m.PreferredProtocol, AgentReady: m.AgentReady, LatencyMS: m.LatencyMS, LastChecked: m.LastChecked, Payload: mb})
		}
		if err := r.db.ReplaceModels(records, b); err != nil {
			return err
		}
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

func mergeDoctorState(dst *providers.Model, old providers.Model) {
	if dst == nil || !old.DoctorVerified {
		return
	}
	dst.ToolCalling = old.ToolCalling
	dst.ParallelTools = old.ParallelTools
	dst.StructuredOutput = old.StructuredOutput
	dst.Reasoning = old.Reasoning
	dst.Streaming = old.Streaming
	dst.Vision = old.Vision
	dst.SystemPrompt = old.SystemPrompt
	dst.AgentReady = old.AgentReady
	dst.ChatVerified = old.ChatVerified
	dst.IdentityVerified = old.IdentityVerified
	dst.UpstreamModelID = old.UpstreamModelID
	dst.DoctorStatus = old.DoctorStatus
	dst.DoctorVerified = old.DoctorVerified
	dst.DoctorScore = old.DoctorScore
	dst.AgentScore = old.AgentScore
	dst.CodingScore = old.CodingScore
	dst.ReliabilityScore = old.ReliabilityScore
	dst.DoctorCheckedAt = old.DoctorCheckedAt
	if old.PreferredProtocol != "" {
		dst.PreferredProtocol = old.PreferredProtocol
		dst.APIType = old.APIType
	}
	if len(old.ProtocolStatus) > 0 {
		dst.ProtocolStatus = make(map[string]string, len(old.ProtocolStatus))
		for k, v := range old.ProtocolStatus {
			dst.ProtocolStatus[k] = v
		}
	}
	if old.ContextWindow > 0 && dst.ContextWindow <= 0 {
		dst.ContextWindow = old.ContextWindow
	}
	if old.MaxOutputTokens > 0 && dst.MaxOutputTokens <= 0 {
		dst.MaxOutputTokens = old.MaxOutputTokens
	}
}

func (r *Registry) RefreshAll(ctx context.Context) Snapshot {
	cfg := r.cfg.Get()
	old := r.Models()
	type result struct {
		id     string
		models []providers.Model
		err    error
	}
	active := 0
	ch := make(chan result, len(cfg.Providers)+len(cfg.Accounts))
	discoverySlots := make(chan struct{}, 4)
	for _, p := range cfg.Providers {
		if !p.Enabled {
			continue
		}
		active++
		go func(p config.Provider) {
			res := result{id: p.ID}
			defer func() {
				if recovered := recover(); recovered != nil {
					res.err = fmt.Errorf("provider discovery recovered from panic: %v", recovered)
				}
				ch <- res
			}()
			select {
			case discoverySlots <- struct{}{}:
				defer func() { <-discoverySlots }()
			case <-ctx.Done():
				res.err = ctx.Err()
				return
			}
			pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			res.models, res.err = r.pm.ListModels(pctx, p)
			for i := range res.models {
				if res.models[i].Source == "" {
					res.models[i].Source = "api"
				}
			}
		}(p)
	}
	if r.am != nil {
		for _, a := range cfg.Accounts {
			if !a.Enabled {
				continue
			}
			active++
			go func(a config.AccountConnection) {
				res := result{id: a.ID}
				defer func() {
					if recovered := recover(); recovered != nil {
						res.err = fmt.Errorf("account discovery recovered from panic: %v", recovered)
					}
					ch <- res
				}()
				select {
				case discoverySlots <- struct{}{}:
					defer func() { <-discoverySlots }()
				case <-ctx.Done():
					res.err = ctx.Err()
					return
				}
				actx, cancel := context.WithTimeout(ctx, 45*time.Second)
				defer cancel()
				res.models, res.err = r.am.ListModels(actx, a)
				for i := range res.models {
					res.models[i].Source = "account"
				}
			}(a)
		}
	}
	all := []providers.Model{}
	for i := 0; i < active; i++ {
		res := <-ch
		if res.err != nil {
			for _, m := range old {
				if m.ProviderID == res.id {
					m.Status = "error"
					m.Error = res.err.Error()
					m.LastChecked = time.Now()
					all = append(all, m)
				}
			}
			continue
		}
		now := time.Now()
		for i := range res.models {
			res.models[i].LastChecked = now
		}
		all = append(all, res.models...)
	}
	oldByID := make(map[string]providers.Model, len(old))
	for _, m := range old {
		oldByID[m.ID] = m
	}
	for i := range all {
		if prior, ok := oldByID[all[i].ID]; ok {
			mergeDoctorState(&all[i], prior)
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].ProviderID == all[j].ProviderID {
			return all[i].NativeID < all[j].NativeID
		}
		return all[i].ProviderID < all[j].ProviderID
	})
	r.mu.Lock()
	r.snap = Snapshot{UpdatedAt: time.Now(), Models: all}
	out := r.snap
	r.mu.Unlock()
	_ = r.save()
	return out
}

func (r *Registry) Models() []providers.Model {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]providers.Model, len(r.snap.Models))
	copy(out, r.snap.Models)
	return out
}
func (r *Registry) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	b, _ := json.Marshal(r.snap)
	var s Snapshot
	_ = json.Unmarshal(b, &s)
	return s
}

func (r *Registry) Find(id string) (providers.Model, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, m := range r.snap.Models {
		if m.ID == id {
			return m, nil
		}
	}
	return providers.Model{}, errors.New("model not found")
}

func (r *Registry) UpdateProbe(modelID string, res providers.ProbeResult) {
	r.mu.Lock()
	for i := range r.snap.Models {
		if r.snap.Models[i].ID == modelID {
			r.snap.Models[i].ChatVerified = res.Basic
			r.snap.Models[i].IdentityVerified = res.IdentityVerified
			r.snap.Models[i].UpstreamModelID = res.UpstreamModelID
			if res.Deep {
				providers.ApplyDoctorScores(&res)
				r.snap.Models[i].DoctorVerified = true
				r.snap.Models[i].DoctorScore = res.DoctorScore
				r.snap.Models[i].AgentScore = res.AgentScore
				r.snap.Models[i].CodingScore = res.CodingScore
				r.snap.Models[i].ReliabilityScore = res.ReliabilityScore
				r.snap.Models[i].DoctorCheckedAt = time.Now()
				r.snap.Models[i].ToolCalling = res.ToolCalling
				r.snap.Models[i].Streaming = res.Streaming
				r.snap.Models[i].SystemPrompt = res.SystemPrompt
				r.snap.Models[i].ParallelTools = res.ParallelTools
				r.snap.Models[i].StructuredOutput = res.StructuredOutput
				r.snap.Models[i].Vision = res.Vision
				r.snap.Models[i].Reasoning = res.Reasoning
				r.snap.Models[i].AgentReady = res.AgentCompatible
				r.snap.Models[i].DoctorStatus = res.DoctorStatus
			}
			if res.SelectedProtocol != "" {
				r.snap.Models[i].PreferredProtocol = res.SelectedProtocol
				r.snap.Models[i].APIType = res.SelectedProtocol
			}
			if len(res.Protocols) > 0 {
				r.snap.Models[i].ProtocolStatus = map[string]string{}
				for k, pr := range res.Protocols {
					status := "error"
					if pr.Basic {
						status = "chat"
					}
					if pr.Basic && pr.Streaming {
						status = "stream"
					}
					if pr.AgentCompatible {
						status = "agent_ready"
					} else if pr.Basic && pr.Streaming && pr.ToolCalling {
						status = "agent_partial"
					}
					r.snap.Models[i].ProtocolStatus[k] = status
				}
			}
			r.snap.Models[i].LatencyMS = res.LatencyMS
			r.snap.Models[i].LastChecked = time.Now()
			if res.Basic {
				r.snap.Models[i].Status = "ready"
				if res.DoctorStatus != "" {
					r.snap.Models[i].DoctorStatus = res.DoctorStatus
				}
				r.snap.Models[i].Error = ""
			} else {
				r.snap.Models[i].Status = "error"
				r.snap.Models[i].Error = res.Error
			}
			break
		}
	}
	r.mu.Unlock()
	_ = r.save()
}

// ResetProviderVerification prevents a changed key, scope, endpoint or project
// from inheriting Doctor evidence collected for an older connection.
func (r *Registry) ResetProviderVerification(providerID string) {
	r.mu.Lock()
	for i := range r.snap.Models {
		if r.snap.Models[i].ProviderID != providerID {
			continue
		}
		r.snap.Models[i].ChatVerified = false
		r.snap.Models[i].IdentityVerified = false
		r.snap.Models[i].UpstreamModelID = ""
		r.snap.Models[i].DoctorVerified = false
		r.snap.Models[i].DoctorStatus = ""
		r.snap.Models[i].AgentReady = false
		r.snap.Models[i].DoctorScore = 0
		r.snap.Models[i].AgentScore = 0
		r.snap.Models[i].CodingScore = 0
		r.snap.Models[i].ReliabilityScore = 0
		r.snap.Models[i].DoctorCheckedAt = time.Time{}
	}
	r.mu.Unlock()
	_ = r.save()
}
