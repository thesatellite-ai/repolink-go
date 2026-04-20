// Package types holds named primitive types + constructors + validation
// used across repolink. Keeping them in one place prevents stringly-typed
// args from leaking into internal/config, internal/ent, internal/symlinker,
// internal/cli, and internal/mcp.
//
// Spec refs (docs/PROBLEM.md):
//   - MappingState: active | paused | trashed
//   - SymlinkKind:  file | dir  (both supported from v0.1)
//   - Table naming: snake_case plural (handled in ent schema, not here)
//   - Hard rule: absolute paths everywhere — no ~, no relative.
package types

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// --- ProfileName ---------------------------------------------------------

// ProfileName names a per-machine profile (e.g. "work", "personal").
// Rules: non-empty, ASCII alnum + dash/underscore, 1..64 chars.
type ProfileName string

var profileNameRx = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func NewProfileName(s string) (ProfileName, error) {
	if s == "" {
		return "", errors.New("profile name: empty")
	}
	if !profileNameRx.MatchString(s) {
		return "", fmt.Errorf("profile name %q: must match [A-Za-z0-9_-]{1,64}", s)
	}
	return ProfileName(s), nil
}

func (p ProfileName) String() string { return string(p) }

// --- AbsPath -------------------------------------------------------------

// AbsPath is a filesystem path guaranteed absolute + cleaned.
// Construction rejects empty, relative, or "~"-prefixed paths.
// Callers must expand "~" before calling NewAbsPath.
type AbsPath string

func NewAbsPath(s string) (AbsPath, error) {
	if s == "" {
		return "", errors.New("path: empty")
	}
	if strings.HasPrefix(s, "~") {
		return "", fmt.Errorf("path %q: must be absolute (expand ~ first)", s)
	}
	if !filepath.IsAbs(s) {
		return "", fmt.Errorf("path %q: must be absolute", s)
	}
	return AbsPath(filepath.Clean(s)), nil
}

func (p AbsPath) String() string { return string(p) }

// --- RepoUUID ------------------------------------------------------------

// RepoUUID is the stable UUID of a repo mapping row.
// Canonical form: 8-4-4-4-12 hex (v4). Prefix lookups happen at the CLI
// layer, not here — this type only stores a full, validated UUID.
type RepoUUID string

var uuidRx = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func NewRepoUUID(s string) (RepoUUID, error) {
	if !uuidRx.MatchString(s) {
		return "", fmt.Errorf("repo uuid %q: expected 8-4-4-4-12 hex", s)
	}
	return RepoUUID(strings.ToLower(s)), nil
}

func (u RepoUUID) String() string { return string(u) }

// --- MappingState --------------------------------------------------------

type MappingState string

const (
	StateActive  MappingState = "active"
	StatePaused  MappingState = "paused"
	StateTrashed MappingState = "trashed"
)

func NewMappingState(s string) (MappingState, error) {
	switch MappingState(s) {
	case StateActive, StatePaused, StateTrashed:
		return MappingState(s), nil
	}
	return "", fmt.Errorf("mapping state %q: must be active|paused|trashed", s)
}

func (m MappingState) Valid() bool {
	_, err := NewMappingState(string(m))
	return err == nil
}

// --- SymlinkKind ---------------------------------------------------------

type SymlinkKind string

const (
	KindFile SymlinkKind = "file"
	KindDir  SymlinkKind = "dir"
)

func NewSymlinkKind(s string) (SymlinkKind, error) {
	switch SymlinkKind(s) {
	case KindFile, KindDir:
		return SymlinkKind(s), nil
	}
	return "", fmt.Errorf("symlink kind %q: must be file|dir", s)
}

// --- Hostname ------------------------------------------------------------

// Hostname is the os.Hostname() of the machine that created a Profile row.
// Informational only — stored for diagnostics. Validated loosely.
type Hostname string

func NewHostname(s string) (Hostname, error) {
	if s == "" {
		return "", errors.New("hostname: empty")
	}
	if len(s) > 253 {
		return "", fmt.Errorf("hostname %q: > 253 chars", s)
	}
	return Hostname(s), nil
}

// --- JSONErrorCode -------------------------------------------------------

// JSONErrorCode is the stable, append-only set of machine-readable error
// codes returned in `--json` output. New codes may be added; existing codes
// must never change meaning.
type JSONErrorCode string

const (
	ErrCollision       JSONErrorCode = "COLLISION"
	ErrUUIDAmbiguous   JSONErrorCode = "UUID_AMBIGUOUS"
	ErrUUIDNotFound    JSONErrorCode = "UUID_NOT_FOUND"
	ErrConfigInvalid   JSONErrorCode = "CONFIG_INVALID"
	ErrProfileUnknown  JSONErrorCode = "PROFILE_UNKNOWN"
	ErrDirNotFound     JSONErrorCode = "DIR_NOT_FOUND"
	ErrSourceMissing   JSONErrorCode = "SOURCE_MISSING"
	ErrTargetClobber   JSONErrorCode = "TARGET_CLOBBER"
	ErrNotASymlink     JSONErrorCode = "NOT_A_SYMLINK"
	ErrDBLocked        JSONErrorCode = "DB_LOCKED"
	ErrDBMigrate       JSONErrorCode = "DB_MIGRATE"
	ErrUnknown         JSONErrorCode = "UNKNOWN"
)
