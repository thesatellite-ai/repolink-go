package store

import "errors"

// ErrNotFound is returned when a lookup targets a row that does not exist.
var ErrNotFound = errors.New("store: not found")

// ErrCollision is returned by CreateMapping when (repo_url, target_rel,
// link_name) is already claimed in this DB.
var ErrCollision = errors.New("store: target already claimed")

// ErrSingletonPresent is returned by EnsureRepoMeta-style helpers when a
// "create once" invariant is violated. Currently unused — EnsureRepoMeta
// is idempotent — but reserved so callers can branch later.
var ErrSingletonPresent = errors.New("store: singleton already present")
