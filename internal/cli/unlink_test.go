package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMVP_09_UnlinkSoftDeletes(t *testing.T) {
	root, consumer := setupLinkedRepo(t, "notes")
	target := filepath.Join(consumer, "research", "notes")

	if err := runWith(root, "unlink", "notes", "--json"); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	// Symlink file must survive soft-delete.
	if _, err := os.Lstat(target); err != nil {
		t.Errorf("symlink removed by unlink (soft-delete violation): %v", err)
	}

	// State check via map list --state trashed.
	out, _ := runWithCapture(root, "map", "list", "--state", "trashed", "--json")
	var env struct {
		Data struct {
			Rows []struct {
				State string `json:"state"`
			} `json:"rows"`
		} `json:"data"`
	}
	_ = json.Unmarshal(out, &env)
	if len(env.Data.Rows) != 1 || env.Data.Rows[0].State != "trashed" {
		t.Errorf("expected 1 trashed row, got %+v", env.Data.Rows)
	}
}

func TestMVP_09_UnlinkRefusesAlreadyTrashed(t *testing.T) {
	root, _ := setupLinkedRepo(t, "foo")
	_ = runWith(root, "unlink", "foo", "--json")
	if err := runWith(root, "unlink", "foo", "--json"); err == nil {
		t.Error("second unlink: expected error (already trashed)")
	}
}

func TestMVP_10_CleanupRemovesTrashedImmediately(t *testing.T) {
	root, consumer := setupLinkedRepo(t, "ntoes2")
	target := filepath.Join(consumer, "research", "ntoes2")

	// Unlink then cleanup.
	_ = runWith(root, "unlink", "ntoes2", "--json")
	if err := runWith(root, "cleanup", "--yes", "--json"); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Errorf("symlink still on fs after cleanup: %v", err)
	}
}

func TestSafety_S00_CleanupPreservesSourceDir(t *testing.T) {
	root, consumer := setupLinkedRepo(t, "src3")
	// Create content inside the source so we can verify it survives.
	profileDir := testProfileDir(t, root)
	sourceContent := filepath.Join(profileDir, "src3", "precious.txt")
	if err := os.WriteFile(sourceContent, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(consumer, "research", "src3")
	_ = runWith(root, "unlink", "src3", "--json")
	_ = runWith(root, "cleanup", "--yes", "--json")

	if _, err := os.Stat(sourceContent); err != nil {
		t.Errorf("source content deleted via cleanup: %v", err)
	}
	if _, err := os.Lstat(target); err == nil {
		t.Error("symlink survived cleanup")
	}
}

// testProfileDir reads the active profile's dir from the config that
// testApp wrote so assertions can poke at source content.
func testProfileDir(t *testing.T, root *testRoot) string {
	t.Helper()
	out, err := runWithCapture(root, "status", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Data struct {
			Rows []struct {
				SourceRel string `json:"source_rel"`
				TargetAbs string `json:"target_abs"`
			} `json:"rows"`
		} `json:"data"`
	}
	_ = json.Unmarshal(out, &env)
	// profile.dir is the symlink's destination parent; we derive it from
	// the first row's source absolute path reconstructed here.
	// Simpler: read the config file directly.
	data, err := os.ReadFile(root.app.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	// Coarse parse — look for "dir": "<path>".
	s := string(data)
	i := 0
	for i < len(s) {
		j := indexFrom(s, `"dir":`, i)
		if j < 0 {
			break
		}
		k := indexFrom(s, `"`, j+len(`"dir":`))
		if k < 0 {
			break
		}
		l := indexFrom(s, `"`, k+1)
		if l < 0 {
			break
		}
		return s[k+1 : l]
	}
	t.Fatal("could not locate dir in config")
	return ""
}

func indexFrom(s, sub string, start int) int {
	if start < 0 || start >= len(s) {
		return -1
	}
	i := -1
	if idx := stringIndex(s[start:], sub); idx >= 0 {
		i = start + idx
	}
	return i
}

func stringIndex(s, sub string) int {
	n := len(sub)
	if n == 0 {
		return 0
	}
	for i := 0; i+n <= len(s); i++ {
		if s[i:i+n] == sub {
			return i
		}
	}
	return -1
}
