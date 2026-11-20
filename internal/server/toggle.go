package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

type enabledRequest struct {
	Enabled bool `json:"enabled"`
}

// setProviderEnabled switches a provider on or off at runtime. The registry
// is refreshed in the background so model lists follow the change.
func (s *Server) setProviderEnabled(w http.ResponseWriter, r *http.Request) {
	var in enabledRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if err := s.Config.SetProviderEnabled(r.PathValue("id"), in.Enabled); err != nil {
		jsonErr(w, 404, err.Error())
		return
	}
	s.refreshAsync()
	jsonOut(w, 200, map[string]any{"id": r.PathValue("id"), "enabled": in.Enabled})
}

// setAccountEnabled switches an account connector on or off without logout.
func (s *Server) setAccountEnabled(w http.ResponseWriter, r *http.Request) {
	var in enabledRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if err := s.Config.SetAccountEnabled(r.PathValue("id"), in.Enabled); err != nil {
		jsonErr(w, 404, err.Error())
		return
	}
	s.refreshAsync()
	jsonOut(w, 200, map[string]any{"id": r.PathValue("id"), "enabled": in.Enabled})
}

func (s *Server) refreshAsync() {
	s.refreshes.Add(1)
	go func() {
		defer s.refreshes.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		s.BackgroundRefresh(ctx)
	}()
}
