package releasecheck

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyManifestRejectsTamperAndTraversal(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(p, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("ok"))
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS.txt"), []byte(fmt.Sprintf("%x  a.txt\n", sum)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyManifest(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyManifest(dir); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("expected tamper rejection, got %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS.txt"), []byte(fmt.Sprintf("%x  ../a.txt\n", sum)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyManifest(dir); err == nil || !strings.Contains(err.Error(), "traversal") {
		t.Fatalf("expected traversal rejection, got %v", err)
	}
}

func TestVerifyRuntimeRejectsSecretLikePayload(t *testing.T) {
	dir := t.TempDir()
	required := map[string]string{
		"OmniBridge.exe": "pe", "VERSION.txt": "1.0.0-rc1",
		"RELEASE.json":                              `{"version":"1.0.0-rc1","channel":"rc","config_schema":5,"db_schema":3}`,
		filepath.Join("scripts", "desktop.ps1"):     "\ufeffWrite-Host ok",
		filepath.Join("scripts", "maintenance.ps1"): "\ufeffWrite-Host ok",
		filepath.Join("ui", "Shell.xaml"):           `<Window></Window>`, filepath.Join("ui", "Theme.xaml"): `<ResourceDictionary></ResourceDictionary>`,
	}
	for rel, body := range required {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	lines := ""
	for rel := range required {
		b, _ := os.ReadFile(filepath.Join(dir, rel))
		s := sha256.Sum256(b)
		lines += fmt.Sprintf("%x  %s\n", s, filepath.ToSlash(rel))
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS.txt"), []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	r := VerifyRuntime(dir, "1.0.0-rc1")
	if !r.OK {
		t.Fatalf("unexpected failure: %+v", r)
	}
	if err := os.WriteFile(filepath.Join(dir, "secrets.json"), []byte(`{"x":"y"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r = VerifyRuntime(dir, "1.0.0-rc1")
	if r.OK {
		t.Fatal("expected secret hygiene failure")
	}
}
