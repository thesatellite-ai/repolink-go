package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMVP_19_State_SingleProfileSnapshot(t *testing.T) {
	root, _ := setupLinkedRepo(t, "notes")
	out, err := runWithCapture(root, "state", "--json")
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			DefaultProfile string `json:"default_profile"`
			ActiveProfile  string `json:"active_profile"`
			CWD            struct {
				RepoURL string `json:"repo_url"`
			} `json:"cwd"`
			Profiles []struct {
				Name      string `json:"name"`
				Reachable bool   `json:"reachable"`
				Meta      *struct {
					DisplayName string `json:"display_name"`
				} `json:"meta"`
				Counts map[string]int `json:"counts"`
			} `json:"profiles"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !env.OK || env.Data.DefaultProfile != "work" {
		t.Errorf("envelope: %+v", env)
	}
	if env.Data.CWD.RepoURL != "github.com/khanakia/abc" {
		t.Errorf("cwd.repo_url: got %q", env.Data.CWD.RepoURL)
	}
	if len(env.Data.Profiles) != 1 || env.Data.Profiles[0].Counts["active"] != 1 {
		t.Errorf("profile snapshot: %+v", env.Data.Profiles)
	}
}

func TestMVP_19_State_ReadOnly_NoSideEffects(t *testing.T) {
	root, _ := newTestApp(t)
	d1 := filepath.Join(t.TempDir(), "work")
	d2 := filepath.Join(t.TempDir(), "never-setup")
	_ = os.MkdirAll(d1, 0o700)
	_ = os.MkdirAll(d2, 0o700)
	_ = runWith(root, "setup", "--dir", d1, "--name", "work", "--make-default", "--json")
	// Add a profile WITHOUT running setup on it.
	_ = runWith(root, "config", "--add-profile", "cold", "--dir", d2, "--json")

	// State must not create d2/repo.db.
	if err := runWith(root, "state", "--json"); err != nil {
		t.Fatalf("state: %v", err)
	}
	if _, err := os.Stat(filepath.Join(d2, "repo.db")); !os.IsNotExist(err) {
		t.Errorf("state created repo.db as a side effect: %v", err)
	}
}

func TestMVP_19_State_NoConfig_StillReturnsEnvelope(t *testing.T) {
	// Fresh tempdir, no setup. state should not error — it should report
	// "no config" gracefully so tooling can poll.
	root, _ := newTestApp(t)
	out, err := runWithCapture(root, "state", "--json")
	if err != nil {
		t.Fatalf("state without config: %v", err)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			DefaultProfile string `json:"default_profile"`
			Profiles       []any  `json:"profiles"`
		} `json:"data"`
	}
	_ = json.Unmarshal(out, &env)
	if !env.OK {
		t.Error("ok=false")
	}
	if env.Data.DefaultProfile != "" || len(env.Data.Profiles) != 0 {
		t.Errorf("pre-setup state should be empty: %+v", env.Data)
	}
}
