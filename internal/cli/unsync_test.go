package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMVP_12_UnsyncRemovesSymlinkDBUnchanged(t *testing.T) {
	root, consumer := setupLinkedRepo(t, "notes")
	target := filepath.Join(consumer, "research", "notes")

	if err := runWith(root, "unsync", "--json"); err != nil {
		t.Fatalf("unsync: %v", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Errorf("symlink still present after unsync: %v", err)
	}

	// Mapping row must still be state=active (unsync does not touch DB).
	out, _ := runWithCapture(root, "map", "list", "--json")
	var env struct {
		Data struct {
			Rows []struct {
				State string `json:"state"`
			} `json:"rows"`
		} `json:"data"`
	}
	_ = json.Unmarshal(out, &env)
	if len(env.Data.Rows) != 1 || env.Data.Rows[0].State != "active" {
		t.Errorf("unsync changed DB state: got %+v", env.Data.Rows)
	}
}

func TestMVP_12_SyncRestoresAfterUnsync(t *testing.T) {
	root, consumer := setupLinkedRepo(t, "x")
	target := filepath.Join(consumer, "research", "x")

	_ = runWith(root, "unsync", "--json")
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("unsync did not remove target: %v", err)
	}
	if err := runWith(root, "sync", "--json"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("sync did not restore symlink: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("restored target not a symlink: %v", info.Mode())
	}
}

func TestMVP_12_UnsyncDryRunNoFSChange(t *testing.T) {
	root, consumer := setupLinkedRepo(t, "y")
	target := filepath.Join(consumer, "research", "y")
	if err := runWith(root, "unsync", "--dry-run", "--json"); err != nil {
		t.Fatalf("unsync --dry-run: %v", err)
	}
	if _, err := os.Lstat(target); err != nil {
		t.Errorf("dry-run removed symlink: %v", err)
	}
}

func TestMVP_12_UnsyncRefusesPaused(t *testing.T) {
	// unsync <id> requires state=active; paused rows refused.
	root, _ := setupLinkedRepo(t, "p")
	_ = runWith(root, "pause", "p", "--json")
	if err := runWith(root, "unsync", "p", "--json"); err == nil {
		t.Error("expected unsync to refuse paused row, got nil")
	}
}
