//go:build windows

package storage

import (
	"math"
	"path/filepath"
	"testing"
)

func TestWindowsSQLiteRealColumnsRemainFloat64(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "real-columns.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	rows, err := db.Query("SELECT CAST(12.75 AS REAL) AS value;")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one row, got %d", len(rows))
	}
	got, ok := rows[0]["value"].(float64)
	if !ok {
		t.Fatalf("REAL column must be float64, got %T (%v)", rows[0]["value"], rows[0]["value"])
	}
	if math.Abs(got-12.75) > 1e-12 {
		t.Fatalf("unexpected REAL value: %v", got)
	}
}
