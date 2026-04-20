package ent_test

import (
	"context"
	stdsql "database/sql"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/schema"

	"github.com/khanakia/repolink-go/internal/ent"

	_ "modernc.org/sqlite"
)

// TestMVP_ent_MigrateAndInsert opens an in-memory sqlite, runs ent migrations
// (foreign keys disabled, per spec), and inserts one row in each table.
// Verifies schema generation + migrations + UUID v7 defaults all wire up.
func TestMVP_ent_MigrateAndInsert(t *testing.T) {
	ctx := context.Background()

	// _pragma=foreign_keys(1) is required by ent's migrator; no harm since
	// our schema defines no FOREIGN KEY columns (see PROBLEM.md "Foreign
	// keys — not used anywhere").
	db, err := stdsql.Open("sqlite", "file:ent_smoke?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := ent.NewClient(ent.Driver(drv))
	defer client.Close()

	if err := client.Schema.Create(ctx, schema.WithForeignKeys(false)); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	meta, err := client.RepoMeta.Create().
		SetDisplayName("test-repo").
		Save(ctx)
	if err != nil {
		t.Fatalf("insert repo_meta: %v", err)
	}
	if meta.ID == "" || meta.PrivateRepoID == "" {
		t.Errorf("expected UUID v7 defaults, got id=%q private_repo_id=%q", meta.ID, meta.PrivateRepoID)
	}

	prof, err := client.Profile.Create().
		SetName("work").
		SetHostname("testhost").
		Save(ctx)
	if err != nil {
		t.Fatalf("insert profile: %v", err)
	}

	mapping, err := client.RepoMapping.Create().
		SetSourceRel("markdown-viewer").
		SetRepoURL("github.com/khanakia/abc").
		SetTargetRel("research").
		SetLinkName("plan").
		Save(ctx)
	if err != nil {
		t.Fatalf("insert repo_mapping: %v", err)
	}

	if _, err := client.RunLog.Create().
		SetProfileID(prof.ID).
		SetMappingID(mapping.ID).
		SetOp("link").
		Save(ctx); err != nil {
		t.Fatalf("insert run_log: %v", err)
	}

	// Collision: second row with identical (repo_url, target_rel, link_name) rejected.
	if _, err := client.RepoMapping.Create().
		SetSourceRel("different-source").
		SetRepoURL("github.com/khanakia/abc").
		SetTargetRel("research").
		SetLinkName("plan").
		Save(ctx); err == nil {
		t.Error("expected unique-index violation on duplicate (repo_url,target_rel,link_name), got nil")
	}
}
