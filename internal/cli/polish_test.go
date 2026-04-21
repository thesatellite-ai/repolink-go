package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/khanakia/repolink-go/internal/app"
)

// setupLinkedRepoWithIdentity is like setupLinkedRepo but writes a [user]
// section in the consumer repo's .git/config so ReadIdentity picks up a
// non-empty Email/Name.
func setupLinkedRepoWithIdentity(t *testing.T, linkName, email, name string) (*testRoot, string) {
	t.Helper()
	a := app.New()
	buf := &bytes.Buffer{}
	cfgDir := t.TempDir()
	a.ConfigPath = filepath.Join(cfgDir, "config.jsonc")
	a.Stdout = buf
	a.Stderr = &bytes.Buffer{}
	root := &testRoot{app: a, buf: buf, cmd: NewRoot(a)}

	workspace := t.TempDir()
	privateRepo := filepath.Join(workspace, "pr")
	consumer := filepath.Join(workspace, "consumer")
	src := filepath.Join(privateRepo, linkName)
	for _, d := range []string{privateRepo, consumer, src, filepath.Join(consumer, "research")} {
		_ = os.MkdirAll(d, 0o700)
	}
	gitDir := filepath.Join(consumer, ".git")
	_ = os.MkdirAll(gitDir, 0o700)
	body := "[user]\n\temail = " + email + "\n\tname = " + name + "\n" +
		"[remote \"origin\"]\n\turl = git@github.com:khanakia/abc.git\n"
	_ = os.WriteFile(filepath.Join(gitDir, "config"), []byte(body), 0o600)

	_ = runWith(root, "setup", "--dir", privateRepo, "--name", "work", "--make-default", "--json")
	chdirTo(t, consumer)
	if err := runWith(root, "link", linkName, "research", "--json"); err != nil {
		t.Fatalf("link: %v", err)
	}
	return root, consumer
}

func TestPolish_SetupWritesSidecarGitignore(t *testing.T) {
	root, _ := newTestApp(t)
	pr := filepath.Join(t.TempDir(), "pr")
	_ = os.MkdirAll(pr, 0o700)
	_ = runWith(root, "setup", "--dir", pr, "--name", "w", "--make-default", "--json")

	data, err := os.ReadFile(filepath.Join(pr, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	for _, pat := range []string{"repo.db-wal", "repo.db-shm"} {
		if !strings.Contains(string(data), pat) {
			t.Errorf(".gitignore missing %q: %s", pat, data)
		}
	}

	// Re-run should be idempotent — doesn't duplicate entries.
	_ = runWith(root, "setup", "--dir", pr, "--name", "w", "--json")
	data2, _ := os.ReadFile(filepath.Join(pr, ".gitignore"))
	if strings.Count(string(data2), "repo.db-wal") != 1 {
		t.Errorf("duplicate .gitignore entries: %s", data2)
	}
}

func TestPolish_LinkStampsCreatedBy(t *testing.T) {
	root, _ := setupLinkedRepoWithIdentity(t, "notes", "amank@example.com", "Aman K")
	out, _ := runWithCapture(root, "map", "list", "--long", "--json")
	var env struct {
		Data struct {
			Rows []struct {
				CreatedByEmail string `json:"created_by_email"`
				CreatedByName  string `json:"created_by_name"`
			} `json:"rows"`
		} `json:"data"`
	}
	_ = json.Unmarshal(out, &env)
	if len(env.Data.Rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(env.Data.Rows))
	}
	if env.Data.Rows[0].CreatedByEmail != "amank@example.com" {
		t.Errorf("email: got %q", env.Data.Rows[0].CreatedByEmail)
	}
	if env.Data.Rows[0].CreatedByName != "Aman K" {
		t.Errorf("name: got %q", env.Data.Rows[0].CreatedByName)
	}
}

func TestPolish_LinkForceClobbersRealFile(t *testing.T) {
	root, _ := newTestApp(t)
	workspace := t.TempDir()
	pr := filepath.Join(workspace, "pr")
	consumer := filepath.Join(workspace, "consumer")
	src := filepath.Join(pr, "notes")
	_ = os.MkdirAll(src, 0o700)
	_ = os.MkdirAll(consumer, 0o700)
	makeFakeRepo(t, consumer, "git@github.com:khanakia/abc.git")
	// Pre-existing real file at target.
	_ = os.MkdirAll(filepath.Join(consumer, "research"), 0o700)
	_ = os.WriteFile(filepath.Join(consumer, "research", "notes"), []byte("REAL"), 0o600)

	_ = runWith(root, "setup", "--dir", pr, "--name", "w", "--make-default", "--json")
	chdirTo(t, consumer)

	// Without --force → collision error.
	if err := runWith(root, "link", "notes", "research", "--json"); err == nil {
		t.Error("expected collision error without --force")
	}
	// With --force → clobbers.
	if err := runWith(root, "link", "notes", "research", "--force", "--json"); err != nil {
		t.Fatalf("link --force: %v", err)
	}
	info, err := os.Lstat(filepath.Join(consumer, "research", "notes"))
	if err != nil {
		t.Fatalf("target missing: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("target is not a symlink")
	}
}

func TestPolish_LinkForceRefusesRealDir(t *testing.T) {
	root, _ := newTestApp(t)
	workspace := t.TempDir()
	pr := filepath.Join(workspace, "pr")
	consumer := filepath.Join(workspace, "consumer")
	_ = os.MkdirAll(filepath.Join(pr, "notes"), 0o700)
	_ = os.MkdirAll(consumer, 0o700)
	makeFakeRepo(t, consumer, "git@github.com:khanakia/abc.git")
	// Pre-existing real directory (not a file).
	_ = os.MkdirAll(filepath.Join(consumer, "research", "notes"), 0o700)

	_ = runWith(root, "setup", "--dir", pr, "--name", "w", "--make-default", "--json")
	chdirTo(t, consumer)

	if err := runWith(root, "link", "notes", "research", "--force", "--json"); err == nil {
		t.Error("expected --force to refuse real directory at target")
	}
}

func TestPolish_ConfigRenameProfile(t *testing.T) {
	root, _ := newTestApp(t)
	pr := filepath.Join(t.TempDir(), "pr")
	_ = os.MkdirAll(pr, 0o700)
	_ = runWith(root, "setup", "--dir", pr, "--name", "work", "--make-default", "--json")

	if err := runWith(root, "config", "--rename-profile", "work,personal", "--json"); err != nil {
		t.Fatalf("rename-profile: %v", err)
	}

	out, _ := runWithCapture(root, "config", "--list", "--json")
	var env struct {
		Data struct {
			DefaultProfile string `json:"default_profile"`
			Profiles       map[string]struct {
				Dir string `json:"dir"`
			} `json:"profiles"`
		} `json:"data"`
	}
	_ = json.Unmarshal(out, &env)
	if env.Data.DefaultProfile != "personal" {
		t.Errorf("default_profile not migrated: got %q", env.Data.DefaultProfile)
	}
	if _, exists := env.Data.Profiles["work"]; exists {
		t.Error("old name still present")
	}
	if _, exists := env.Data.Profiles["personal"]; !exists {
		t.Error("new name missing")
	}

	// Re-rename to an already-existing name → error.
	if err := runWith(root, "config", "--rename-profile", "personal,personal", "--json"); err == nil {
		t.Error("same-name rename should refuse")
	}
}
