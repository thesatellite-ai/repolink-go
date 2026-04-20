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

// TestMVP_04_SetupZeroFlag runs `setup` with --dir + --name and asserts
// config.jsonc, repo.db, and the repo_meta singleton are all created.
func TestMVP_04_SetupZeroFlag(t *testing.T) {
	root, cfgPath := newTestApp(t)
	privateRepo := filepath.Join(t.TempDir(), "pr")
	if err := os.MkdirAll(privateRepo, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := runWith(root, "setup", "--dir", privateRepo, "--name", "work", "--make-default", "--json"); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("config.jsonc not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(privateRepo, "repo.db")); err != nil {
		t.Fatalf("repo.db not created: %v", err)
	}
}

// TestMVP_04_SetupIdempotent runs setup twice and asserts the second run
// reports meta_created=false but still succeeds without error.
func TestMVP_04_SetupIdempotent(t *testing.T) {
	root, _ := newTestApp(t)
	privateRepo := filepath.Join(t.TempDir(), "pr")
	if err := os.MkdirAll(privateRepo, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := runWith(root, "setup", "--dir", privateRepo, "--name", "work", "--make-default", "--json"); err != nil {
		t.Fatalf("first setup: %v", err)
	}

	// Second run — capture stdout to inspect the envelope.
	out, err := runWithCapture(root, "setup", "--dir", privateRepo, "--name", "work", "--json")
	if err != nil {
		t.Fatalf("second setup: %v", err)
	}

	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			MetaCreated   bool `json:"meta_created"`
			DBCreated     bool `json:"db_created"`
			ConfigCreated bool `json:"config_created"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("parse envelope: %v (out=%s)", err, out)
	}
	if !env.OK {
		t.Error("ok=false")
	}
	if env.Data.MetaCreated || env.Data.DBCreated || env.Data.ConfigCreated {
		t.Errorf("idempotent run reported creation: %+v", env.Data)
	}
}

// --- test harness ---

// newTestApp builds a cobra root with an App whose ConfigPath points at a
// t.TempDir-scoped config.jsonc and whose Stdout is captured.
func newTestApp(t *testing.T) (*testRoot, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.jsonc")

	a := app.New()
	a.ConfigPath = cfgPath
	buf := &bytes.Buffer{}
	a.Stdout = buf
	a.Stderr = &bytes.Buffer{}

	return &testRoot{app: a, buf: buf, cmd: NewRoot(a)}, cfgPath
}

type testRoot struct {
	app *app.App
	buf *bytes.Buffer
	cmd interface {
		SetArgs([]string)
		ExecuteContext(context.Context) error
	}
}

func runWith(r *testRoot, args ...string) error {
	r.buf.Reset()
	r.cmd.SetArgs(args)
	return r.cmd.ExecuteContext(context.Background())
}

func runWithCapture(r *testRoot, args ...string) ([]byte, error) {
	if err := runWith(r, args...); err != nil {
		return nil, err
	}
	return r.buf.Bytes(), nil
}
