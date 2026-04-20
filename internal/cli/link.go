package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/khanakia/repolink-go/internal/app"
	"github.com/khanakia/repolink-go/internal/config"
	"github.com/khanakia/repolink-go/internal/gitremote"
	"github.com/khanakia/repolink-go/internal/store"
	"github.com/khanakia/repolink-go/internal/symlinker"
)

// newLinkCmd implements MVP-05:
//
//	repolink link <src> [dest]
//	  <src>   path inside active profile's private-repo dir (relative or absolute)
//	  [dest]  optional destination inside the current consumer repo. Forms:
//	            (omitted)          → link_name = basename(src), target_rel = ""
//	            "research"         → link_name = basename(src), target_rel = "research"
//	            "research/custom"  → link_name = "custom",       target_rel = "research"
//
// Atomic: inserts DB row first, then creates fs symlink. If the symlink
// step fails, the DB row is deleted (soft rollback via hard delete).
func newLinkCmd(a *app.App) *cobra.Command {
	var (
		notes string
		force bool
	)

	cmd := &cobra.Command{
		Use:   "link <src> [dest]",
		Short: "Add one mapping + symlink in a single shot",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			dest := ""
			if len(args) == 2 {
				dest = args[1]
			}
			return runLink(cmd.Context(), a, linkOpts{
				Src:   args[0],
				Dest:  dest,
				Notes: notes,
				Force: force,
			})
		},
	}
	cmd.Flags().StringVar(&notes, "note", "", "optional free-form note stored on the mapping")
	cmd.Flags().BoolVar(&force, "force", false, "clobber existing non-symlink FILE at target (refuses directories)")
	return cmd
}

type linkOpts struct {
	Src   string
	Dest  string
	Notes string
	Force bool
}

type linkResult struct {
	MappingID string `json:"mapping_id"`
	RepoURL   string `json:"repo_url"`
	SourceAbs string `json:"source_abs"`
	SourceRel string `json:"source_rel"`
	TargetAbs string `json:"target_abs"`
	TargetRel string `json:"target_rel"`
	LinkName  string `json:"link_name"`
	Kind      string `json:"kind"`
	Created   bool   `json:"created"`
}

func runLink(ctx context.Context, a *app.App, opts linkOpts) error {
	// ------ Compute ------
	cfgPath, err := config.DefaultPath()
	if err != nil {
		return err
	}
	if a.ConfigPath != "" {
		cfgPath = a.ConfigPath
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			return fmt.Errorf("no ~/.repolink/config.jsonc — run `repolink setup` first")
		}
		return err
	}
	profName, prof, err := cfg.Resolve(a.ProfileOverride)
	if err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	repoRoot, repoURL, err := gitremote.ResolveFromCWD(cwd)
	if err != nil {
		return fmt.Errorf("detect consumer repo: %w", err)
	}

	sourceAbs, sourceRel, err := resolveSourcePath(opts.Src, prof.Dir)
	if err != nil {
		return err
	}

	srcInfo, err := os.Stat(sourceAbs)
	if err != nil {
		return fmt.Errorf("stat source %s: %w", sourceAbs, err)
	}
	kind := "dir"
	if !srcInfo.IsDir() {
		kind = "file"
	}

	targetRel, linkName, err := resolveDest(opts.Dest, sourceRel)
	if err != nil {
		return err
	}
	targetAbs := filepath.Join(repoRoot, targetRel, linkName)

	if !isInside(targetAbs, repoRoot) {
		return fmt.Errorf("dest %q escapes repo root", opts.Dest)
	}

	// ------ Plan ------
	st, err := store.OpenDB(ctx, prof.Dir)
	if err != nil {
		return fmt.Errorf("open repo.db: %w", err)
	}
	defer st.Close()

	plan := symlinker.Compute([]symlinker.Intent{{
		SourceAbs: sourceAbs,
		TargetAbs: targetAbs,
		Kind:      kind,
	}})
	switch plan.Actions[0].Kind {
	case symlinker.ActSkip:
		// Already correct on disk. Make sure DB agrees; if missing, insert.
	case symlinker.ActCreate:
	case symlinker.ActCollision:
		// Spec S-07: `--force` allows clobbering a real file, NEVER a dir.
		if opts.Force {
			if info, err := os.Lstat(targetAbs); err == nil && info.Mode().IsRegular() {
				if err := os.Remove(targetAbs); err != nil {
					return fmt.Errorf("--force: remove %s: %w", targetAbs, err)
				}
				// Re-compute now that target is clear.
				plan = symlinker.Compute([]symlinker.Intent{{
					SourceAbs: sourceAbs, TargetAbs: targetAbs, Kind: kind,
				}})
			} else {
				return fmt.Errorf("collision at %s: --force only clobbers regular files, not %s", targetAbs, plan.Actions[0].Reason)
			}
		} else {
			return fmt.Errorf("collision at %s: %s (use --force to clobber a real file)", targetAbs, plan.Actions[0].Reason)
		}
	case symlinker.ActReplace:
		return fmt.Errorf("target %s points elsewhere: %s (use `repolink unlink` then `link`)", targetAbs, plan.Actions[0].Reason)
	case symlinker.ActSourceMissing:
		return fmt.Errorf("source missing: %s", sourceAbs)
	}

	// Audit identity (best-effort — missing user.email/name is not fatal).
	ident, _ := gitremote.ReadIdentity(repoRoot)

	// ------ Apply ------
	mapping, err := st.CreateMapping(ctx, store.NewMapping{
		SourceRel:      sourceRel,
		RepoURL:        repoURL,
		TargetRel:      targetRel,
		LinkName:       linkName,
		Kind:           kind,
		Notes:          opts.Notes,
		CreatedByEmail: ident.Email,
		CreatedByName:  ident.Name,
	})
	if err != nil {
		if errors.Is(err, store.ErrCollision) {
			existing, _ := st.MappingByTarget(ctx, repoURL, targetRel, linkName)
			return fmt.Errorf("DB collision: %s/%s/%s already claimed by mapping %s (state=%s)",
				repoURL, targetRel, linkName, existing.ID, existing.State)
		}
		return fmt.Errorf("insert mapping: %w", err)
	}

	if a.DryRun {
		// Mapping row already inserted — delete it on dry-run so we don't leave state.
		_ = st.UpdateMappingState(ctx, mapping.ID, "trashed")
		return renderLink(a, linkResult{
			MappingID: mapping.ID,
			RepoURL:   repoURL,
			SourceAbs: sourceAbs,
			SourceRel: sourceRel,
			TargetAbs: targetAbs,
			TargetRel: targetRel,
			LinkName:  linkName,
			Kind:      kind,
			Created:   false,
		})
	}

	applyRes, err := symlinker.Apply(ctx, plan, symlinker.ApplyOpts{})
	if err != nil || len(applyRes.Refused) > 0 {
		// Roll back DB row: we flip to trashed rather than hard-deleting
		// so audit trail (run_logs) survives. cleanup/purge removes later.
		_ = st.UpdateMappingState(ctx, mapping.ID, "trashed")
		if err != nil {
			return fmt.Errorf("fs symlink creation failed (mapping trashed): %w", err)
		}
		return fmt.Errorf("symlink refused (mapping trashed): %+v", applyRes.Refused)
	}

	prof0, _ := st.EnsureProfile(ctx, profName, hostnameOr("unknown"))
	_ = st.LogRun(ctx, store.NewRun{
		ProfileID: prof0.ID,
		MappingID: mapping.ID,
		Op:        "link",
		Result:    "ok",
		Message:   fmt.Sprintf("%s → %s", sourceRel, targetAbs),
	})

	return renderLink(a, linkResult{
		MappingID: mapping.ID,
		RepoURL:   repoURL,
		SourceAbs: sourceAbs,
		SourceRel: sourceRel,
		TargetAbs: targetAbs,
		TargetRel: targetRel,
		LinkName:  linkName,
		Kind:      kind,
		Created:   plan.Actions[0].Kind == symlinker.ActCreate,
	})
}

// resolveSourcePath accepts either a relative source path (joined to
// profile.dir) or an absolute path that must sit inside profile.dir.
// Returns (absolute, rel-to-profile-dir). Rejects ".." escape and
// absolute paths outside the profile.
func resolveSourcePath(src, profileDir string) (string, string, error) {
	if src == "" {
		return "", "", errors.New("src: empty")
	}
	var abs string
	if filepath.IsAbs(src) {
		abs = filepath.Clean(src)
	} else {
		abs = filepath.Clean(filepath.Join(profileDir, src))
	}
	if !isInside(abs, profileDir) {
		return "", "", fmt.Errorf("src %q escapes profile dir %s", src, profileDir)
	}
	rel, err := filepath.Rel(profileDir, abs)
	if err != nil {
		return "", "", err
	}
	if rel == "." || rel == "" {
		return "", "", errors.New("src cannot be the profile dir root itself")
	}
	return abs, rel, nil
}

// resolveDest splits a dest arg into (target_rel, link_name) per the
// grammar described at the top of this file. If dest is empty, link_name
// is basename(sourceRel) and target_rel is "".
func resolveDest(dest, sourceRel string) (string, string, error) {
	if dest == "" {
		return "", filepath.Base(sourceRel), nil
	}
	clean := filepath.Clean(dest)
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", "", fmt.Errorf("dest %q: must be repo-relative (no .., no absolute)", dest)
	}
	// Heuristic: if dest has a trailing `/` or names an existing dir, treat it as
	// a directory; link_name is basename(sourceRel).
	if strings.HasSuffix(dest, string(os.PathSeparator)) || strings.HasSuffix(dest, "/") {
		return clean, filepath.Base(sourceRel), nil
	}
	dir, base := filepath.Split(clean)
	dir = strings.TrimRight(dir, string(os.PathSeparator))
	if dir == "" {
		// single segment — treat as target_rel (directory), link_name = basename(src)
		return clean, filepath.Base(sourceRel), nil
	}
	return dir, base, nil
}

func isInside(path, parent string) bool {
	rel, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(path))
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..") && rel != ".."
}

func hostnameOr(fallback string) string {
	if h, _ := os.Hostname(); h != "" {
		return h
	}
	return fallback
}

func renderLink(a *app.App, r linkResult) error {
	if a.JSON {
		return json.NewEncoder(a.Stdout).Encode(map[string]any{
			"ok":      true,
			"version": app.Version,
			"data":    r,
		})
	}
	verb := "✓ linked"
	if !r.Created {
		verb = "✓ already linked"
	}
	fmt.Fprintf(a.Stdout, "%s %s\n", verb, r.TargetAbs)
	fmt.Fprintf(a.Stdout, "  source: %s  (%s)\n", r.SourceRel, r.Kind)
	fmt.Fprintf(a.Stdout, "  repo:   %s\n", r.RepoURL)
	fmt.Fprintf(a.Stdout, "  id:     %s\n", r.MappingID)
	return nil
}
