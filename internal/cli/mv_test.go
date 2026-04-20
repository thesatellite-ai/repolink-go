package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMVP_14_MapMv_PrefixRewritesDBAndFS(t *testing.T) {
	root, consumer := setupLinkedRepo(t, "old-name")
	// Add a nested source + mapping so the prefix match has 2 rows.
	profDir := testProfileDir(t, root)
	_ = os.MkdirAll(filepath.Join(profDir, "old-name", "nested"), 0o700)
	// Prepare the new source path ahead of time (required for validation).
	_ = os.MkdirAll(filepath.Join(profDir, "new-name", "nested"), 0o700)

	if err := runWith(root, "link", "old-name/nested", "deep", "--json"); err != nil {
		t.Fatalf("second link: %v", err)
	}

	if err := runWith(root, "map", "mv", "old-name", "new-name", "--yes", "--json"); err != nil {
		t.Fatalf("map mv: %v", err)
	}

	// Both DB rows rewritten.
	out, _ := runWithCapture(root, "map", "list", "--json")
	var env struct {
		Data struct {
			Rows []struct {
				SourceRel string `json:"source_rel"`
			} `json:"rows"`
		} `json:"data"`
	}
	_ = json.Unmarshal(out, &env)
	got := map[string]bool{}
	for _, r := range env.Data.Rows {
		got[r.SourceRel] = true
	}
	if !got["new-name"] || !got["new-name/nested"] {
		t.Errorf("db rename incomplete: %+v", env.Data.Rows)
	}

	// Symlink in current repo points at new absolute source path.
	target := filepath.Join(consumer, "research", "old-name")
	dest, err := os.Readlink(target)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if dest != filepath.Join(profDir, "new-name") {
		t.Errorf("symlink not refreshed: %s", dest)
	}
}

func TestMVP_14_MapMv_ExactFlagSkipsDescendants(t *testing.T) {
	root, _ := setupLinkedRepo(t, "top")
	profDir := testProfileDir(t, root)
	_ = os.MkdirAll(filepath.Join(profDir, "top", "child"), 0o700)
	_ = os.MkdirAll(filepath.Join(profDir, "moved"), 0o700)

	_ = runWith(root, "link", "top/child", "inner", "--json")

	if err := runWith(root, "map", "mv", "top", "moved", "--exact", "--yes", "--json"); err != nil {
		t.Fatalf("map mv --exact: %v", err)
	}
	out, _ := runWithCapture(root, "map", "list", "--json")
	var env struct {
		Data struct {
			Rows []struct {
				SourceRel string `json:"source_rel"`
			} `json:"rows"`
		} `json:"data"`
	}
	_ = json.Unmarshal(out, &env)
	got := []string{}
	for _, r := range env.Data.Rows {
		got = append(got, r.SourceRel)
	}
	// top should be renamed to moved; top/child must stay (exact mode).
	hasMoved := false
	hasTopChild := false
	for _, s := range got {
		if s == "moved" {
			hasMoved = true
		}
		if s == "top/child" {
			hasTopChild = true
		}
	}
	if !hasMoved || !hasTopChild {
		t.Errorf("--exact wrong: got %v", got)
	}
}

func TestMVP_14_MapMv_ValidatesNewSrcExists(t *testing.T) {
	root, _ := setupLinkedRepo(t, "src")
	if err := runWith(root, "map", "mv", "src", "nonexistent-path", "--yes", "--json"); err == nil {
		t.Error("expected validation error for missing new-src, got nil")
	}
}

func TestMVP_14_MapMv_NoMatch(t *testing.T) {
	root, _ := setupLinkedRepo(t, "alpha")
	if err := runWith(root, "map", "mv", "nope", "alpha", "--yes", "--json"); err == nil {
		t.Error("expected error for no-match old-src")
	}
}
