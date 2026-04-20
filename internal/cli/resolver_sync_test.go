package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/khanakia/repolink-go/internal/app"
)

// TestMVP_08_MultiSourceSync_ByDisplayName exercises the full MVP-08
// resolver → sync path using .repolink.jsonc sources form.
func TestMVP_08_MultiSourceSync_ByDisplayName(t *testing.T) {
	a := app.New()
	buf := &bytes.Buffer{}
	cfgDir := t.TempDir()
	a.ConfigPath = filepath.Join(cfgDir, "config.jsonc")
	a.Stdout = buf
	a.Stderr = &bytes.Buffer{}
	root := &testRoot{app: a, buf: buf, cmd: NewRoot(a)}

	// Two private-repos.
	prA := filepath.Join(t.TempDir(), "prA")
	prB := filepath.Join(t.TempDir(), "prB")
	_ = os.MkdirAll(filepath.Join(prA, "docs"), 0o700)
	_ = os.MkdirAll(filepath.Join(prB, "tools"), 0o700)
	consumer := filepath.Join(t.TempDir(), "consumer")
	_ = os.MkdirAll(consumer, 0o700)
	makeFakeRepo(t, consumer, "git@github.com:khanakia/abc.git")

	// Setup + rename display_names.
	_ = runWith(root, "setup", "--dir", prA, "--name", "a", "--make-default", "--json")
	_ = runWith(root, "setup", "--dir", prB, "--name", "b", "--json")
	_ = runWith(root, "meta", "rename", "Work Notes", "--profile", "a", "--json")
	_ = runWith(root, "meta", "rename", "Tools", "--profile", "b", "--json")

	// Link one mapping per profile into the consumer.
	chdirTo(t, consumer)
	_ = runWith(root, "link", "docs", "research", "--profile", "a", "--json")
	_ = runWith(root, "link", "tools", "tools", "--profile", "b", "--json")

	// Drop symlinks + write pin file.
	_ = os.Remove(filepath.Join(consumer, "research", "docs"))
	_ = os.Remove(filepath.Join(consumer, "tools", "tools"))
	if err := os.WriteFile(
		filepath.Join(consumer, ".repolink.jsonc"),
		[]byte(`{ "sources": ["Work Notes", "Tools"] }`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	// Bare sync — resolver reads pin, unions mappings across two DBs.
	out, err := runWithCapture(root, "sync", "--json")
	if err != nil {
		t.Fatalf("sync: %v (out=%s)", err, out)
	}
	var env struct {
		Data struct {
			Profiles []string `json:"profiles"`
			Created  int      `json:"created"`
			Actions  []struct {
				Profile string `json:"profile"`
			} `json:"actions"`
		} `json:"data"`
	}
	_ = json.Unmarshal(out, &env)
	if len(env.Data.Profiles) != 2 {
		t.Errorf("expected 2 resolved profiles, got %v", env.Data.Profiles)
	}
	if env.Data.Created != 2 {
		t.Errorf("expected 2 creates, got %d", env.Data.Created)
	}
	// Every action should report its contributing profile.
	seenProfiles := map[string]bool{}
	for _, a := range env.Data.Actions {
		seenProfiles[a.Profile] = true
	}
	if !seenProfiles["a"] || !seenProfiles["b"] {
		t.Errorf("actions should come from both profiles, got %v", seenProfiles)
	}

	// Symlinks on disk.
	for _, rel := range []string{"research/docs", "tools/tools"} {
		if _, err := os.Lstat(filepath.Join(consumer, rel)); err != nil {
			t.Errorf("missing symlink %s: %v", rel, err)
		}
	}
	_ = context.Background()
}

// TestMVP_08_MultiSourceSync_ByCommaFlag covers `-p a,b`.
func TestMVP_08_MultiSourceSync_ByCommaFlag(t *testing.T) {
	a := app.New()
	buf := &bytes.Buffer{}
	cfgDir := t.TempDir()
	a.ConfigPath = filepath.Join(cfgDir, "config.jsonc")
	a.Stdout = buf
	a.Stderr = &bytes.Buffer{}
	root := &testRoot{app: a, buf: buf, cmd: NewRoot(a)}

	prA := filepath.Join(t.TempDir(), "prA")
	prB := filepath.Join(t.TempDir(), "prB")
	_ = os.MkdirAll(filepath.Join(prA, "x"), 0o700)
	_ = os.MkdirAll(filepath.Join(prB, "y"), 0o700)
	consumer := filepath.Join(t.TempDir(), "consumer")
	_ = os.MkdirAll(consumer, 0o700)
	makeFakeRepo(t, consumer, "git@github.com:khanakia/abc.git")

	_ = runWith(root, "setup", "--dir", prA, "--name", "a", "--make-default", "--json")
	_ = runWith(root, "setup", "--dir", prB, "--name", "b", "--json")

	chdirTo(t, consumer)
	_ = runWith(root, "link", "x", "research", "--profile", "a", "--json")
	_ = runWith(root, "link", "y", "tools", "--profile", "b", "--json")

	_ = os.Remove(filepath.Join(consumer, "research", "x"))
	_ = os.Remove(filepath.Join(consumer, "tools", "y"))

	out, err := runWithCapture(root, "sync", "--profile", "a,b", "--json")
	if err != nil {
		t.Fatalf("sync -p a,b: %v out=%s", err, out)
	}
	var env struct {
		Data struct {
			Profiles []string `json:"profiles"`
			Created  int      `json:"created"`
		} `json:"data"`
	}
	_ = json.Unmarshal(out, &env)
	if len(env.Data.Profiles) != 2 || env.Data.Created != 2 {
		t.Errorf("want 2 profiles / 2 creates, got %+v", env.Data)
	}
}

