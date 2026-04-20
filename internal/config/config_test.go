package config

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleJSONC = `{
  // Active profile by default.
  "default_profile": "work",
  "profiles": {
    "work": {
      // Where the private-repo is cloned on this machine.
      "dir": "/tmp/repolink-test-work",
      "scan_roots": ["/tmp/repolink-test-scans"]
    },
    "personal": {
      "dir": "/tmp/repolink-test-personal"
    }
  }
}
`

func writeTempConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.jsonc")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

func TestMVP_02_Config_LoadPreservesComments(t *testing.T) {
	p := writeTempConfig(t, sampleJSONC)
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.DefaultProfile != "work" {
		t.Errorf("default_profile: got %q want work", c.DefaultProfile)
	}
	if got := c.Profiles["work"].Dir; got != "/tmp/repolink-test-work" {
		t.Errorf("work.dir: got %q", got)
	}
	if got := c.Profiles["work"].ScanRoots; len(got) != 1 || got[0] != "/tmp/repolink-test-scans" {
		t.Errorf("work.scan_roots: got %v", got)
	}
	// Raw bytes round-trip preserved (for later comment-preserving writes).
	if string(c.Raw()) != sampleJSONC {
		t.Errorf("Raw() did not preserve original bytes\n got (%d): %q\nwant (%d): %q",
			len(c.Raw()), string(c.Raw()), len(sampleJSONC), sampleJSONC)
	}
}

func TestMVP_02_Config_ResolveActive(t *testing.T) {
	p := writeTempConfig(t, sampleJSONC)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}

	name, prof, err := c.Resolve("")
	if err != nil || name != "work" || prof.Dir != "/tmp/repolink-test-work" {
		t.Errorf("Resolve(\"\") = %q, %+v, %v", name, prof, err)
	}

	name, _, err = c.Resolve("personal")
	if err != nil || name != "personal" {
		t.Errorf("Resolve(\"personal\") = %q, %v", name, err)
	}

	if _, _, err := c.Resolve("nosuch"); err == nil {
		t.Error("Resolve(\"nosuch\"): want err, got nil")
	}
}

func TestMVP_02_Config_NotFound(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing.jsonc"))
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if err != ErrNotFound {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestMVP_02_Config_RejectsUnknownFields(t *testing.T) {
	body := `{"default_profile":"w","profiles":{"w":{"dir":"/abs","typo_field":"x"}}}`
	p := writeTempConfig(t, body)
	if _, err := Load(p); err == nil {
		t.Error("expected decode error for unknown field")
	}
}

func TestMVP_02_Config_ValidateRequiresAbsDir(t *testing.T) {
	body := `{"default_profile":"w","profiles":{"w":{"dir":"relative/path"}}}`
	p := writeTempConfig(t, body)
	if _, err := Load(p); err == nil {
		t.Error("expected validation error for non-absolute dir")
	}
}

func TestMVP_02_Config_ValidateDefaultProfileMustExist(t *testing.T) {
	body := `{"default_profile":"ghost","profiles":{"w":{"dir":"/abs"}}}`
	p := writeTempConfig(t, body)
	if _, err := Load(p); err == nil {
		t.Error("expected error for unknown default_profile")
	}
}

func TestMVP_02_Config_ValidateKey_Allowlist(t *testing.T) {
	cases := map[string]bool{
		"default_profile":             true,
		"dir":                         true,
		"scan_roots":                  true,
		"profiles.work.dir":           true,
		"profiles.work.scan_roots":    true,
		"profiles.anything.dir":       true,
		"default_profle":              false, // typo
		"profiles.work.foo":           false, // bad leaf
		"profiles.":                   false, // empty name
		"":                            false,
	}
	for in, wantOK := range cases {
		_, hint, err := ValidateKey(in)
		got := err == nil
		if got != wantOK {
			t.Errorf("ValidateKey(%q) = err=%v, wantOK=%v (hint=%q)", in, err, wantOK, hint)
		}
	}
	// "did you mean?" hint for a typo.
	if _, hint, _ := ValidateKey("default_profle"); hint != "default_profile" {
		t.Errorf("typo hint: got %q want default_profile", hint)
	}
}
