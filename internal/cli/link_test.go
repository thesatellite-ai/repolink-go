package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// makeFakeRepo produces a minimal consumer repo with [remote "origin"] set.
func makeFakeRepo(t *testing.T, dir, originURL string) {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := "[remote \"origin\"]\n\turl = " + originURL + "\n"
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// chdirTo changes CWD to dir and restores it on test teardown.
func chdirTo(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

// TestMVP_05_LinkCreatesRowAndSymlink runs setup then link end-to-end.
func TestMVP_05_LinkCreatesRowAndSymlink(t *testing.T) {
	root, _ := newTestApp(t)

	workspace := t.TempDir()
	privateRepo := filepath.Join(workspace, "pr")
	consumer := filepath.Join(workspace, "consumer")
	source := filepath.Join(privateRepo, "markdown-viewer")
	for _, d := range []string{privateRepo, consumer, source} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	makeFakeRepo(t, consumer, "git@github.com:khanakia/abc.git")

	if err := runWith(root, "setup", "--dir", privateRepo, "--name", "work", "--make-default", "--json"); err != nil {
		t.Fatalf("setup: %v", err)
	}

	chdirTo(t, consumer)
	if err := runWith(root, "link", "markdown-viewer", "research", "--json"); err != nil {
		t.Fatalf("link: %v", err)
	}

	target := filepath.Join(consumer, "research", "markdown-viewer")
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("symlink not created at %s: %v", target, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("target is not a symlink: mode=%v", info.Mode())
	}
	dest, err := os.Readlink(target)
	if err != nil {
		t.Fatal(err)
	}
	if dest != source {
		t.Errorf("symlink target: got %q want %q", dest, source)
	}
}

// TestMVP_05_LinkCollisionRefused verifies the DB uniqueness guard — same
// (repo_url, target_rel, link_name) triple is rejected on a second link.
func TestMVP_05_LinkCollisionRefused(t *testing.T) {
	root, _ := newTestApp(t)
	workspace := t.TempDir()
	privateRepo := filepath.Join(workspace, "pr")
	consumer := filepath.Join(workspace, "consumer")
	src := filepath.Join(privateRepo, "md-viewer")
	for _, d := range []string{privateRepo, consumer, src} {
		_ = os.MkdirAll(d, 0o700)
	}
	makeFakeRepo(t, consumer, "git@github.com:khanakia/abc.git")

	_ = runWith(root, "setup", "--dir", privateRepo, "--name", "work", "--make-default", "--json")
	chdirTo(t, consumer)
	if err := runWith(root, "link", "md-viewer", "research", "--json"); err != nil {
		t.Fatalf("first link: %v", err)
	}
	if err := runWith(root, "link", "md-viewer", "research", "--json"); err == nil {
		t.Error("second link: expected collision error, got nil")
	}
}

// TestSafety_S05_LinkRejectsSourceEscape verifies src cannot escape profile dir.
func TestSafety_S05_LinkRejectsSourceEscape(t *testing.T) {
	root, _ := newTestApp(t)
	workspace := t.TempDir()
	privateRepo := filepath.Join(workspace, "pr")
	consumer := filepath.Join(workspace, "consumer")
	escape := filepath.Join(workspace, "outside")
	for _, d := range []string{privateRepo, consumer, escape} {
		_ = os.MkdirAll(d, 0o700)
	}
	makeFakeRepo(t, consumer, "git@github.com:khanakia/abc.git")

	_ = runWith(root, "setup", "--dir", privateRepo, "--name", "work", "--make-default", "--json")
	chdirTo(t, consumer)
	if err := runWith(root, "link", "../outside", "research", "--json"); err == nil {
		t.Error("expected source-escape error, got nil")
	}
}
