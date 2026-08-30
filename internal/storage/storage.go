package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const CurrentSchemaVersion = 4

type DB struct {
	mu   sync.Mutex
	conn nativeConn
	path string
}

type Row map[string]any

func Open(path string) (*DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("sqlite path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	existed := fileExists(path)
	conn, err := nativeOpen(path)
	if err != nil {
		return nil, err
	}
	db := &DB{conn: conn, path: path}
	if err := db.execUnlocked("PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON; PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL;"); err != nil {
		_ = conn.close()
		return nil, fmt.Errorf("sqlite pragmas: %w", err)
	}
	if err := db.migrate(existed); err != nil {
		_ = conn.close()
		return nil, err
	}
	return db, nil
}

func (d *DB) Path() string { return d.path }

func (d *DB) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn == nil {
		return nil
	}
	err := d.conn.close()
	d.conn = nil
	return err
}

func (d *DB) Exec(sql string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.execUnlocked(sql)
}

func (d *DB) execUnlocked(sql string) error {
	if d.conn == nil {
		return errors.New("sqlite database is closed")
	}
	return d.conn.exec(sql)
}

func (d *DB) Query(sql string) ([]Row, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn == nil {
		return nil, errors.New("sqlite database is closed")
	}
	return d.conn.query(sql)
}

func (d *DB) Transaction(statements ...string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn == nil {
		return errors.New("sqlite database is closed")
	}
	if err := d.conn.exec("BEGIN IMMEDIATE;"); err != nil {
		return err
	}
	for _, stmt := range statements {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if err := d.conn.exec(stmt); err != nil {
			_ = d.conn.exec("ROLLBACK;")
			return err
		}
	}
	if err := d.conn.exec("COMMIT;"); err != nil {
		_ = d.conn.exec("ROLLBACK;")
		return err
	}
	return nil
}

func quoteText(s string) string {
	s = strings.ReplaceAll(s, "\x00", "")
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func quoteTime(t time.Time) string {
	if t.IsZero() {
		return "NULL"
	}
	return quoteText(t.UTC().Format(time.RFC3339Nano))
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func (d *DB) userVersion() (int, error) {
	rows, err := d.conn.query("PRAGMA user_version;")
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	for _, v := range rows[0] {
		switch x := v.(type) {
		case int64:
			return int(x), nil
		case string:
			n, _ := strconv.Atoi(x)
			return n, nil
		}
	}
	return 0, nil
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o600)
}

func (d *DB) migrate(existed bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	v, err := d.userVersion()
	if err != nil {
		return fmt.Errorf("read sqlite schema version: %w", err)
	}
	if v > CurrentSchemaVersion {
		return fmt.Errorf("omnibridge.db schema %d is newer than supported %d", v, CurrentSchemaVersion)
	}
	if v == CurrentSchemaVersion {
		return nil
	}
	if existed {
		_ = d.conn.exec("PRAGMA wal_checkpoint(TRUNCATE);")
		backup := fmt.Sprintf("%s.schema%d.%s.bak", d.path, v, time.Now().UTC().Format("20060102T150405Z"))
		if err := copyFile(d.path, backup); err != nil {
			return fmt.Errorf("backup sqlite before migration: %w", err)
		}
	}
	for next := v + 1; next <= CurrentSchemaVersion; next++ {
		stmts, ok := migrations[next]
		if !ok {
			return fmt.Errorf("missing sqlite migration %d", next)
		}
		script := []string{"BEGIN IMMEDIATE;"}
		script = append(script, stmts...)
		script = append(script,
			fmt.Sprintf("INSERT INTO schema_migrations(version, applied_at) VALUES(%d, %s);", next, quoteText(time.Now().UTC().Format(time.RFC3339Nano))),
			fmt.Sprintf("PRAGMA user_version=%d;", next),
			"COMMIT;",
		)
		if err := d.conn.exec(strings.Join(script, "\n")); err != nil {
			_ = d.conn.exec("ROLLBACK;")
			return fmt.Errorf("apply sqlite migration %d: %w", next, err)
		}
	}
	return nil
}

// PutDocument stores a compatibility document transactionally. The normalized
// domain tables are updated by the dedicated Sync* methods below.
func (d *DB) PutDocument(name string, payload []byte) error {
	if !json.Valid(payload) {
		return fmt.Errorf("document %s is not valid json", name)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return d.Transaction(fmt.Sprintf(`INSERT INTO legacy_documents(name,payload,updated_at) VALUES(%s,%s,%s)
ON CONFLICT(name) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at;`, quoteText(name), quoteText(string(payload)), quoteText(now)))
}

func (d *DB) SaveRoutingQuality(provider, connection, model string, at time.Time, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return d.Exec(fmt.Sprintf(`INSERT INTO routing_quality(provider_id,connection_id,model_id,updated_at,payload) VALUES(%s,%s,%s,%s,%s)
ON CONFLICT(provider_id,connection_id,model_id) DO UPDATE SET updated_at=excluded.updated_at,payload=excluded.payload;`, quoteText(provider), quoteText(connection), quoteText(model), quoteTime(at), quoteText(string(b))))
}

func (d *DB) SaveRouteDecision(traceID string, at time.Time, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return d.Exec(fmt.Sprintf(`INSERT INTO route_decisions(trace_id,at,payload) VALUES(%s,%s,%s)
ON CONFLICT(trace_id) DO UPDATE SET at=excluded.at,payload=excluded.payload;`, quoteText(traceID), quoteTime(at), quoteText(string(b))))
}

func (d *DB) GetDocument(name string) ([]byte, bool, error) {
	rows, err := d.Query("SELECT payload FROM legacy_documents WHERE name=" + quoteText(name) + " LIMIT 1;")
	if err != nil {
		return nil, false, err
	}
	if len(rows) == 0 {
		return nil, false, nil
	}
	v, ok := rows[0]["payload"]
	if !ok {
		return nil, false, nil
	}
	s := fmt.Sprint(v)
	return []byte(s), true, nil
}

// SyncConfigJSON mirrors public configuration metadata into normalized tables.
// Secrets are intentionally absent because config.json is already secret-free.
func (d *DB) SyncConfigJSON(payload []byte) error {
	if !json.Valid(payload) {
		return errors.New("config payload is invalid json")
	}
	var c struct {
		Providers []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Kind    string `json:"kind"`
			BaseURL string `json:"base_url"`
			APIType string `json:"api_type"`
			Enabled bool   `json:"enabled"`
		} `json:"providers"`
		Accounts []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			Kind          string `json:"kind"`
			Enabled       bool   `json:"enabled"`
			Status        string `json:"status"`
			LastErrorKind string `json:"last_error_kind"`
		} `json:"accounts"`
		Routing json.RawMessage `json:"routing"`
	}
	if err := json.Unmarshal(payload, &c); err != nil {
		return err
	}
	stmts := []string{"DELETE FROM providers;", "DELETE FROM accounts;"}
	for _, p := range c.Providers {
		stmts = append(stmts, fmt.Sprintf(`INSERT INTO providers(id,name,kind,base_url,api_type,enabled,updated_at) VALUES(%s,%s,%s,%s,%s,%d,%s);`, quoteText(p.ID), quoteText(p.Name), quoteText(p.Kind), quoteText(p.BaseURL), quoteText(p.APIType), boolInt(p.Enabled), quoteText(time.Now().UTC().Format(time.RFC3339Nano))))
	}
	for _, a := range c.Accounts {
		stmts = append(stmts, fmt.Sprintf(`INSERT INTO accounts(id,name,kind,enabled,last_status,last_error_kind,updated_at) VALUES(%s,%s,%s,%d,%s,%s,%s);`, quoteText(a.ID), quoteText(a.Name), quoteText(a.Kind), boolInt(a.Enabled), quoteText(a.Status), quoteText(a.LastErrorKind), quoteText(time.Now().UTC().Format(time.RFC3339Nano))))
	}
	if len(c.Routing) > 0 {
		stmts = append(stmts, fmt.Sprintf(`INSERT INTO routing_state(id,payload,updated_at) VALUES(1,%s,%s) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at;`, quoteText(string(c.Routing)), quoteText(time.Now().UTC().Format(time.RFC3339Nano))))
	}
	stmts = append(stmts, fmt.Sprintf(`INSERT INTO legacy_documents(name,payload,updated_at) VALUES('config.json',%s,%s) ON CONFLICT(name) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at;`, quoteText(string(payload)), quoteText(time.Now().UTC().Format(time.RFC3339Nano))))
	return d.Transaction(stmts...)
}

type ModelRecord struct {
	ID, ProviderID, NativeID, Name, Source, Status, DoctorStatus, PreferredProtocol string
	AgentReady                                                                      bool
	LatencyMS                                                                       int64
	LastChecked                                                                     time.Time
	Payload                                                                         []byte
}

func (d *DB) ReplaceModels(models []ModelRecord, snapshot []byte) error {
	stmts := []string{"DELETE FROM models;"}
	for _, m := range models {
		stmts = append(stmts, fmt.Sprintf(`INSERT INTO models(id,provider_id,native_id,name,source,status,doctor_status,preferred_protocol,agent_ready,latency_ms,last_checked,payload) VALUES(%s,%s,%s,%s,%s,%s,%s,%s,%d,%d,%s,%s);`,
			quoteText(m.ID), quoteText(m.ProviderID), quoteText(m.NativeID), quoteText(m.Name), quoteText(m.Source), quoteText(m.Status), quoteText(m.DoctorStatus), quoteText(m.PreferredProtocol), boolInt(m.AgentReady), m.LatencyMS, quoteTime(m.LastChecked), quoteText(string(m.Payload))))
	}
	stmts = append(stmts, fmt.Sprintf(`INSERT INTO legacy_documents(name,payload,updated_at) VALUES('model-registry.json',%s,%s) ON CONFLICT(name) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at;`, quoteText(string(snapshot)), quoteText(time.Now().UTC().Format(time.RFC3339Nano))))
	return d.Transaction(stmts...)
}

func (d *DB) AppendUsageEvent(id string, at time.Time, provider, model string, failed bool, in, out, total, latency int64) error {
	return d.Exec(fmt.Sprintf(`INSERT OR IGNORE INTO usage_events(id,at,provider,model,failed,input_tokens,output_tokens,total_tokens,latency_ms) VALUES(%s,%s,%s,%s,%d,%d,%d,%d,%d);`, quoteText(id), quoteTime(at), quoteText(provider), quoteText(model), boolInt(failed), in, out, total, latency))
}

func (d *DB) ReplaceUsageSnapshot(payload []byte) error {
	return d.PutDocument("usage-stats.json", payload)
}

func (d *DB) AppendHealthEvent(id string, at time.Time, connectionID, source, status string, models, ready int, latency int64, errText string) error {
	return d.Exec(fmt.Sprintf(`INSERT OR IGNORE INTO connection_health(event_id,at,connection_id,source,status,models,ready_models,latency_ms,error) VALUES(%s,%s,%s,%s,%s,%d,%d,%d,%s);`, quoteText(id), quoteTime(at), quoteText(connectionID), quoteText(source), quoteText(status), models, ready, latency, quoteText(errText)))
}

func (d *DB) ReplaceHealthSnapshot(payload []byte) error {
	return d.PutDocument("connection-health.json", payload)
}

func (d *DB) Meta() (map[string]any, error) {
	rows, err := d.Query("SELECT key,value FROM settings ORDER BY key;")
	if err != nil {
		return nil, err
	}
	out := map[string]any{"schema_version": CurrentSchemaVersion, "path": d.path}
	for _, r := range rows {
		out[fmt.Sprint(r["key"])] = r["value"]
	}
	return out, nil
}

func (d *DB) SetSetting(key, value string) error {
	return d.Exec(fmt.Sprintf(`INSERT INTO settings(key,value,updated_at) VALUES(%s,%s,%s) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at;`, quoteText(key), quoteText(value), quoteText(time.Now().UTC().Format(time.RFC3339Nano))))
}

// ImportLegacy performs a one-time, transaction-safe import of the rc6 JSON
// persistence layer. Secrets are explicitly excluded. Existing files remain in
// place as compatibility mirrors and rollback material.
func (d *DB) ImportLegacy(dataDir string) (LegacyImportReport, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.importLegacyUnlocked(dataDir)
}

type LegacyImportReport struct {
	Imported     bool     `json:"imported"`
	BackupDir    string   `json:"backup_dir,omitempty"`
	Documents    []string `json:"documents"`
	Providers    int      `json:"providers"`
	Accounts     int      `json:"accounts"`
	Models       int      `json:"models"`
	UsageEvents  int      `json:"usage_events"`
	HealthEvents int      `json:"health_events"`
}

func (d *DB) importLegacyUnlocked(dataDir string) (LegacyImportReport, error) {
	var rep LegacyImportReport
	rows, err := d.conn.query("SELECT value FROM settings WHERE key='legacy_import_completed' LIMIT 1;")
	if err != nil {
		return rep, err
	}
	if len(rows) > 0 && fmt.Sprint(rows[0]["value"]) == "1" {
		return rep, nil
	}

	names := []string{"config.json", "model-registry.json", "usage-stats.json", "connection-health.json"}
	docs := map[string][]byte{}
	for _, name := range names {
		p := filepath.Join(dataDir, name)
		b, err := os.ReadFile(p)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return rep, err
		}
		if !json.Valid(b) {
			return rep, fmt.Errorf("legacy %s is invalid json", name)
		}
		docs[name] = b
	}
	if len(docs) > 0 {
		stamp := time.Now().UTC().Format("20060102T150405Z")
		rep.BackupDir = filepath.Join(dataDir, "migration-backups", "pre-sqlite-"+stamp)
		if err := os.MkdirAll(rep.BackupDir, 0o700); err != nil {
			return rep, err
		}
		for name, b := range docs {
			if err := os.WriteFile(filepath.Join(rep.BackupDir, name), b, 0o600); err != nil {
				return rep, err
			}
		}
	}
	stmts := []string{}
	for name, b := range docs {
		rep.Documents = append(rep.Documents, name)
		stmts = append(stmts, fmt.Sprintf(`INSERT INTO legacy_documents(name,payload,updated_at) VALUES(%s,%s,%s) ON CONFLICT(name) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at;`, quoteText(name), quoteText(string(b)), quoteText(time.Now().UTC().Format(time.RFC3339Nano))))
	}
	sort.Strings(rep.Documents)

	if b := docs["config.json"]; len(b) > 0 {
		var c struct {
			Providers []struct {
				ID      string `json:"id"`
				Name    string `json:"name"`
				Kind    string `json:"kind"`
				BaseURL string `json:"base_url"`
				APIType string `json:"api_type"`
				Enabled bool   `json:"enabled"`
			} `json:"providers"`
			Accounts []struct {
				ID            string `json:"id"`
				Name          string `json:"name"`
				Kind          string `json:"kind"`
				Status        string `json:"status"`
				LastErrorKind string `json:"last_error_kind"`
				Enabled       bool   `json:"enabled"`
			} `json:"accounts"`
			Routing json.RawMessage `json:"routing"`
		}
		if err := json.Unmarshal(b, &c); err != nil {
			return rep, err
		}
		stmts = append(stmts, "DELETE FROM providers;", "DELETE FROM accounts;")
		for _, p := range c.Providers {
			rep.Providers++
			stmts = append(stmts, fmt.Sprintf(`INSERT INTO providers(id,name,kind,base_url,api_type,enabled,updated_at) VALUES(%s,%s,%s,%s,%s,%d,%s);`, quoteText(p.ID), quoteText(p.Name), quoteText(p.Kind), quoteText(p.BaseURL), quoteText(p.APIType), boolInt(p.Enabled), quoteText(time.Now().UTC().Format(time.RFC3339Nano))))
		}
		for _, a := range c.Accounts {
			rep.Accounts++
			stmts = append(stmts, fmt.Sprintf(`INSERT INTO accounts(id,name,kind,enabled,last_status,last_error_kind,updated_at) VALUES(%s,%s,%s,%d,%s,%s,%s);`, quoteText(a.ID), quoteText(a.Name), quoteText(a.Kind), boolInt(a.Enabled), quoteText(a.Status), quoteText(a.LastErrorKind), quoteText(time.Now().UTC().Format(time.RFC3339Nano))))
		}
		if len(c.Routing) > 0 {
			stmts = append(stmts, fmt.Sprintf(`INSERT INTO routing_state(id,payload,updated_at) VALUES(1,%s,%s) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at;`, quoteText(string(c.Routing)), quoteText(time.Now().UTC().Format(time.RFC3339Nano))))
		}
	}

	if b := docs["model-registry.json"]; len(b) > 0 {
		var snap struct {
			Models []map[string]any `json:"models"`
		}
		if err := json.Unmarshal(b, &snap); err != nil {
			return rep, err
		}
		stmts = append(stmts, "DELETE FROM models;")
		for _, m := range snap.Models {
			mb, _ := json.Marshal(m)
			rep.Models++
			str := func(k string) string {
				if v := m[k]; v != nil {
					return fmt.Sprint(v)
				}
				return ""
			}
			ar := false
			if v, ok := m["agent_ready"].(bool); ok {
				ar = v
			}
			lat := int64(0)
			switch v := m["latency_ms"].(type) {
			case float64:
				lat = int64(v)
			case int64:
				lat = v
			}
			lc := time.Time{}
			if s := str("last_checked"); s != "" {
				lc, _ = time.Parse(time.RFC3339Nano, s)
			}
			stmts = append(stmts, fmt.Sprintf(`INSERT INTO models(id,provider_id,native_id,name,source,status,doctor_status,preferred_protocol,agent_ready,latency_ms,last_checked,payload) VALUES(%s,%s,%s,%s,%s,%s,%s,%s,%d,%d,%s,%s);`, quoteText(str("id")), quoteText(str("provider_id")), quoteText(str("native_id")), quoteText(str("name")), quoteText(str("source")), quoteText(str("status")), quoteText(str("doctor_status")), quoteText(str("preferred_protocol")), boolInt(ar), lat, quoteTime(lc), quoteText(string(mb))))
		}
	}

	if b := docs["usage-stats.json"]; len(b) > 0 {
		var snap struct {
			Events []struct {
				At           time.Time `json:"at"`
				Provider     string    `json:"provider"`
				Model        string    `json:"model"`
				Failed       bool      `json:"failed"`
				InputTokens  int64     `json:"input_tokens"`
				OutputTokens int64     `json:"output_tokens"`
				TotalTokens  int64     `json:"total_tokens"`
				LatencyMS    int64     `json:"latency_ms"`
			} `json:"events"`
		}
		if err := json.Unmarshal(b, &snap); err != nil {
			return rep, err
		}
		for i, e := range snap.Events {
			rep.UsageEvents++
			eid := fmt.Sprintf("legacy:%d:%d", e.At.UnixNano(), i)
			stmts = append(stmts, fmt.Sprintf(`INSERT OR IGNORE INTO usage_events(id,at,provider,model,failed,input_tokens,output_tokens,total_tokens,latency_ms) VALUES(%s,%s,%s,%s,%d,%d,%d,%d,%d);`, quoteText(eid), quoteTime(e.At), quoteText(e.Provider), quoteText(e.Model), boolInt(e.Failed), e.InputTokens, e.OutputTokens, e.TotalTokens, e.LatencyMS))
		}
	}
	if b := docs["connection-health.json"]; len(b) > 0 {
		var events []struct {
			At      time.Time `json:"at"`
			ID      string    `json:"id"`
			Source  string    `json:"source"`
			Status  string    `json:"status"`
			Models  int       `json:"models"`
			Ready   int       `json:"ready_models"`
			Latency int64     `json:"latency_ms"`
			Error   string    `json:"error"`
		}
		if err := json.Unmarshal(b, &events); err != nil {
			return rep, err
		}
		for i, e := range events {
			rep.HealthEvents++
			eid := fmt.Sprintf("legacy:%d:%d", e.At.UnixNano(), i)
			stmts = append(stmts, fmt.Sprintf(`INSERT OR IGNORE INTO connection_health(event_id,at,connection_id,source,status,models,ready_models,latency_ms,error) VALUES(%s,%s,%s,%s,%s,%d,%d,%d,%s);`, quoteText(eid), quoteTime(e.At), quoteText(e.ID), quoteText(e.Source), quoteText(e.Status), e.Models, e.Ready, e.Latency, quoteText(e.Error)))
		}
	}
	stmts = append(stmts, fmt.Sprintf(`INSERT INTO settings(key,value,updated_at) VALUES('legacy_import_completed','1',%s) ON CONFLICT(key) DO UPDATE SET value='1',updated_at=excluded.updated_at;`, quoteText(time.Now().UTC().Format(time.RFC3339Nano))))
	if err := d.conn.exec("BEGIN IMMEDIATE;"); err != nil {
		return rep, err
	}
	for _, stmt := range stmts {
		if err := d.conn.exec(stmt); err != nil {
			_ = d.conn.exec("ROLLBACK;")
			return rep, err
		}
	}
	if err := d.conn.exec("COMMIT;"); err != nil {
		_ = d.conn.exec("ROLLBACK;")
		return rep, err
	}
	rep.Imported = true
	return rep, nil
}

type UsageEventRecord struct {
	ID           string
	At           time.Time
	Provider     string
	Model        string
	Failed       bool
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
	LatencyMS    int64
}

func (d *DB) PersistUsage(snapshot []byte, event *UsageEventRecord) error {
	if !json.Valid(snapshot) {
		return errors.New("usage snapshot is invalid json")
	}
	stmts := []string{fmt.Sprintf(`INSERT INTO legacy_documents(name,payload,updated_at) VALUES('usage-stats.json',%s,%s) ON CONFLICT(name) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at;`, quoteText(string(snapshot)), quoteText(time.Now().UTC().Format(time.RFC3339Nano)))}
	if event != nil {
		stmts = append(stmts, fmt.Sprintf(`INSERT OR IGNORE INTO usage_events(id,at,provider,model,failed,input_tokens,output_tokens,total_tokens,latency_ms) VALUES(%s,%s,%s,%s,%d,%d,%d,%d,%d);`, quoteText(event.ID), quoteTime(event.At), quoteText(event.Provider), quoteText(event.Model), boolInt(event.Failed), event.InputTokens, event.OutputTokens, event.TotalTokens, event.LatencyMS))
	}
	return d.Transaction(stmts...)
}

type HealthEventRecord struct {
	EventID      string
	At           time.Time
	ConnectionID string
	Source       string
	Status       string
	Models       int
	Ready        int
	LatencyMS    int64
	Error        string
}

func (d *DB) PersistHealth(snapshot []byte, event *HealthEventRecord) error {
	if !json.Valid(snapshot) {
		return errors.New("health snapshot is invalid json")
	}
	stmts := []string{fmt.Sprintf(`INSERT INTO legacy_documents(name,payload,updated_at) VALUES('connection-health.json',%s,%s) ON CONFLICT(name) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at;`, quoteText(string(snapshot)), quoteText(time.Now().UTC().Format(time.RFC3339Nano)))}
	if event != nil {
		stmts = append(stmts, fmt.Sprintf(`INSERT OR IGNORE INTO connection_health(event_id,at,connection_id,source,status,models,ready_models,latency_ms,error) VALUES(%s,%s,%s,%s,%s,%d,%d,%d,%s);`, quoteText(event.EventID), quoteTime(event.At), quoteText(event.ConnectionID), quoteText(event.Source), quoteText(event.Status), event.Models, event.Ready, event.LatencyMS, quoteText(event.Error)))
	}
	return d.Transaction(stmts...)
}

type CircuitBreakerRecord struct {
	Key       string
	State     string
	UpdatedAt time.Time
	Payload   []byte
}

func (d *DB) SaveCircuitBreaker(key, state string, updatedAt time.Time, payload []byte) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("circuit breaker key is empty")
	}
	if !json.Valid(payload) {
		return errors.New("circuit breaker payload is invalid json")
	}
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	return d.Exec(fmt.Sprintf(`INSERT INTO circuit_breakers(key,state,updated_at,payload) VALUES(%s,%s,%s,%s)
ON CONFLICT(key) DO UPDATE SET state=excluded.state,updated_at=excluded.updated_at,payload=excluded.payload;`, quoteText(key), quoteText(state), quoteTime(updatedAt), quoteText(string(payload))))
}

func (d *DB) LoadCircuitBreakers() ([]CircuitBreakerRecord, error) {
	rows, err := d.Query(`SELECT key,state,updated_at,payload FROM circuit_breakers ORDER BY key;`)
	if err != nil {
		return nil, err
	}
	out := make([]CircuitBreakerRecord, 0, len(rows))
	for _, row := range rows {
		rec := CircuitBreakerRecord{Key: fmt.Sprint(row["key"]), State: fmt.Sprint(row["state"]), Payload: []byte(fmt.Sprint(row["payload"]))}
		if raw := fmt.Sprint(row["updated_at"]); raw != "" && raw != "<nil>" {
			rec.UpdatedAt, _ = time.Parse(time.RFC3339Nano, raw)
		}
		out = append(out, rec)
	}
	return out, nil
}

func (d *DB) DeleteCircuitBreakers(key string) error {
	if strings.TrimSpace(key) == "" {
		return d.Transaction(`DELETE FROM circuit_breakers;`, `DELETE FROM resilience_events;`)
	}
	return d.Transaction(`DELETE FROM circuit_breakers WHERE key=`+quoteText(key)+`;`, `DELETE FROM resilience_events WHERE breaker_key=`+quoteText(key)+`;`)
}

func (d *DB) AppendResilienceEvent(key string, at time.Time, fromState, toState, failureKind, reason string, retryAfterMS int64) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	id := fmt.Sprintf("%s:%d:%s:%s", key, at.UnixNano(), fromState, toState)
	return d.Exec(fmt.Sprintf(`INSERT OR IGNORE INTO resilience_events(event_id,at,breaker_key,from_state,to_state,failure_kind,reason,retry_after_ms) VALUES(%s,%s,%s,%s,%s,%s,%s,%d);`, quoteText(id), quoteTime(at), quoteText(key), quoteText(fromState), quoteText(toState), quoteText(failureKind), quoteText(reason), retryAfterMS))
}

func (d *DB) Checkpoint() error {
	return d.Exec("PRAGMA wal_checkpoint(TRUNCATE);")
}

func (d *DB) QuickCheck() (string, error) {
	rows, err := d.Query("PRAGMA quick_check;")
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", errors.New("sqlite quick_check returned no rows")
	}
	for _, v := range rows[0] {
		return fmt.Sprint(v), nil
	}
	return "", errors.New("sqlite quick_check returned empty row")
}

func (d *DB) Counts() (map[string]int64, error) {
	tables := []string{"providers", "accounts", "models", "usage_events", "connection_health", "doctor_results", "eval_results", "quota_snapshots", "pricing", "cost_events", "circuit_breakers", "resilience_events", "context_events", "integration_state", "mcp_servers", "request_traces"}
	out := map[string]int64{}
	for _, table := range tables {
		rows, err := d.Query("SELECT COUNT(*) AS count FROM " + table + ";")
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			continue
		}
		if v, ok := rows[0]["count"].(int64); ok {
			out[table] = v
		}
	}
	return out, nil
}

type QuotaSnapshotRecord struct {
	ConnectionID string
	At           time.Time
	Confidence   string
	Payload      []byte
}

func (d *DB) SaveQuotaSnapshot(connectionID, confidence string, at time.Time, payload []byte) error {
	if strings.TrimSpace(connectionID) == "" {
		return errors.New("quota connection id is empty")
	}
	if !json.Valid(payload) {
		return errors.New("quota payload is invalid json")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if strings.TrimSpace(confidence) == "" {
		confidence = "unknown"
	}
	return d.Transaction(
		`DELETE FROM quota_snapshots WHERE connection_id=`+quoteText(connectionID)+`;`,
		fmt.Sprintf(`INSERT INTO quota_snapshots(connection_id,at,confidence,payload) VALUES(%s,%s,%s,%s);`, quoteText(connectionID), quoteTime(at), quoteText(confidence), quoteText(string(payload))),
	)
}

func (d *DB) LoadQuotaSnapshots() ([]QuotaSnapshotRecord, error) {
	rows, err := d.Query(`SELECT connection_id,at,confidence,payload FROM quota_snapshots ORDER BY connection_id;`)
	if err != nil {
		return nil, err
	}
	out := make([]QuotaSnapshotRecord, 0, len(rows))
	for _, row := range rows {
		r := QuotaSnapshotRecord{ConnectionID: fmt.Sprint(row["connection_id"]), Confidence: fmt.Sprint(row["confidence"]), Payload: []byte(fmt.Sprint(row["payload"]))}
		if raw := fmt.Sprint(row["at"]); raw != "" && raw != "<nil>" {
			r.At, _ = time.Parse(time.RFC3339Nano, raw)
		}
		out = append(out, r)
	}
	return out, nil
}

type PricingRecord struct {
	Provider   string
	Model      string
	Source     string
	Confidence string
	UpdatedAt  time.Time
	Payload    []byte
}

func (d *DB) SavePricing(provider, model, source, confidence string, updatedAt time.Time, payload []byte) error {
	if strings.TrimSpace(provider) == "" || strings.TrimSpace(model) == "" {
		return errors.New("pricing provider/model is empty")
	}
	if !json.Valid(payload) {
		return errors.New("pricing payload is invalid json")
	}
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	return d.Exec(fmt.Sprintf(`INSERT INTO pricing(provider,model,source,confidence,updated_at,payload) VALUES(%s,%s,%s,%s,%s,%s)
ON CONFLICT(provider,model) DO UPDATE SET source=excluded.source,confidence=excluded.confidence,updated_at=excluded.updated_at,payload=excluded.payload;`, quoteText(provider), quoteText(model), quoteText(source), quoteText(confidence), quoteTime(updatedAt), quoteText(string(payload))))
}

func (d *DB) LoadPricing() ([]PricingRecord, error) {
	rows, err := d.Query(`SELECT provider,model,source,confidence,updated_at,payload FROM pricing ORDER BY provider,model;`)
	if err != nil {
		return nil, err
	}
	out := make([]PricingRecord, 0, len(rows))
	for _, row := range rows {
		r := PricingRecord{Provider: fmt.Sprint(row["provider"]), Model: fmt.Sprint(row["model"]), Source: fmt.Sprint(row["source"]), Confidence: fmt.Sprint(row["confidence"]), Payload: []byte(fmt.Sprint(row["payload"]))}
		if raw := fmt.Sprint(row["updated_at"]); raw != "" && raw != "<nil>" {
			r.UpdatedAt, _ = time.Parse(time.RFC3339Nano, raw)
		}
		out = append(out, r)
	}
	return out, nil
}

func (d *DB) AppendContextEvent(eventID string, at time.Time, model string, beforeTokens, afterTokens, savedTokens int64, action string) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if strings.TrimSpace(eventID) == "" {
		eventID = fmt.Sprintf("ctx:%d", at.UnixNano())
	}
	return d.Exec(fmt.Sprintf(`INSERT OR IGNORE INTO context_events(event_id,at,model,before_tokens,after_tokens,saved_tokens,action) VALUES(%s,%s,%s,%d,%d,%d,%s);`, quoteText(eventID), quoteTime(at), quoteText(model), beforeTokens, afterTokens, savedTokens, quoteText(action)))
}

func (d *DB) SaveIntegrationState(id string, payload []byte) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("integration id is empty")
	}
	if !json.Valid(payload) {
		return errors.New("integration payload is invalid json")
	}
	return d.Exec(fmt.Sprintf(`INSERT INTO integration_state(id,payload,updated_at) VALUES(%s,%s,%s) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at;`, quoteText(id), quoteText(string(payload)), quoteTime(time.Now().UTC())))
}

func (d *DB) LoadIntegrationStates() (map[string][]byte, error) {
	rows, err := d.Query(`SELECT id,payload FROM integration_state ORDER BY id;`)
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, row := range rows {
		out[fmt.Sprint(row["id"])] = []byte(fmt.Sprint(row["payload"]))
	}
	return out, nil
}

func (d *DB) SaveMCPServer(id string, payload []byte) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("mcp server id is empty")
	}
	if !json.Valid(payload) {
		return errors.New("mcp server payload is invalid json")
	}
	return d.Exec(fmt.Sprintf(`INSERT INTO mcp_servers(id,payload,updated_at) VALUES(%s,%s,%s) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at;`, quoteText(id), quoteText(string(payload)), quoteTime(time.Now().UTC())))
}

func (d *DB) DeleteMCPServer(id string) error {
	return d.Exec(`DELETE FROM mcp_servers WHERE id=` + quoteText(id) + `;`)
}

func (d *DB) LoadMCPServers() (map[string][]byte, error) {
	rows, err := d.Query(`SELECT id,payload FROM mcp_servers ORDER BY id;`)
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, row := range rows {
		out[fmt.Sprint(row["id"])] = []byte(fmt.Sprint(row["payload"]))
	}
	return out, nil
}

func (d *DB) AppendTrace(traceID string, startedAt time.Time, durationMS int64, method, path, model, selectedModel, provider, status, errorKind string, attempts int, payload []byte) error {
	if strings.TrimSpace(traceID) == "" {
		return errors.New("trace id is empty")
	}
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	if len(payload) == 0 {
		payload = []byte(`{}`)
	}
	if !json.Valid(payload) {
		return errors.New("trace payload is invalid json")
	}
	return d.Exec(fmt.Sprintf(`INSERT OR REPLACE INTO request_traces(trace_id,started_at,duration_ms,method,path,model,selected_model,provider,status,error_kind,attempts,payload) VALUES(%s,%s,%d,%s,%s,%s,%s,%s,%s,%s,%d,%s);`, quoteText(traceID), quoteTime(startedAt), durationMS, quoteText(method), quoteText(path), quoteText(model), quoteText(selectedModel), quoteText(provider), quoteText(status), quoteText(errorKind), attempts, quoteText(string(payload))))
}

func (d *DB) RecentTraces(limit int) ([]Row, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	return d.Query(fmt.Sprintf(`SELECT trace_id,started_at,duration_ms,method,path,model,selected_model,provider,status,error_kind,attempts,payload FROM request_traces ORDER BY started_at DESC LIMIT %d;`, limit))
}

func (d *DB) AppendCostEvent(eventID string, at time.Time, provider, model, connectionID string, inputTokens, outputTokens int64, costUSD float64, confidence string) error {
	if strings.TrimSpace(eventID) == "" {
		return errors.New("cost event id is empty")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if confidence == "" {
		confidence = "unknown"
	}
	return d.Exec(fmt.Sprintf(`INSERT OR IGNORE INTO cost_events(event_id,at,provider,model,connection_id,input_tokens,output_tokens,cost_usd,confidence) VALUES(%s,%s,%s,%s,%s,%d,%d,%.12f,%s);`, quoteText(eventID), quoteTime(at), quoteText(provider), quoteText(model), quoteText(connectionID), inputTokens, outputTokens, costUSD, quoteText(confidence)))
}

func (d *DB) CostSince(since time.Time) (float64, error) {
	where := ""
	if !since.IsZero() {
		where = " WHERE at >= " + quoteTime(since)
	}
	rows, err := d.Query(`SELECT COALESCE(SUM(cost_usd),0) AS total FROM cost_events` + where + `;`)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	switch v := rows[0]["total"].(type) {
	case float64:
		return v, nil
	case int64:
		return float64(v), nil
	case string:
		f, _ := strconv.ParseFloat(v, 64)
		return f, nil
	default:
		return 0, nil
	}
}

func (d *DB) ContextStats() (map[string]int64, error) {
	rows, err := d.Query(`SELECT COUNT(*) AS events, COALESCE(SUM(saved_tokens),0) AS saved_tokens, COALESCE(SUM(before_tokens),0) AS before_tokens, COALESCE(SUM(after_tokens),0) AS after_tokens FROM context_events;`)
	if err != nil {
		return nil, err
	}
	out := map[string]int64{"events": 0, "saved_tokens": 0, "before_tokens": 0, "after_tokens": 0}
	if len(rows) == 0 {
		return out, nil
	}
	for k := range out {
		switch v := rows[0][k].(type) {
		case int64:
			out[k] = v
		case float64:
			out[k] = int64(v)
		case string:
			n, _ := strconv.ParseInt(v, 10, 64)
			out[k] = n
		}
	}
	return out, nil
}
