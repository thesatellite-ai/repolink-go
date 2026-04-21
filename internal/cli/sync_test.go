package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// setupLinkedRepo sets up a private-repo + consumer repo + one linked mapping
// and returns (root, consumer-path). Used by multiple sync tests.
func setupLinkedRepo(t *testing.T, linkName string) (*testRoot, string) {
	t.Helper()
	root, _ := newTestApp(t)

	workspace := t.TempDir()
	privateRepo := filepath.Join(workspace, "pr")
	consumer := filepath.Join(workspace, "consumer")
	src := filepath.Join(privateRepo, linkName)
	// Pre-create the research/ dir so `link <src> research` sees an existing
	// dir and places the symlink inside it (matches ln -s dir-semantics).
	for _, d := range []string{privateRepo, consumer, src, filepath.Join(consumer, "research")} {
		_ = os.MkdirAll(d, 0o700)
	}
	makeFakeRepo(t, consumer, "git@github.com:khanakia/abc.git")

	_ = runWith(root, "setup", "--dir", privateRepo, "--name", "work", "--make-default", "--json")
	chdirTo(t, consumer)
	if err := runWith(root, "link", linkName, "research", "--json"); err != nil {
		t.Fatalf("link setup: %v", err)
	}
	return root, consumer
}

// TestMVP_06_SyncIdempotent — re-running sync on correct state makes no fs change.
func TestMVP_06_SyncIdempotent(t *testing.T) {
	root, consumer := setupLinkedRepo(t, "mdviewer")
	target := filepath.Join(consumer, "research", "mdviewer")

	before, _ := os.Readlink(target)
	out, err := runWithCapture(root, "sync", "--json")
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	after, _ := os.Readlink(target)
	if before != after {
		t.Errorf("idempotent sync changed symlink: before=%q after=%q", before, after)
	}

	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Created int `json:"created"`
			Skipped int `json:"skipped"`
			Total   int `json:"total"`
		} `json:"data"`
	}
	_ = json.Unmarshal(out, &env)
	if !env.OK || env.Data.Created != 0 || env.Data.Skipped != 1 {
		t.Errorf("expected ok + 0 created + 1 skipped, got %+v", env.Data)
	}
}

// TestMVP_06_SyncCreatesMissing — deleting a symlink then running sync recreates it.
func TestMVP_06_SyncCreatesMissing(t *testing.T) {
	root, consumer := setupLinkedRepo(t, "notes")
	target := filepath.Join(consumer, "research", "notes")
	if err := os.Remove(target); err != nil {
		t.Fatalf("remove symlink: %v", err)
	}

	if err := runWith(root, "sync", "--json"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if info, err := os.Lstat(target); err != nil {
		t.Fatalf("symlink not recreated: %v", err)
	} else if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("recreated target is not a symlink: %v", info.Mode())
	}
}

// TestMVP_06_SyncDryRunNoFSChange — --dry-run reports plan without touching fs.
func TestMVP_06_SyncDryRunNoFSChange(t *testing.T) {
	root, consumer := setupLinkedRepo(t, "foo")
	target := filepath.Join(consumer, "research", "foo")
	_ = os.Remove(target)

	if err := runWith(root, "sync", "--dry-run", "--json"); err != nil {
		t.Fatalf("sync --dry-run: %v", err)
	}
	if _, err := os.Lstat(target); err == nil {
		t.Error("dry-run created a symlink on disk")
	}
}
