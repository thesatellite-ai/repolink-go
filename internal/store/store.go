// Package store is the only package allowed to import internal/ent
// (hygiene gate G1). Callers talk to the Store interface; the ent-backed
// implementation lives in ent_store.go.
//
// Rationale: keeping ent off the import graph of internal/cli, internal/tui,
// internal/mcp means we can swap in Turso / libSQL / a mock without touching
// any command handler.
package store

import (
	"context"
	"time"
)

// RepoMeta is the single-row identity of a private-repo. Stable cross-machine.
type RepoMeta struct {
	ID            string
	PrivateRepoID string
	DisplayName   string
	CreatedAt     time.Time
}

// Profile is one per-machine profile row. Live default is in config.jsonc;
// this row exists for hostname tracking + run_log references.
type Profile struct {
	ID        string
	Name      string
	Hostname  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Mapping is one private-repo → consumer symlink intention.
type Mapping struct {
	ID             string
	SourceRel      string
	RepoURL        string
	TargetRel      string
	LinkName       string
	Kind           string // "dir" | "file"
	State          string // "active" | "paused" | "trashed"
	Notes          string
	CreatedByEmail string
	CreatedByName  string
	UpdatedByEmail string
	UpdatedByName  string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// NewMapping is the input to CreateMapping.
type NewMapping struct {
	SourceRel      string
	RepoURL        string
	TargetRel      string
	LinkName       string
	Kind           string
	Notes          string
	CreatedByEmail string
	CreatedByName  string
}

// MappingFilter narrows ListMappings. Zero-value = "all states, all repos".
type MappingFilter struct {
	RepoURL string   // exact match, empty = any
	States  []string // empty = all
}

// SourceRename is one mapping's new source_rel, used by UpdateMappingSources.
type SourceRename struct {
	ID           string
	NewSourceRel string
}

// NewRun is the input to LogRun.
type NewRun struct {
	ProfileID string
	MappingID string // "" for profile-scope ops (setup, config)
	Op        string
	Result    string // "ok" | "error"
	UserEmail string
	UserName  string
	Message   string
}

// Store is the abstraction over whatever backs repolink's per-DB state.
// Every method takes context.Context first (hygiene gate G5).
type Store interface {
	// Close releases the underlying DB. Idempotent.
	Close() error

	// EnsureRepoMeta inserts the singleton if missing, or returns the
	// existing row. displayName is only used on first insert.
	EnsureRepoMeta(ctx context.Context, displayName string) (RepoMeta, error)

	// GetRepoMeta returns the singleton. Errors if not yet inserted.
	GetRepoMeta(ctx context.Context) (RepoMeta, error)

	// RenameRepoMeta updates display_name (meta rename).
	RenameRepoMeta(ctx context.Context, newName string) (RepoMeta, error)

	// EnsureProfile upserts the (name, hostname) row and returns it.
	EnsureProfile(ctx context.Context, name, hostname string) (Profile, error)

	// CreateMapping inserts a new row. Returns ErrCollision if the
	// (repo_url, target_rel, link_name) triple is already claimed in this DB.
	CreateMapping(ctx context.Context, in NewMapping) (Mapping, error)

	// MappingByTarget returns the row that claims (repoURL, targetRel,
	// linkName) in this DB, or ErrNotFound. Includes all states.
	MappingByTarget(ctx context.Context, repoURL, targetRel, linkName string) (Mapping, error)

	// ListMappings returns all mappings matching filter, sorted by id (UUID v7
	// is time-sortable, so that's creation order).
	ListMappings(ctx context.Context, f MappingFilter) ([]Mapping, error)

	// UpdateMappingState transitions a row between active/paused/trashed.
	UpdateMappingState(ctx context.Context, id, state string) error

	// PurgeMapping hard-deletes one trashed mapping row. Within a single
	// transaction, first nulls run_logs.mapping_id for rows pointing at this
	// mapping (preserving the audit trail), then deletes the mapping itself.
	// Refuses rows whose state != "trashed" — caller must unlink first.
	PurgeMapping(ctx context.Context, id string) error

	// UpdateMappingSources rewrites source_rel on every mapping in one
	// transaction. Either every row is updated or none are (rollback on
	// any error). Used by `map mv` for bulk prefix renames.
	UpdateMappingSources(ctx context.Context, renames []SourceRename) error

	// LogRun appends one run_logs row.
	LogRun(ctx context.Context, in NewRun) error
}
