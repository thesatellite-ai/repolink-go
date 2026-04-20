package resolver

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/khanakia/repolink-go/internal/config"
	"github.com/khanakia/repolink-go/internal/store"
)

func writePin(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".repolink.jsonc"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestReadPin_Forms(t *testing.T) {
	legacy := writePin(t, `{ "profile": "work" }`)
	p, err := ReadPin(legacy)
	if err != nil || p.Kind() != "legacy" || p.Profile != "work" {
		t.Errorf("legacy: %+v %v", p, err)
	}
	legacyArr := writePin(t, `{ "profiles": ["work","personal"] }`)
	p, err = ReadPin(legacyArr)
	if err != nil || p.Kind() != "legacy" || len(p.Profiles) != 2 {
		t.Errorf("legacyArr: %+v %v", p, err)
	}
	srcs := writePin(t, `{ "sources": ["Work Notes","8f3a"] }`)
	p, err = ReadPin(srcs)
	if err != nil || p.Kind() != "sources" || len(p.Sources) != 2 {
		t.Errorf("sources: %+v %v", p, err)
	}
	mixed := writePin(t, `{ "profile": "work", "sources": ["X"] }`)
	if _, err := ReadPin(mixed); err != ErrMixedForms {
		t.Errorf("mixed: want ErrMixedForms, got %v", err)
	}
	empty := t.TempDir()
	if _, err := ReadPin(empty); err != ErrPinNotFound {
		t.Errorf("missing: want ErrPinNotFound, got %v", err)
	}
}

// setupProfile creates a temp private-repo dir, runs migrations via
// store.OpenDB, inserts repo_meta with the given display_name, returns
// (dir, privateRepoID).
func setupProfile(t *testing.T, displayName string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	st, err := store.OpenDB(ctx, dir)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	meta, err := st.EnsureRepoMeta(ctx, displayName)
	if err != nil {
		t.Fatalf("EnsureRepoMeta: %v", err)
	}
	_ = st.Close()
	return dir, meta.PrivateRepoID
}

// buildConfig builds a config.Config stub populated with the given profiles.
func buildConfig(t *testing.T, profiles map[string]config.Profile) *config.Config {
	t.Helper()
	body := `{"default_profile":"","profiles":{`
	first := true
	for name, p := range profiles {
		if !first {
			body += ","
		}
		first = false
		body += `"` + name + `":{"dir":"` + p.Dir + `"}`
	}
	body += `}}`
	// Default to first profile key.
	for name := range profiles {
		body = `{"default_profile":"` + name + `","profiles":{`
		first := true
		for n2, p2 := range profiles {
			if !first {
				body += ","
			}
			first = false
			body += `"` + n2 + `":{"dir":"` + p2.Dir + `"}`
		}
		body += `}}`
		break
	}
	path := filepath.Join(t.TempDir(), "config.jsonc")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return c
}

func TestResolve_Legacy_SingleProfile(t *testing.T) {
	dir, _ := setupProfile(t, "work-notes")
	cfg := buildConfig(t, map[string]config.Profile{"work": {Dir: dir}})

	pin := &Pin{Profile: "work"}
	got, warns, err := Resolve(context.Background(), cfg, pin)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) != 1 || got[0].ProfileName != "work" || got[0].MatchedBy != "profile" {
		t.Errorf("resolved: %+v", got)
	}
	if len(warns) != 0 {
		t.Errorf("warnings: %+v", warns)
	}
}

func TestResolve_SourcesByDisplayName(t *testing.T) {
	dirA, _ := setupProfile(t, "Work Notes")
	dirB, _ := setupProfile(t, "Personal Stuff")
	cfg := buildConfig(t, map[string]config.Profile{
		"work":     {Dir: dirA},
		"personal": {Dir: dirB},
	})

	pin := &Pin{Sources: []string{"Work Notes"}}
	got, _, err := Resolve(context.Background(), cfg, pin)
	if err != nil || len(got) != 1 || got[0].ProfileName != "work" || got[0].MatchedBy != "display_name" {
		t.Fatalf("got %+v err=%v", got, err)
	}
}

func TestResolve_SourcesByUUIDPrefix(t *testing.T) {
	dirA, uuidA := setupProfile(t, "A")
	dirB, _ := setupProfile(t, "B")
	cfg := buildConfig(t, map[string]config.Profile{
		"a": {Dir: dirA},
		"b": {Dir: dirB},
	})

	// Use enough of the UUID to be unambiguous. v7 UUIDs created in the
	// same millisecond share their first 12 hex chars (48-bit timestamp),
	// so use 20 chars to reliably distinguish.
	pin := &Pin{Sources: []string{uuidA[:20]}}
	got, _, err := Resolve(context.Background(), cfg, pin)
	if err != nil || len(got) != 1 || got[0].ProfileName != "a" || got[0].MatchedBy != "uuid" {
		t.Fatalf("got %+v err=%v", got, err)
	}
}

func TestResolve_MultiSource(t *testing.T) {
	dirA, _ := setupProfile(t, "A")
	dirB, _ := setupProfile(t, "B")
	cfg := buildConfig(t, map[string]config.Profile{
		"a": {Dir: dirA},
		"b": {Dir: dirB},
	})
	pin := &Pin{Sources: []string{"A", "B"}}
	got, _, err := Resolve(context.Background(), cfg, pin)
	if err != nil || len(got) != 2 {
		t.Fatalf("got %+v err=%v", got, err)
	}
}

func TestResolve_DisplayNameAmbiguous(t *testing.T) {
	dirA, _ := setupProfile(t, "DUPE")
	dirB, _ := setupProfile(t, "DUPE")
	cfg := buildConfig(t, map[string]config.Profile{
		"a": {Dir: dirA},
		"b": {Dir: dirB},
	})
	pin := &Pin{Sources: []string{"DUPE"}}
	if _, _, err := Resolve(context.Background(), cfg, pin); err == nil {
		t.Error("expected ambiguity error for duplicate display_names")
	}
}

func TestResolve_UnknownProfile(t *testing.T) {
	dir, _ := setupProfile(t, "W")
	cfg := buildConfig(t, map[string]config.Profile{"work": {Dir: dir}})
	pin := &Pin{Profile: "nosuch"}
	if _, _, err := Resolve(context.Background(), cfg, pin); err == nil {
		t.Error("expected error for unknown profile name")
	}
}
