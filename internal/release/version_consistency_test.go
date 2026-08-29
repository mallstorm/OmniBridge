package release

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"omnibridge/internal/config"
)

func TestReleaseVersionIsConsistentAcrossRuntimeAndUI(t *testing.T) {
	root := projectRoot(t)
	versionBytes, err := os.ReadFile(filepath.Join(root, "VERSION.txt"))
	if err != nil {
		t.Fatal(err)
	}
	version := strings.TrimSpace(string(versionBytes))
	if version != config.CurrentVersion {
		t.Fatalf("VERSION.txt=%q config.CurrentVersion=%q", version, config.CurrentVersion)
	}

	var release struct {
		Version string `json:"version"`
	}
	releaseBytes, err := os.ReadFile(filepath.Join(root, "RELEASE.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(releaseBytes, &release); err != nil {
		t.Fatal(err)
	}
	if release.Version != version {
		t.Fatalf("RELEASE.json=%q VERSION.txt=%q", release.Version, version)
	}

	re := regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)-rc(\d+)$`)
	parts := re.FindStringSubmatch(version)
	if len(parts) != 5 {
		t.Fatalf("unsupported release version format %q", version)
	}
	assemblyVersion := fmt.Sprintf(`version="%s.%s.%s.%s"`, parts[1], parts[2], parts[3], parts[4])
	manifest, err := os.ReadFile(filepath.Join(root, "assets", "omnibridge.exe.manifest"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifest), assemblyVersion) {
		t.Fatalf("Windows manifest does not contain %s", assemblyVersion)
	}

	uiFiles := []string{
		filepath.Join("ui", "Shell.xaml"),
		filepath.Join("ui", "pages", "Dashboard.xaml"),
		filepath.Join("ui", "pages", "Settings.xaml"),
		filepath.Join("ui", "pages", "About.xaml"),
	}
	for _, rel := range uiFiles {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), version) {
			t.Errorf("%s does not contain current version %s", filepath.ToSlash(rel), version)
		}
	}
}
