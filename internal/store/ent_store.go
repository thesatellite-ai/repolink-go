package store

import (
	"context"
	stdsql "database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	entschema "entgo.io/ent/dialect/sql/schema"

	"github.com/khanakia/repolink-go/internal/ent"
	"github.com/khanakia/repolink-go/internal/ent/profile"
	"github.com/khanakia/repolink-go/internal/ent/repomapping"

	_ "modernc.org/sqlite"
)

// entStore is the production Store impl, backed by ent + modernc/sqlite.
type entStore struct {
	client *ent.Client
	sqlDB  *stdsql.DB
}

// OpenDB opens <dir>/repo.db, creates+migrates if missing, and returns a Store.
// Does NOT insert the repo_meta singleton — call EnsureRepoMeta explicitly
// (setup does this; sync just expects it to already exist).
func OpenDB(ctx context.Context, dir string) (Store, error) {
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("OpenDB: dir must be absolute, got %q", dir)
	}
	dbPath := filepath.Join(dir, "repo.db")

	// Per PROBLEM.md SQLite pragmas section: WAL + synchronous=NORMAL +
	// busy_timeout=5s. _pragma=foreign_keys(1) is required by ent's migrator
	// even though we define no FK columns.
	dsn := "file:" + dbPath +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(1)"

	sdb, err := stdsql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", dbPath, err)
	}
	if err := sdb.PingContext(ctx); err != nil {
		sdb.Close()
		return nil, fmt.Errorf("ping sqlite %s: %w", dbPath, err)
	}

	drv := entsql.OpenDB(dialect.SQLite, sdb)
	client := ent.NewClient(ent.Driver(drv))

	if err := client.Schema.Create(ctx, entschema.WithForeignKeys(false)); err != nil {
		client.Close()
		return nil, fmt.Errorf("migrate %s: %w", dbPath, err)
	}
	return &entStore{client: client, sqlDB: sdb}, nil
}

func (s *entStore) Close() error {
	if s.client != nil {
		_ = s.client.Close()
	}
	if s.sqlDB != nil {
		return s.sqlDB.Close()
	}
	return nil
}

func (s *entStore) EnsureRepoMeta(ctx context.Context, displayName string) (RepoMeta, error) {
	existing, err := s.client.RepoMeta.Query().First(ctx)
	if err == nil {
		return RepoMeta{
			ID:            existing.ID,
			PrivateRepoID: existing.PrivateRepoID,
			DisplayName:   existing.DisplayName,
			CreatedAt:     existing.CreatedAt,
		}, nil
	}
	if !ent.IsNotFound(err) {
		return RepoMeta{}, fmt.Errorf("query repo_meta: %w", err)
	}

	// Singleton invariant: guard by counting first. Two concurrent setup
	// runs hitting the same DB are caught by SQLite busy_timeout + retry.
	count, err := s.client.RepoMeta.Query().Count(ctx)
	if err != nil {
		return RepoMeta{}, fmt.Errorf("count repo_meta: %w", err)
	}
	if count > 0 {
		return RepoMeta{}, ErrSingletonPresent
	}

	row, err := s.client.RepoMeta.Create().
		SetDisplayName(displayName).
		Save(ctx)
	if err != nil {
		return RepoMeta{}, fmt.Errorf("insert repo_meta: %w", err)
	}
	return RepoMeta{
		ID:            row.ID,
		PrivateRepoID: row.PrivateRepoID,
		DisplayName:   row.DisplayName,
		CreatedAt:     row.CreatedAt,
	}, nil
}

func (s *entStore) GetRepoMeta(ctx context.Context) (RepoMeta, error) {
	row, err := s.client.RepoMeta.Query().First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return RepoMeta{}, ErrNotFound
		}
		return RepoMeta{}, err
	}
	return RepoMeta{
		ID:            row.ID,
		PrivateRepoID: row.PrivateRepoID,
		DisplayName:   row.DisplayName,
		CreatedAt:     row.CreatedAt,
	}, nil
}

func (s *entStore) RenameRepoMeta(ctx context.Context, newName string) (RepoMeta, error) {
	if newName == "" {
		return RepoMeta{}, errors.New("RenameRepoMeta: empty name")
	}
	row, err := s.client.RepoMeta.Query().First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return RepoMeta{}, ErrNotFound
		}
		return RepoMeta{}, err
	}
	updated, err := row.Update().SetDisplayName(newName).Save(ctx)
	if err != nil {
		return RepoMeta{}, err
	}
	return RepoMeta{
		ID:            updated.ID,
		PrivateRepoID: updated.PrivateRepoID,
		DisplayName:   updated.DisplayName,
		CreatedAt:     updated.CreatedAt,
	}, nil
}

func (s *entStore) EnsureProfile(ctx context.Context, name, hostname string) (Profile, error) {
	// Schema: unique (name, hostname). Look up first; insert if missing.
	row, err := s.client.Profile.Query().
		Where(profile.Name(name), profile.Hostname(hostname)).
		First(ctx)
	if err == nil {
		return toProfile(row), nil
	}
	if !ent.IsNotFound(err) {
		return Profile{}, fmt.Errorf("query profile: %w", err)
	}

	created, err := s.client.Profile.Create().
		SetName(name).
		SetHostname(hostname).
		Save(ctx)
	if err != nil {
		return Profile{}, fmt.Errorf("insert profile: %w", err)
	}
	return toProfile(created), nil
}

func (s *entStore) CreateMapping(ctx context.Context, in NewMapping) (Mapping, error) {
	kind := in.Kind
	if kind == "" {
		kind = "dir"
	}
	create := s.client.RepoMapping.Create().
		SetSourceRel(in.SourceRel).
		SetRepoURL(in.RepoURL).
		SetTargetRel(in.TargetRel).
		SetLinkName(in.LinkName).
		SetKind(repomapping.Kind(kind))
	if in.Notes != "" {
		create = create.SetNotes(in.Notes)
	}
	if in.CreatedByEmail != "" {
		create = create.SetCreatedByEmail(in.CreatedByEmail)
	}
	if in.CreatedByName != "" {
		create = create.SetCreatedByName(in.CreatedByName)
	}
	row, err := create.Save(ctx)
	if err != nil {
		if isUniqueConstraint(err) {
			return Mapping{}, ErrCollision
		}
		return Mapping{}, fmt.Errorf("insert repo_mapping: %w", err)
	}
	return toMapping(row), nil
}

func (s *entStore) MappingByTarget(ctx context.Context, repoURL, targetRel, linkName string) (Mapping, error) {
	row, err := s.client.RepoMapping.Query().
		Where(
			repomapping.RepoURL(repoURL),
			repomapping.TargetRel(targetRel),
			repomapping.LinkName(linkName),
		).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return Mapping{}, ErrNotFound
		}
		return Mapping{}, err
	}
	return toMapping(row), nil
}

func (s *entStore) ListMappings(ctx context.Context, f MappingFilter) ([]Mapping, error) {
	q := s.client.RepoMapping.Query()
	if f.RepoURL != "" {
		q = q.Where(repomapping.RepoURL(f.RepoURL))
	}
	if len(f.States) > 0 {
		states := make([]repomapping.State, 0, len(f.States))
		for _, st := range f.States {
			states = append(states, repomapping.State(st))
		}
		q = q.Where(repomapping.StateIn(states...))
	}
	rows, err := q.Order(ent.Asc(repomapping.FieldID)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Mapping, len(rows))
	for i, r := range rows {
		out[i] = toMapping(r)
	}
	return out, nil
}

func (s *entStore) UpdateMappingState(ctx context.Context, id, state string) error {
	_, err := s.client.RepoMapping.UpdateOneID(id).SetState(repomapping.State(state)).Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

// UpdateMappingSources rewrites every mapping's source_rel in one tx.
// Used by `repolink map mv` for bulk prefix renames; all-or-nothing.
func (s *entStore) UpdateMappingSources(ctx context.Context, renames []SourceRename) error {
	if len(renames) == 0 {
		return nil
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	for _, r := range renames {
		if _, err := tx.RepoMapping.UpdateOneID(r.ID).SetSourceRel(r.NewSourceRel).Save(ctx); err != nil {
			_ = tx.Rollback()
			if ent.IsNotFound(err) {
				return ErrNotFound
			}
			return fmt.Errorf("update source_rel %s: %w", r.ID, err)
		}
	}
	return tx.Commit()
}

// PurgeMapping runs the two-step delete inside one ent transaction:
//  1. UPDATE run_logs SET mapping_id = NULL WHERE mapping_id = ?
//  2. DELETE FROM repo_mappings WHERE id = ?
//
// Refuses rows whose state != "trashed" (ErrNotFound if id doesn't exist;
// a generic error otherwise — caller is expected to have already checked
// state, but we defensively re-check).
func (s *entStore) PurgeMapping(ctx context.Context, id string) error {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	// Fetch state for safety re-check.
	m, err := tx.RepoMapping.Get(ctx, id)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsNotFound(err) {
			return ErrNotFound
		}
		return fmt.Errorf("get mapping %s: %w", id, err)
	}
	if string(m.State) != "trashed" {
		_ = tx.Rollback()
		return fmt.Errorf("refusing to purge %s: state=%s (must be trashed)", id, m.State)
	}

	// Null out run_logs.mapping_id references.
	if _, err := tx.RunLog.Update().
		Where(mappingIDEq(id)).
		ClearMappingID().
		Save(ctx); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("null run_logs.mapping_id: %w", err)
	}

	if err := tx.RepoMapping.DeleteOneID(id).Exec(ctx); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("delete mapping %s: %w", id, err)
	}
	return tx.Commit()
}

// isUniqueConstraint detects SQLite's "UNIQUE constraint failed" error
// shape bubbling up through ent. String match is unfortunate but SQLite
// doesn't expose a stable error code the way Postgres does.
func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "constraint failed: UNIQUE")
}
