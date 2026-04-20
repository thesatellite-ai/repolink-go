package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMVP_02_Config_AddProfile_PreservesComments(t *testing.T) {
	path := writeTempConfig(t, sampleJSONC)
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := c.AddProfile("archive", Profile{Dir: "/tmp/archive-repo", ScanRoots: []string{"/tmp/archive-scans"}}); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}

	raw := string(c.raw)
	for _, want := range []string{
		"// Active profile by default.",           // comment preserved
		"// Where the private-repo is cloned",     // comment preserved
		`"archive"`,                               // new profile key present
		`"/tmp/archive-repo"`,                     // new profile dir
		`"/tmp/archive-scans"`,                    // new scan root
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("missing %q in patched raw:\n%s", want, raw)
		}
	}

	// WriteFile + reload round-trip still parses.
	if err := c.WriteFile(); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	c2, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := c2.Profiles["archive"]; !ok {
		t.Errorf("archive profile missing after round-trip")
	}
}

func TestMVP_02_Config_BootstrapEmpty_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.jsonc")
	c := BootstrapEmpty(path)
	if err := c.AddProfile("work", Profile{Dir: "/tmp/empty-work"}); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}
	if err := c.WriteFile(); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"work"`) {
		t.Errorf("work key missing in written file:\n%s", data)
	}

	c2, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c2.DefaultProfile != "work" {
		t.Errorf("default_profile: got %q want work (should auto-set on first profile)", c2.DefaultProfile)
	}
	if got := c2.Profiles["work"].Dir; got != "/tmp/empty-work" {
		t.Errorf("work.dir: got %q", got)
	}
}

func TestMVP_02_Config_SetDefaultProfile(t *testing.T) {
	path := writeTempConfig(t, sampleJSONC)
	c, _ := Load(path)

	if err := c.SetDefaultProfile("personal"); err != nil {
		t.Fatalf("SetDefaultProfile: %v", err)
	}
	if c.DefaultProfile != "personal" {
		t.Errorf("in-memory: got %q", c.DefaultProfile)
	}
	if err := c.SetDefaultProfile("nosuch"); err == nil {
		t.Error("expected error for unknown profile")
	}

	_ = c.WriteFile()
	c2, _ := Load(path)
	if c2.DefaultProfile != "personal" {
		t.Errorf("persisted: got %q", c2.DefaultProfile)
	}
}
