// Package symlinker is the Compute → Plan → Apply engine for every
// repolink mutation that touches the filesystem (hygiene gate G3).
// Pure functions in Compute; all fs I/O quarantined to Apply +
// RemoveSymlink. Callers (link, sync, cleanup, pause, resume, unsync)
// build Intents, call Compute, and then Apply — the same three-phase
// shape every time.
package symlinker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ActionKind classifies what Apply will do for one Intent.
type ActionKind int

const (
	// ActCreate — no entry at target; create the symlink.
	ActCreate ActionKind = iota
	// ActSkip — target already points at source; no-op.
	ActSkip
	// ActReplace — existing symlink points elsewhere; rewrite it.
	ActReplace
	// ActCollision — a non-symlink (real file/dir) occupies target; refuse.
	ActCollision
	// ActSourceMissing — source path does not exist; refuse.
	ActSourceMissing
	// ActRemove — Apply will remove this symlink (cleanup / pause / unsync).
	ActRemove
)

func (k ActionKind) String() string {
	switch k {
	case ActCreate:
		return "create"
	case ActSkip:
		return "skip"
	case ActReplace:
		return "replace"
	case ActCollision:
		return "collision"
	case ActSourceMissing:
		return "source_missing"
	case ActRemove:
		return "remove"
	}
	return "unknown"
}

// Intent is a single desired (source → target) symlink or removal request.
// All paths MUST be absolute.
type Intent struct {
	SourceAbs string // absolute path inside private-repo (ignored for Remove)
	TargetAbs string // absolute symlink placement path
	Kind      string // "dir" | "file" — informational
	MappingID string // UUID of backing repo_mappings row, for logging
	Remove    bool   // set true for removal planning (cleanup / pause / unsync)
}

// Action is the outcome of Compute for one Intent. `Reason` is a short
// human phrase for the renderer / JSON context field.
type Action struct {
	Intent Intent
	Kind   ActionKind
	Reason string
}

// Plan is an ordered set of Actions ready for Apply.
type Plan struct {
	Actions []Action
}

// Compute is pure: it inspects the filesystem once per Intent and produces
// a Plan describing what Apply would do. No mutations. Safe to call with
// --dry-run.
//
// For Remove intents, the classification is ActRemove or ActCollision
// (non-symlink at TargetAbs) or ActSkip (nothing there).
//
// For create/replace intents, the classification is ActCreate /
// ActSkip / ActReplace / ActCollision / ActSourceMissing.
func Compute(intents []Intent) Plan {
	out := make([]Action, 0, len(intents))
	for _, in := range intents {
		out = append(out, classify(in))
	}
	return Plan{Actions: out}
}

func classify(in Intent) Action {
	if !filepath.IsAbs(in.TargetAbs) || (!in.Remove && !filepath.IsAbs(in.SourceAbs)) {
		return Action{Intent: in, Kind: ActCollision, Reason: "non-absolute path"}
	}

	if in.Remove {
		info, err := os.Lstat(in.TargetAbs)
		if errors.Is(err, os.ErrNotExist) {
			return Action{Intent: in, Kind: ActSkip, Reason: "already gone"}
		}
		if err != nil {
			return Action{Intent: in, Kind: ActCollision, Reason: err.Error()}
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return Action{Intent: in, Kind: ActCollision, Reason: "not a symlink — refuse to remove"}
		}
		return Action{Intent: in, Kind: ActRemove, Reason: "symlink present"}
	}

	// Create / replace path.
	if _, err := os.Lstat(in.SourceAbs); err != nil {
		return Action{Intent: in, Kind: ActSourceMissing, Reason: err.Error()}
	}

	info, err := os.Lstat(in.TargetAbs)
	if errors.Is(err, os.ErrNotExist) {
		return Action{Intent: in, Kind: ActCreate}
	}
	if err != nil {
		return Action{Intent: in, Kind: ActCollision, Reason: err.Error()}
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return Action{Intent: in, Kind: ActCollision, Reason: "non-symlink occupies target"}
	}
	current, err := os.Readlink(in.TargetAbs)
	if err != nil {
		return Action{Intent: in, Kind: ActCollision, Reason: err.Error()}
	}
	// Symlink may be stored relative to target's dir — resolve before compare.
	resolved := current
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Clean(filepath.Join(filepath.Dir(in.TargetAbs), resolved))
	}
	if resolved == filepath.Clean(in.SourceAbs) {
		return Action{Intent: in, Kind: ActSkip, Reason: "already points at source"}
	}
	return Action{Intent: in, Kind: ActReplace, Reason: "points elsewhere: " + current}
}

// ApplyOpts tune the Apply phase. DryRun returns a populated Result
// without touching the filesystem.
//
// Note: there is deliberately no Force option here. Collisions with real
// files/dirs are surfaced as ActCollision and returned in Result.Refused;
// the caller (e.g. `repolink link --force`) is responsible for any
// pre-clearing of a real file. Keeping fs-destructive logic out of this
// package lets us enforce the CI grep-gate against `os.RemoveAll` here.
type ApplyOpts struct {
	DryRun bool
}

// Result summarizes what Apply did (or would do).
type Result struct {
	Applied  []Action
	Skipped  []Action
	Refused  []Action // collisions / source-missing not overridden by Force
}

// Apply executes a Plan. Returns a Result describing each action's outcome.
// Errors on the first unrecoverable fs error (partial results returned).
func Apply(ctx context.Context, p Plan, opts ApplyOpts) (Result, error) {
	var r Result
	for _, a := range p.Actions {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		switch a.Kind {
		case ActSkip:
			r.Skipped = append(r.Skipped, a)
		case ActCreate:
			if opts.DryRun {
				r.Applied = append(r.Applied, a)
				continue
			}
			if err := makeSymlink(a.Intent); err != nil {
				return r, fmt.Errorf("create %s: %w", a.Intent.TargetAbs, err)
			}
			r.Applied = append(r.Applied, a)
		case ActReplace:
			if opts.DryRun {
				r.Applied = append(r.Applied, a)
				continue
			}
			if err := RemoveSymlink(a.Intent.TargetAbs); err != nil {
				return r, fmt.Errorf("remove for replace %s: %w", a.Intent.TargetAbs, err)
			}
			if err := makeSymlink(a.Intent); err != nil {
				return r, fmt.Errorf("recreate %s: %w", a.Intent.TargetAbs, err)
			}
			r.Applied = append(r.Applied, a)
		case ActRemove:
			if opts.DryRun {
				r.Applied = append(r.Applied, a)
				continue
			}
			if err := RemoveSymlink(a.Intent.TargetAbs); err != nil {
				return r, fmt.Errorf("remove %s: %w", a.Intent.TargetAbs, err)
			}
			r.Applied = append(r.Applied, a)
		case ActCollision, ActSourceMissing:
			r.Refused = append(r.Refused, a)
		}
	}
	return r, nil
}

// makeSymlink ensures TargetAbs's parent dir exists, then creates the
// symlink. MappingID / Kind are purely informational.
func makeSymlink(in Intent) error {
	parent := filepath.Dir(in.TargetAbs)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	return os.Symlink(in.SourceAbs, in.TargetAbs)
}

// RemoveSymlink is the ONLY fs-delete helper used by repolink
// (safety rule S-00). It refuses to remove anything that is not a
// symlink, so we can never accidentally recurse into a source directory.
func RemoveSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("RemoveSymlink: %s is not a symlink (mode=%v)", path, info.Mode())
	}
	return os.Remove(path)
}
