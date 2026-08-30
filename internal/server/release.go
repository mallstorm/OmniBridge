package server

import (
	"net"
	"net/http"
	"strings"

	"omnibridge/internal/config"
	"omnibridge/internal/releasecheck"
	"omnibridge/internal/storage"
)

func (s *Server) releaseReport() map[string]any {
	checks := []map[string]any{}
	ok := true
	add := func(name string, passed bool, detail string) {
		c := map[string]any{"name": name, "ok": passed}
		if detail != "" {
			c["detail"] = detail
		}
		checks = append(checks, c)
		if !passed {
			ok = false
		}
	}

	if s.Storage == nil {
		add("sqlite_quick_check", false, "storage is not attached")
	} else if q, err := s.Storage.QuickCheck(); err != nil {
		add("sqlite_quick_check", false, err.Error())
	} else {
		add("sqlite_quick_check", q == "ok", q)
		add("sqlite_schema", storage.CurrentSchemaVersion == 4, "schema=4")
	}

	cfg := s.Config.Get()
	add("config_schema", cfg.SchemaVersion == config.CurrentSchemaVersion, "schema="+itoa(cfg.SchemaVersion))
	host, _, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		host = cfg.Listen
	}
	loopback := strings.EqualFold(host, "localhost")
	if ip := net.ParseIP(host); ip != nil {
		loopback = ip.IsLoopback()
	}
	remoteSafe := loopback || (cfg.Remote.Enabled && strings.TrimSpace(cfg.Remote.TLSCertPath) != "" && strings.TrimSpace(cfg.Remote.TLSKeyPath) != "" && len(cfg.Remote.AllowedCIDRs) > 0)
	add("network_bind_policy", remoteSafe, cfg.Listen)

	if strings.TrimSpace(s.Root) == "" {
		add("release_package_integrity", false, "application root is not set")
	} else {
		r := releasecheck.VerifyRuntime(s.Root, config.CurrentVersion)
		add("release_package_integrity", r.OK, summarizeReleaseChecks(r))
	}

	return map[string]any{
		"ok":            ok,
		"version":       config.CurrentVersion,
		"channel":       "rc",
		"config_schema": config.CurrentSchemaVersion,
		"db_schema":     storage.CurrentSchemaVersion,
		"checks":        checks,
	}
}

func (s *Server) readiness(w http.ResponseWriter, r *http.Request) {
	rep := s.releaseReport()
	status := http.StatusOK
	if ok, _ := rep["ok"].(bool); !ok {
		status = http.StatusServiceUnavailable
	}
	jsonOut(w, status, rep)
}

func (s *Server) releaseReadiness(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, http.StatusOK, s.releaseReport())
}

func summarizeReleaseChecks(r releasecheck.Report) string {
	bad := []string{}
	for _, c := range r.Checks {
		if !c.OK {
			bad = append(bad, c.Name+": "+c.Detail)
		}
	}
	if len(bad) == 0 {
		return "manifest/version/runtime layout verified"
	}
	return strings.Join(bad, "; ")
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	b := [32]byte{}
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
