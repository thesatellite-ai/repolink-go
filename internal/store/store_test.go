package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// setupStore gives each test a fresh on-disk repo.db in a t.TempDir.
// In-memory sqlite DSN with modernc is finicky across multiple Open calls,
// so on-disk keeps behavior identical to the real setup path.
func setupStore(t *testing.T) (context.Context, Store, string) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	s, err := OpenDB(ctx, dir)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return ctx, s, dir
}

func TestMVP_store_OpenDB_CreatesFile(t *testing.T) {
	_, _, dir := setupStore(t)
	info, err := os.Stat(filepath.Join(dir, "repo.db"))
	if err != nil {
		t.Fatalf("repo.db not created: %v", err)
	}
	if info.Size() == 0 {
		t.Error("repo.db is empty — migrations did not run")
	}
}

func TestMVP_store_EnsureRepoMeta_Idempotent(t *testing.T) {
	ctx, s, _ := setupStore(t)

	first, err := s.EnsureRepoMeta(ctx, "work-notes")
	if err != nil {
		t.Fatalf("first EnsureRepoMeta: %v", err)
	}
	if first.ID == "" || first.PrivateRepoID == "" || first.DisplayName != "work-notes" {
		t.Fatalf("unexpected first meta: %+v", first)
	}

	second, err := s.EnsureRepoMeta(ctx, "IGNORED-ON-SECOND-CALL")
	if err != nil {
		t.Fatalf("second EnsureRepoMeta: %v", err)
	}
	if second.ID != first.ID || second.DisplayName != "work-notes" {
		t.Errorf("EnsureRepoMeta not idempotent: first=%+v second=%+v", first, second)
	}
}

func TestMVP_store_CreateMapping_Collision(t *testing.T) {
	ctx, s, _ := setupStore(t)
	_, _ = s.EnsureRepoMeta(ctx, "x")

	if _, err := s.CreateMapping(ctx, NewMapping{
		SourceRel: "a",
		RepoURL:   "github.com/khanakia/abc",
		TargetRel: "research",
		LinkName:  "plan",
		Kind:      "dir",
	}); err != nil {
		t.Fatalf("first CreateMapping: %v", err)
	}

	_, err := s.CreateMapping(ctx, NewMapping{
		SourceRel: "different-source",
		RepoURL:   "github.com/khanakia/abc",
		TargetRel: "research",
		LinkName:  "plan",
		Kind:      "dir",
	})
	if !errors.Is(err, ErrCollision) {
		t.Errorf("expected ErrCollision on dup (repo_url,target_rel,link_name); got %v", err)
	}
}

func TestMVP_store_ListMappings_FilterByState(t *testing.T) {
	ctx, s, _ := setupStore(t)
	_, _ = s.EnsureRepoMeta(ctx, "x")

	a, _ := s.CreateMapping(ctx, NewMapping{SourceRel: "a", RepoURL: "r1", TargetRel: "t", LinkName: "l1"})
	_, _ = s.CreateMapping(ctx, NewMapping{SourceRel: "b", RepoURL: "r1", TargetRel: "t", LinkName: "l2"})
	if err := s.UpdateMappingState(ctx, a.ID, "trashed"); err != nil {
		t.Fatalf("UpdateMappingState: %v", err)
	}

	active, err := s.ListMappings(ctx, MappingFilter{States: []string{"active"}})
	if err != nil || len(active) != 1 || active[0].LinkName != "l2" {
		t.Errorf("active filter: got %v err=%v", active, err)
	}

	all, err := s.ListMappings(ctx, MappingFilter{})
	if err != nil || len(all) != 2 {
		t.Errorf("unfiltered: got %d err=%v", len(all), err)
	}
}

func TestMVP_store_EnsureProfile_AndLogRun(t *testing.T) {
	ctx, s, _ := setupStore(t)
	_, _ = s.EnsureRepoMeta(ctx, "x")

	p1, err := s.EnsureProfile(ctx, "work", "laptop")
	if err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}
	p2, err := s.EnsureProfile(ctx, "work", "laptop")
	if err != nil || p2.ID != p1.ID {
		t.Errorf("EnsureProfile not idempotent: p1=%s p2=%s err=%v", p1.ID, p2.ID, err)
	}

	if err := s.LogRun(ctx, NewRun{ProfileID: p1.ID, Op: "setup", Result: "ok", Message: "first setup"}); err != nil {
		t.Errorf("LogRun: %v", err)
	}
}
