package releasecheck

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

type Report struct {
	OK      bool    `json:"ok"`
	Version string  `json:"version"`
	Checks  []Check `json:"checks"`
}

type releaseMetadata struct {
	Version      string `json:"version"`
	Channel      string `json:"channel"`
	ConfigSchema int    `json:"config_schema"`
	DBSchema     int    `json:"db_schema"`
}

func VerifyRuntime(root, expectedVersion string) Report {
	r := Report{OK: true, Version: expectedVersion}
	add := func(name string, err error) {
		c := Check{Name: name, OK: err == nil}
		if err != nil {
			c.Detail = err.Error()
			r.OK = false
		}
		r.Checks = append(r.Checks, c)
	}
	add("required_files", verifyRequired(root))
	add("version_identity", verifyVersion(root, expectedVersion))
	add("release_metadata", verifyMetadata(root, expectedVersion))
	add("sha256_manifest", VerifyManifest(root))
	add("powershell_utf8_bom", verifyPowerShellBOM(root))
	add("xaml_well_formed", verifyXAML(root))
	add("secret_hygiene", verifySecretHygiene(root))
	return r
}

func verifyRequired(root string) error {
	required := []string{
		"OmniBridge.exe", "VERSION.txt", "RELEASE.json", "SHA256SUMS.txt",
		filepath.Join("scripts", "desktop.ps1"), filepath.Join("scripts", "maintenance.ps1"),
		filepath.Join("ui", "Shell.xaml"), filepath.Join("ui", "Theme.xaml"),
	}
	missing := []string{}
	for _, rel := range required {
		st, err := os.Stat(filepath.Join(root, rel))
		if err != nil || st.IsDir() {
			missing = append(missing, filepath.ToSlash(rel))
		}
	}
	if len(missing) != 0 {
		return fmt.Errorf("missing: %s", strings.Join(missing, ", "))
	}
	return nil
}

func verifyVersion(root, expected string) error {
	b, err := os.ReadFile(filepath.Join(root, "VERSION.txt"))
	if err != nil {
		return err
	}
	got := strings.TrimSpace(string(b))
	if got != expected {
		return fmt.Errorf("VERSION.txt=%q expected=%q", got, expected)
	}
	return nil
}

func verifyMetadata(root, expected string) error {
	b, err := os.ReadFile(filepath.Join(root, "RELEASE.json"))
	if err != nil {
		return err
	}
	var m releaseMetadata
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	if m.Version != expected {
		return fmt.Errorf("RELEASE.json version=%q expected=%q", m.Version, expected)
	}
	if m.Channel != "rc" {
		return fmt.Errorf("RELEASE.json channel=%q expected=rc", m.Channel)
	}
	if m.ConfigSchema <= 0 || m.DBSchema <= 0 {
		return errors.New("RELEASE.json schema metadata is incomplete")
	}
	return nil
}

func safeManifestPath(rel string) (string, error) {
	rel = strings.TrimSpace(strings.ReplaceAll(rel, "\\", "/"))
	if rel == "" {
		return "", errors.New("empty manifest path")
	}
	if strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute path forbidden: %s", rel)
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path traversal forbidden: %s", rel)
	}
	return clean, nil
}

func VerifyManifest(root string) error {
	f, err := os.Open(filepath.Join(root, "SHA256SUMS.txt"))
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	checked := 0
	seen := map[string]bool{}
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || len(fields[0]) != 64 {
			return fmt.Errorf("malformed SHA256SUMS line: %q", line)
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			return fmt.Errorf("malformed sha256 for %q", line)
		}
		relText := strings.TrimSpace(strings.TrimPrefix(line[len(fields[0]):], "*"))
		rel, err := safeManifestPath(relText)
		if err != nil {
			return err
		}
		key := strings.ToLower(filepath.Clean(rel))
		if seen[key] {
			return fmt.Errorf("duplicate manifest path: %s", filepath.ToSlash(rel))
		}
		seen[key] = true
		if strings.EqualFold(filepath.Base(rel), "SHA256SUMS.txt") {
			continue
		}
		p := filepath.Join(root, rel)
		rf, err := os.Open(p)
		if err != nil {
			return fmt.Errorf("manifest file %s: %w", filepath.ToSlash(rel), err)
		}
		h := sha256.New()
		_, copyErr := io.Copy(h, rf)
		closeErr := rf.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		actual := hex.EncodeToString(h.Sum(nil))
		if !strings.EqualFold(actual, fields[0]) {
			return fmt.Errorf("sha256 mismatch: %s", filepath.ToSlash(rel))
		}
		checked++
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if checked == 0 {
		return errors.New("SHA256SUMS contains no files")
	}
	return nil
}

func verifyPowerShellBOM(root string) error {
	files, err := filepath.Glob(filepath.Join(root, "scripts", "*.ps1"))
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return errors.New("no PowerShell runtime scripts")
	}
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if len(b) < 3 || b[0] != 0xEF || b[1] != 0xBB || b[2] != 0xBF {
			return fmt.Errorf("UTF-8 BOM missing: %s", filepath.Base(p))
		}
	}
	return nil
}

func verifyXAML(root string) error {
	var files []string
	err := filepath.WalkDir(filepath.Join(root, "ui"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".xaml") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return errors.New("no XAML files")
	}
	sort.Strings(files)
	for _, p := range files {
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		dec := xml.NewDecoder(f)
		for {
			_, err = dec.Token()
			if errors.Is(err, io.EOF) {
				err = nil
				break
			}
			if err != nil {
				break
			}
		}
		_ = f.Close()
		if err != nil {
			return fmt.Errorf("invalid XAML %s: %w", filepath.Base(p), err)
		}
	}
	return nil
}

func verifySecretHygiene(root string) error {
	forbiddenNames := map[string]bool{
		"secrets.json": true, ".env": true, "credentials.json": true,
	}
	var bad []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		relSlash := filepath.ToSlash(rel)
		// data/ and logs/ are mutable runtime directories. A legitimate installed
		// instance contains data/secrets.json protected by DPAPI; package-time
		// release hygiene is enforced separately by build-release.ps1.
		if strings.HasPrefix(relSlash, "data/") || strings.HasPrefix(relSlash, "logs/") {
			return nil
		}
		name := strings.ToLower(d.Name())
		if forbiddenNames[name] || strings.HasSuffix(name, ".pfx") || strings.HasSuffix(name, ".p12") || strings.HasSuffix(name, ".key") {
			bad = append(bad, relSlash)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(bad) != 0 {
		sort.Strings(bad)
		return fmt.Errorf("forbidden secret-like files: %s", strings.Join(bad, ", "))
	}
	return nil
}
