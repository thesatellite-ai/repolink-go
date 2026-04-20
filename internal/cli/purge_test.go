package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMVP_13_MapPurgeRefusesActive(t *testing.T) {
	root, _ := setupLinkedRepo(t, "a")
	// Fetch the id.
	out, _ := runWithCapture(root, "map", "list", "--json")
	id := extractFirstMappingID(t, out)

	if err := runWith(root, "map", "purge", id, "--yes", "--json"); err == nil {
		t.Error("purge on active mapping: expected error, got nil")
	}
}

func TestMVP_13_MapPurgeHardDeletesTrashed(t *testing.T) {
	root, consumer := setupLinkedRepo(t, "b")
	target := filepath.Join(consumer, "research", "b")
	out, _ := runWithCapture(root, "map", "list", "--json")
	id := extractFirstMappingID(t, out)

	_ = runWith(root, "unlink", id, "--json")
	if err := runWith(root, "map", "purge", id, "--yes", "--json"); err != nil {
		t.Fatalf("purge: %v", err)
	}

	// Row gone from DB.
	out2, _ := runWithCapture(root, "map", "list", "--state", "all", "--json")
	var env struct {
		Data struct {
			Rows []struct{} `json:"rows"`
		} `json:"data"`
	}
	_ = json.Unmarshal(out2, &env)
	if len(env.Data.Rows) != 0 {
		t.Errorf("row not deleted from DB: %d rows", len(env.Data.Rows))
	}
	// Leftover symlink gone.
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Errorf("leftover symlink: %v", err)
	}
}

func TestMVP_13_MapPurgeAllTrashed(t *testing.T) {
	root, _ := setupLinkedRepo(t, "x")
	// Add a second mapping, unlink both.
	profDir := testProfileDir(t, root)
	_ = os.MkdirAll(filepath.Join(profDir, "y"), 0o700)
	_ = runWith(root, "link", "y", "research", "--json")

	// Unlink both by link_name.
	_ = runWith(root, "unlink", "x", "--json")
	_ = runWith(root, "unlink", "y", "--json")

	if err := runWith(root, "map", "purge", "--all", "--yes", "--json"); err != nil {
		t.Fatalf("purge --all: %v", err)
	}
	out, _ := runWithCapture(root, "map", "list", "--state", "all", "--json")
	var env struct {
		Data struct {
			Rows []struct{} `json:"rows"`
		} `json:"data"`
	}
	_ = json.Unmarshal(out, &env)
	if len(env.Data.Rows) != 0 {
		t.Errorf("expected 0 rows after --all purge, got %d", len(env.Data.Rows))
	}
}

// extractFirstMappingID parses a map list JSON envelope and returns rows[0].id.
func extractFirstMappingID(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Data struct {
			Rows []struct {
				ID string `json:"id"`
			} `json:"rows"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("parse map list: %v", err)
	}
	if len(env.Data.Rows) == 0 {
		t.Fatal("map list returned no rows")
	}
	return env.Data.Rows[0].ID
}
