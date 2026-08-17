package release

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

func projectRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestRuntimePowerShellScriptsAreUTF8BOM(t *testing.T) {
	scripts := filepath.Join(projectRoot(t), "scripts")
	err := filepath.Walk(scripts, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.EqualFold(filepath.Ext(path), ".ps1") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.HasPrefix(b, utf8BOM) {
			t.Errorf("%s must be UTF-8 with BOM for Windows PowerShell 5.1", filepath.Base(path))
			return nil
		}
		if !utf8.Valid(b[len(utf8BOM):]) {
			t.Errorf("%s contains invalid UTF-8", filepath.Base(path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDesktopAccountLoginContract(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(projectRoot(t), "scripts", "desktop.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(bytes.TrimPrefix(b, utf8BOM))
	for _, want := range []string{"$r.auth_url", "$r.verification_url", "$r.browser_managed", "/preflight", "Start-Process $authUrl", "Start-Process $verificationUrl", "$settings.command", "$settings.client_json"} {
		if !strings.Contains(s, want) {
			t.Errorf("desktop.ps1 missing account login contract fragment %q", want)
		}
	}
}

func TestServerDoesNotStartCodexLoginTwice(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(projectRoot(t), "internal", "server", "server.go"))
	if err != nil {
		t.Fatal(err)
	}
	needle := "out, err = s.Accounts.StartCodexLogin(context.Background(), a, in.Device)"
	if got := strings.Count(string(b), needle); got != 1 {
		t.Fatalf("startAccountLogin must invoke Codex login exactly once; got %d", got)
	}
}
