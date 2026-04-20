package gitremote

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeURL(t *testing.T) {
	cases := map[string]string{
		"https://github.com/khanakia/abc.git":   "github.com/khanakia/abc",
		"https://github.com/khanakia/abc":       "github.com/khanakia/abc",
		"git@github.com:khanakia/abc.git":       "github.com/khanakia/abc",
		"ssh://git@github.com/khanakia/abc":     "github.com/khanakia/abc",
		"github.com/khanakia/abc":               "github.com/khanakia/abc",
		"https://GITHUB.COM/khanakia/abc.git":   "github.com/khanakia/abc",
		"http://gitlab.example.com/a/b.git":     "gitlab.example.com/a/b",
	}
	for in, want := range cases {
		got, err := NormalizeURL(in)
		if err != nil || got != want {
			t.Errorf("NormalizeURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}

	bad := []string{"", "notaurl", "https://github.com/only-one-seg"}
	for _, in := range bad {
		if _, err := NormalizeURL(in); err == nil {
			t.Errorf("NormalizeURL(%q): expected err, got nil", in)
		}
	}
}

// makeRepo creates a fake git repo layout under dir with the given origin URL.
func makeRepo(t *testing.T, dir, originURL string) {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := `[core]
	repositoryformatversion = 0
[remote "origin"]
	url = ` + originURL + `
	fetch = +refs/heads/*:refs/remotes/origin/*
[remote "upstream"]
	url = https://github.com/other/ignored.git
`
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFindRepoRootAndReadOrigin(t *testing.T) {
	dir := t.TempDir()
	makeRepo(t, dir, "https://github.com/khanakia/abc.git")

	// Walk-up from a nested subdir.
	nested := filepath.Join(dir, "sub", "deeper")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := FindRepoRoot(nested)
	if err != nil {
		t.Fatalf("FindRepoRoot: %v", err)
	}
	if root != dir {
		t.Errorf("root: got %q want %q", root, dir)
	}

	url, err := ReadOriginURL(root)
	if err != nil {
		t.Fatalf("ReadOriginURL: %v", err)
	}
	if url != "https://github.com/khanakia/abc.git" {
		t.Errorf("url: got %q", url)
	}

	_, canonical, err := ResolveFromCWD(nested)
	if err != nil {
		t.Fatalf("ResolveFromCWD: %v", err)
	}
	if canonical != "github.com/khanakia/abc" {
		t.Errorf("canonical: got %q", canonical)
	}
}

func TestFindRepoRoot_NoRepo(t *testing.T) {
	// t.TempDir is under /var/folders, which has no .git ancestor.
	if _, err := FindRepoRoot(t.TempDir()); err == nil {
		t.Error("expected ErrNoGitRepo, got nil")
	}
}

func TestReadOriginURL_MissingOrigin(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	_ = os.MkdirAll(gitDir, 0o700)
	_ = os.WriteFile(filepath.Join(gitDir, "config"), []byte(`[core]
	repositoryformatversion = 0
`), 0o600)

	if _, err := ReadOriginURL(dir); err != ErrNoOriginRemote {
		t.Errorf("expected ErrNoOriginRemote, got %v", err)
	}
}

func TestFindRepoRoot_GitlinkFile(t *testing.T) {
	// Simulate a git worktree: .git is a file whose body is "gitdir: <path>".
	repo := t.TempDir()
	realGitDir := filepath.Join(t.TempDir(), "real-gitdir")
	_ = os.MkdirAll(realGitDir, 0o700)
	_ = os.WriteFile(filepath.Join(realGitDir, "config"), []byte(`[remote "origin"]
	url = git@github.com:org/repo.git
`), 0o600)

	_ = os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: "+realGitDir+"\n"), 0o600)

	root, err := FindRepoRoot(repo)
	if err != nil {
		t.Fatalf("FindRepoRoot: %v", err)
	}
	if root != repo {
		t.Errorf("root: got %q want %q", root, repo)
	}
	url, err := ReadOriginURL(root)
	if err != nil {
		t.Fatalf("ReadOriginURL: %v", err)
	}
	if url != "git@github.com:org/repo.git" {
		t.Errorf("url: got %q", url)
	}
}
