package gitignore

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestUpdateBlock_CreatesFileAndBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".gitignore")
	if err := UpdateBlock(path, []string{"/docs", "/research"}); err != nil {
		t.Fatal(err)
	}
	got, _ := ReadBlock(path)
	want := []string{"/docs", "/research"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestUpdateBlock_PreservesUserLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".gitignore")
	if err := os.WriteFile(path, []byte("# user content\n*.log\nnode_modules/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := UpdateBlock(path, []string{"/research"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	s := string(data)
	for _, want := range []string{"# user content", "*.log", "node_modules/", "/research", beginMarker, endMarker} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q:\n%s", want, s)
		}
	}
}

func TestUpdateBlock_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".gitignore")
	_ = UpdateBlock(path, []string{"/a"})
	first, _ := os.ReadFile(path)
	info1, _ := os.Stat(path)
	// Re-apply same set — content should match byte-for-byte.
	_ = UpdateBlock(path, []string{"/a"})
	second, _ := os.ReadFile(path)
	if !reflect.DeepEqual(first, second) {
		t.Errorf("not idempotent:\n%s\n-----\n%s", first, second)
	}
	info2, _ := os.Stat(path)
	// Mtime may be unchanged if we short-circuited on no-op (we do).
	if info1.ModTime() != info2.ModTime() {
		t.Log("note: mtime changed despite no-op write — UpdateBlock returned early only when bytes match")
	}
}

func TestUpdateBlock_UpdatesChangedBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".gitignore")
	_ = UpdateBlock(path, []string{"/a", "/b"})
	if err := UpdateBlock(path, []string{"/b", "/c"}); err != nil {
		t.Fatal(err)
	}
	got, _ := ReadBlock(path)
	want := []string{"/b", "/c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestUpdateBlock_EmptyDesiredRemovesBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".gitignore")
	// Seed: user content + block.
	_ = os.WriteFile(path, []byte("*.log\n"), 0o644)
	_ = UpdateBlock(path, []string{"/a"})

	if err := UpdateBlock(path, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := ReadBlock(path)
	if got != nil {
		t.Errorf("block should be gone, got %v", got)
	}
	// User's *.log line still there.
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "*.log") {
		t.Errorf("user line removed: %s", data)
	}
	if strings.Contains(string(data), beginMarker) {
		t.Errorf("marker still present: %s", data)
	}
}

func TestUpdateBlock_EmptyDesiredAndEmptyFileRemovesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".gitignore")
	_ = UpdateBlock(path, []string{"/a"})           // create block
	if err := UpdateBlock(path, nil); err != nil { // remove it
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected file removed, err=%v", err)
	}
}

func TestUpdateBlock_DedupAndSort(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".gitignore")
	_ = UpdateBlock(path, []string{"/c", "/a", "/b", "/a"})
	got, _ := ReadBlock(path)
	want := []string{"/a", "/b", "/c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}
