package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMVP_18_Config_AddProfile_And_List(t *testing.T) {
	root, _ := newTestApp(t)
	dir1 := filepath.Join(t.TempDir(), "pr1")
	dir2 := filepath.Join(t.TempDir(), "pr2")
	_ = os.MkdirAll(dir1, 0o700)
	_ = os.MkdirAll(dir2, 0o700)

	// Bootstrap config via setup, then add a second profile via config cmd.
	_ = runWith(root, "setup", "--dir", dir1, "--name", "work", "--make-default", "--json")
	if err := runWith(root, "config", "--add-profile", "personal", "--dir", dir2, "--json"); err != nil {
		t.Fatalf("add-profile: %v", err)
	}

	out, _ := runWithCapture(root, "config", "--list", "--json")
	var env struct {
		Data struct {
			DefaultProfile string `json:"default_profile"`
			Profiles       map[string]struct {
				Dir string `json:"dir"`
			} `json:"profiles"`
		} `json:"data"`
	}
	_ = json.Unmarshal(out, &env)
	if env.Data.DefaultProfile != "work" {
		t.Errorf("default_profile: got %q", env.Data.DefaultProfile)
	}
	if env.Data.Profiles["personal"].Dir != dir2 {
		t.Errorf("personal.dir: got %q", env.Data.Profiles["personal"].Dir)
	}
}

func TestMVP_18_Config_SetDefaultProfile(t *testing.T) {
	root, _ := newTestApp(t)
	d1 := filepath.Join(t.TempDir(), "a")
	d2 := filepath.Join(t.TempDir(), "b")
	_ = os.MkdirAll(d1, 0o700)
	_ = os.MkdirAll(d2, 0o700)
	_ = runWith(root, "setup", "--dir", d1, "--name", "first", "--make-default", "--json")
	_ = runWith(root, "config", "--add-profile", "second", "--dir", d2, "--json")

	if err := runWith(root, "config", "--set", "default_profile", "second", "--json"); err != nil {
		t.Fatalf("set default_profile: %v", err)
	}
	out, _ := runWithCapture(root, "config", "--get", "default_profile", "--json")
	var env struct {
		Data struct {
			Value string `json:"value"`
		} `json:"data"`
	}
	_ = json.Unmarshal(out, &env)
	if env.Data.Value != "second" {
		t.Errorf("get default_profile: got %q want second", env.Data.Value)
	}

	if err := runWith(root, "config", "--set", "default_profile", "ghost", "--json"); err == nil {
		t.Error("expected error for unknown profile")
	}
}

func TestMVP_18_Config_RejectArraySet(t *testing.T) {
	root, _ := newTestApp(t)
	d := filepath.Join(t.TempDir(), "pr")
	_ = os.MkdirAll(d, 0o700)
	_ = runWith(root, "setup", "--dir", d, "--name", "w", "--make-default", "--json")

	if err := runWith(root, "config", "--set", "scan_roots", "/foo", "--json"); err == nil {
		t.Error("expected array-key rejection")
	}
}

func TestMVP_18_Config_UnknownKeyHints(t *testing.T) {
	root, _ := newTestApp(t)
	d := filepath.Join(t.TempDir(), "pr")
	_ = os.MkdirAll(d, 0o700)
	_ = runWith(root, "setup", "--dir", d, "--name", "w", "--make-default", "--json")

	buf := &strings.Builder{}
	root.app.Stderr = buf
	if err := runWith(root, "config", "--set", "default_profle", "w", "--json"); err == nil {
		t.Error("expected unknown-key error")
	}
}

func TestMVP_18_Config_AddAndRemoveScanRoot(t *testing.T) {
	root, _ := newTestApp(t)
	d := filepath.Join(t.TempDir(), "pr")
	_ = os.MkdirAll(d, 0o700)
	scans := filepath.Join(t.TempDir(), "scans")
	_ = os.MkdirAll(scans, 0o700)
	_ = runWith(root, "setup", "--dir", d, "--name", "w", "--make-default", "--json")

	if err := runWith(root, "config", "--add-scan-root", scans, "--json"); err != nil {
		t.Fatalf("add-scan-root: %v", err)
	}
	out, _ := runWithCapture(root, "config", "--get", "scan_roots", "--json")
	var env struct {
		Data struct {
			Value []string `json:"value"`
		} `json:"data"`
	}
	_ = json.Unmarshal(out, &env)
	if len(env.Data.Value) != 1 || env.Data.Value[0] != scans {
		t.Errorf("scan_roots after add: %v", env.Data.Value)
	}

	if err := runWith(root, "config", "--remove-scan-root", scans, "--json"); err != nil {
		t.Fatalf("remove-scan-root: %v", err)
	}
	out2, _ := runWithCapture(root, "config", "--get", "scan_roots", "--json")
	_ = json.Unmarshal(out2, &env)
	if len(env.Data.Value) != 0 {
		t.Errorf("scan_roots after remove: %v", env.Data.Value)
	}
}

func TestMVP_18_Config_PreservesCommentsOnWrite(t *testing.T) {
	root, cfgPath := newTestApp(t)
	d1 := filepath.Join(t.TempDir(), "a")
	d2 := filepath.Join(t.TempDir(), "b")
	_ = os.MkdirAll(d1, 0o700)
	_ = os.MkdirAll(d2, 0o700)
	_ = runWith(root, "setup", "--dir", d1, "--name", "a", "--make-default", "--json")

	// After several writes comments in the initial jsonc should still be present.
	_ = runWith(root, "config", "--add-profile", "b", "--dir", d2, "--json")
	_ = runWith(root, "config", "--set", "default_profile", "b", "--json")

	raw, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(raw), "// Active profile") {
		t.Errorf("top-level comment stripped: %s", raw)
	}
}
