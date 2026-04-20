package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMVP_11_PauseRemovesSymlinkKeepsRow(t *testing.T) {
	root, consumer := setupLinkedRepo(t, "notes")
	target := filepath.Join(consumer, "research", "notes")

	if err := runWith(root, "pause", "notes", "--json"); err != nil {
		t.Fatalf("pause: %v", err)
	}
	// Symlink gone; row stays (state=paused).
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Errorf("symlink not removed by pause: %v", err)
	}
	out, _ := runWithCapture(root, "map", "list", "--state", "paused", "--json")
	var env struct {
		Data struct {
			Rows []struct {
				State string `json:"state"`
			} `json:"rows"`
		} `json:"data"`
	}
	_ = json.Unmarshal(out, &env)
	if len(env.Data.Rows) != 1 || env.Data.Rows[0].State != "paused" {
		t.Errorf("expected 1 paused row, got %+v", env.Data.Rows)
	}
}

func TestMVP_11_ResumeRecreatesSymlink(t *testing.T) {
	root, consumer := setupLinkedRepo(t, "n")
	target := filepath.Join(consumer, "research", "n")

	_ = runWith(root, "pause", "n", "--json")
	if err := runWith(root, "resume", "n", "--json"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("symlink not recreated: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("target not a symlink: %v", info.Mode())
	}
}

func TestMVP_11_SyncSkipsPaused(t *testing.T) {
	root, consumer := setupLinkedRepo(t, "x")
	_ = runWith(root, "pause", "x", "--json")

	// Remove nothing — mapping is paused, target is gone. sync should NOT
	// re-create it (D-22 / paused semantics).
	out, err := runWithCapture(root, "sync", "--json")
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	var env struct {
		Data struct {
			Total         int `json:"total"`
			Created       int `json:"created"`
			PausedSkipped int `json:"paused_skipped"`
		} `json:"data"`
	}
	_ = json.Unmarshal(out, &env)
	if env.Data.PausedSkipped != 1 || env.Data.Created != 0 {
		t.Errorf("expected paused=1 created=0, got %+v", env.Data)
	}
	// Symlink must not have been recreated.
	if _, err := os.Lstat(filepath.Join(consumer, "research", "x")); err == nil {
		t.Error("sync recreated paused mapping's symlink")
	}
}

func TestMVP_11_PauseAllInRepo(t *testing.T) {
	root, consumer := setupLinkedRepo(t, "a")
	// Add a second mapping to the same repo.
	profDir := testProfileDir(t, root)
	_ = os.MkdirAll(filepath.Join(profDir, "b"), 0o700)
	if err := runWith(root, "link", "b", "research", "--json"); err != nil {
		t.Fatalf("second link: %v", err)
	}

	if err := runWith(root, "pause", "--all-in-repo", "--json"); err != nil {
		t.Fatalf("pause --all-in-repo: %v", err)
	}
	for _, name := range []string{"a", "b"} {
		if _, err := os.Lstat(filepath.Join(consumer, "research", name)); !os.IsNotExist(err) {
			t.Errorf("%s symlink not removed: %v", name, err)
		}
	}
}
