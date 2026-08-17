package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTestDB(t *testing.T, path string) *DB {
	t.Helper()
	db, err := Open(path)
	if err != nil {
		if strings.Contains(err.Error(), "requires cgo") {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestOpenCreatesWALDatabaseAndSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "omnibridge.db")
	db := openTestDB(t, path)

	rows, err := db.Query("PRAGMA journal_mode;")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || strings.ToLower(strings.TrimSpace(anyString(rows[0]))) != "wal" {
		t.Fatalf("journal mode = %#v, want WAL", rows)
	}
	rows, err = db.Query("PRAGMA user_version;")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || anyInt(rows[0]) != CurrentSchemaVersion {
		t.Fatalf("user_version = %#v, want %d", rows, CurrentSchemaVersion)
	}
	if check, err := db.QuickCheck(); err != nil || check != "ok" {
		t.Fatalf("quick_check=%q err=%v", check, err)
	}
	if err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 16 || string(b[:16]) != "SQLite format 3\x00" {
		t.Fatalf("not a sqlite database header: %q", b[:min(16, len(b))])
	}
}

func TestImportLegacyIsTransactionalBackedUpAndIdempotent(t *testing.T) {
	data := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	writeJSON := func(name string, v any) {
		t.Helper()
		b, _ := json.MarshalIndent(v, "", "  ")
		if err := os.WriteFile(filepath.Join(data, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeJSON("config.json", map[string]any{
		"schema_version": 3,
		"version":        "0.3.2-rc6",
		"listen":         "127.0.0.1:4142",
		"providers":      []any{map[string]any{"id": "p1", "name": "Provider 1", "kind": "openai-compatible", "base_url": "https://example.invalid/v1", "api_type": "chat-completions", "enabled": true}},
		"accounts":       []any{map[string]any{"id": "a1", "name": "Account 1", "kind": "chatgpt", "enabled": true, "status": "connected"}},
		"routing":        map[string]any{"max_retries": 2},
	})
	writeJSON("model-registry.json", map[string]any{"updated_at": now, "models": []any{map[string]any{"id": "p1/m1", "provider_id": "p1", "native_id": "m1", "name": "m1", "status": "ready", "doctor_status": "agent_ready", "agent_ready": true, "latency_ms": 42, "last_checked": now}}})
	writeJSON("usage-stats.json", map[string]any{"started_at": now, "events": []any{map[string]any{"at": now, "provider": "p1", "model": "p1/m1", "failed": false, "input_tokens": 10, "output_tokens": 5, "total_tokens": 15, "latency_ms": 42}}})
	writeJSON("connection-health.json", []any{map[string]any{"at": now, "id": "p1", "source": "api", "status": "ready", "models": 1, "ready_models": 1, "latency_ms": 42}})
	if err := os.WriteFile(filepath.Join(data, "secrets.json"), []byte(`{"must":"not-import"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	db := openTestDB(t, filepath.Join(data, "omnibridge.db"))
	rep, err := db.ImportLegacy(data)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Imported || rep.Providers != 1 || rep.Accounts != 1 || rep.Models != 1 || rep.UsageEvents != 1 || rep.HealthEvents != 1 {
		t.Fatalf("unexpected import report: %+v", rep)
	}
	if rep.BackupDir == "" {
		t.Fatal("migration backup was not created")
	}
	for _, name := range []string{"config.json", "model-registry.json", "usage-stats.json", "connection-health.json"} {
		if _, err := os.Stat(filepath.Join(rep.BackupDir, name)); err != nil {
			t.Fatalf("backup missing %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(rep.BackupDir, "secrets.json")); !os.IsNotExist(err) {
		t.Fatalf("secrets.json must not be copied to migration backup, err=%v", err)
	}
	counts, err := db.Counts()
	if err != nil {
		t.Fatal(err)
	}
	if counts["providers"] != 1 || counts["accounts"] != 1 || counts["models"] != 1 || counts["usage_events"] != 1 || counts["connection_health"] != 1 {
		t.Fatalf("unexpected counts: %#v", counts)
	}
	rows, err := db.Query("SELECT input_tokens,output_tokens,total_tokens,latency_ms FROM usage_events LIMIT 1;")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["input_tokens"] != int64(10) || rows[0]["output_tokens"] != int64(5) || rows[0]["total_tokens"] != int64(15) || rows[0]["latency_ms"] != int64(42) {
		t.Fatalf("usage event was not imported correctly: %#v", rows)
	}
	rows, err = db.Query("SELECT last_status FROM accounts WHERE id='a1';")
	if err != nil || len(rows) != 1 || rows[0]["last_status"] != "connected" {
		t.Fatalf("account status import: rows=%#v err=%v", rows, err)
	}

	rep2, err := db.ImportLegacy(data)
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Imported {
		t.Fatalf("second import should be a no-op: %+v", rep2)
	}
	counts2, _ := db.Counts()
	if counts2["usage_events"] != 1 || counts2["connection_health"] != 1 {
		t.Fatalf("idempotency failed: %#v", counts2)
	}
}

func TestTransactionRollsBackOnFailure(t *testing.T) {
	db := openTestDB(t, filepath.Join(t.TempDir(), "omnibridge.db"))
	err := db.Transaction(
		"INSERT INTO settings(key,value,updated_at) VALUES('tx-test','before','now');",
		"INSERT INTO definitely_missing_table(x) VALUES(1);",
	)
	if err == nil {
		t.Fatal("expected transaction failure")
	}
	rows, err := db.Query("SELECT value FROM settings WHERE key='tx-test';")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("failed transaction was not rolled back: %#v", rows)
	}
}

func anyString(r Row) string {
	for _, v := range r {
		return strings.TrimSpace(toString(v))
	}
	return ""
}

func anyInt(r Row) int {
	for _, v := range r {
		if n, ok := v.(int64); ok {
			return int(n)
		}
	}
	return -1
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func TestExistingV0DatabaseGetsSchemaBackupBeforeMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "omnibridge.db")
	conn, err := nativeOpen(path)
	if err != nil {
		if strings.Contains(err.Error(), "requires cgo") {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	if err := conn.exec("CREATE TABLE sentinel(value TEXT); INSERT INTO sentinel(value) VALUES('keep'); PRAGMA user_version=0;"); err != nil {
		t.Fatal(err)
	}
	if err := conn.close(); err != nil {
		t.Fatal(err)
	}
	db := openTestDB(t, path)
	rows, err := db.Query("SELECT value FROM sentinel;")
	if err != nil || len(rows) != 1 || rows[0]["value"] != "keep" {
		t.Fatalf("sentinel lost: rows=%#v err=%v", rows, err)
	}
	matches, err := filepath.Glob(path + ".schema0.*.bak")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("schema migration backup not created: %v", matches)
	}
}

func TestRejectsDatabaseFromNewerSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "omnibridge.db")
	db := openTestDB(t, path)
	if err := db.Exec("PRAGMA user_version=999;"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := Open(path)
	if err == nil || !strings.Contains(err.Error(), "newer than supported") {
		t.Fatalf("expected newer-schema rejection, got %v", err)
	}
}

func TestSchema1MigratesToCurrentSchemaWithResilienceEvents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "omnibridge.db")
	conn, err := nativeOpen(path)
	if err != nil {
		if strings.Contains(err.Error(), "requires cgo") {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	for _, stmt := range migrations[1] {
		if err := conn.exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := conn.exec("PRAGMA user_version=1;"); err != nil {
		t.Fatal(err)
	}
	if err := conn.close(); err != nil {
		t.Fatal(err)
	}

	db := openTestDB(t, path)
	defer db.Close()
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table' AND name='resilience_events';")
	if err != nil || len(rows) != 1 {
		t.Fatalf("resilience_events missing rows=%#v err=%v", rows, err)
	}
	uv, err := db.Query("PRAGMA user_version;")
	if err != nil || len(uv) != 1 || anyInt(uv[0]) != CurrentSchemaVersion {
		t.Fatalf("user_version=%#v err=%v", uv, err)
	}
	matches, err := filepath.Glob(path + ".schema1.*.bak")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("schema1 migration backup missing: %v", matches)
	}
}

func TestCorruptDatabaseIsRejectedWithoutOverwritingOriginal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "omnibridge.db")
	original := []byte("not-a-sqlite-database\x00release-corruption-probe")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if db, err := Open(path); err == nil {
		_ = db.Close()
		t.Fatal("expected corrupt database to be rejected")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatal("corrupt database was modified while failing closed")
	}
}
