package cli

import (
	"bufio"
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

// MVP-14 map mv — bulk rename source_rel across all matching mappings.
//
//	repolink map mv <old-src> <new-src>           prefix match (default)
//	repolink map mv <old-src> <new-src> --exact   top-level-only
//
// Steps:
//  1. Validate <new-src> exists inside active profile.dir (no source_rel
//     escape). Rejects .. / absolute / non-existent.
//  2. Find rows where source_rel == <old-src> (exact) OR starts with
//     <old-src>+"/" (prefix).
//  3. Compute the new source_rel for each matched row.
//  4. Collision check: no (repo_url, target_rel, link_name) clashes among
//     renamed rows. (Uniqueness is already indexed; we catch early here.)
//  5. Store.UpdateMappingSources in one tx.
//  6. Best-effort symlink recreation in current repo: any mapping whose
//     repo_url matches current CWD's repo gets its symlink refreshed so
//     it points at the NEW absolute source path. Others are reported
//     "needs_sync" — user runs `repolink sync` in each consumer repo.
//
// `--dry-run` prints the plan without touching DB or fs.

func newMapMvCmd(a *app.App) *cobra.Command {
	var (
		exactFlag bool
		yesFlag   bool
	)
	cmd := &cobra.Command{
		Use:   "mv <old-src> <new-src>",
		Short: "Bulk rename source_rel across mappings (prefix match; --exact for top-level only)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMapMv(cmd.Context(), a, mapMvOpts{
				Old: args[0], New: args[1], Exact: exactFlag, Yes: yesFlag,
			})
		},
	}
	cmd.Flags().BoolVar(&exactFlag, "exact", false, "match source_rel exactly (skip prefix descendants)")
	cmd.Flags().BoolVar(&yesFlag, "yes", false, "skip confirmation prompt")
	return cmd
}

type mapMvOpts struct {
	Old, New string
	Exact    bool
	Yes      bool
}

type mvAction struct {
	MappingID    string `json:"mapping_id"`
	LinkName     string `json:"link_name"`
	RepoURL      string `json:"repo_url"`
	OldSourceRel string `json:"old_source_rel"`
	NewSourceRel string `json:"new_source_rel"`
	FSAction     string `json:"fs_action"` // refreshed | needs_sync | refused_non_symlink | unchanged
	Reason       string `json:"reason,omitempty"`
}

type mapMvResult struct {
	Profile      string     `json:"profile"`
	OldPrefix    string     `json:"old"`
	NewPrefix    string     `json:"new"`
	Exact        bool       `json:"exact"`
	Matched      int        `json:"matched"`
	DBUpdated    int        `json:"db_updated"`
	FSRefreshed  int        `json:"fs_refreshed"`
	FSNeedsSync  int        `json:"fs_needs_sync"`
	FSRefused    int        `json:"fs_refused"`
	DryRun       bool       `json:"dry_run"`
	Actions      []mvAction `json:"actions"`
}

func runMapMv(ctx context.Context, a *app.App, opts mapMvOpts) error {
	if opts.Old == opts.New {
		return errors.New("map mv: old-src == new-src")
	}

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

	// Validate new path (must live inside profile.dir and exist on fs).
	if err := validateSourceRel(opts.New, prof.Dir); err != nil {
		return err
	}

	st, err := store.OpenDB(ctx, prof.Dir)
	if err != nil {
		return err
	}
	defer st.Close()

	// Gather candidate rows (any state so we don't orphan trashed rows by
	// refusing to rename them).
	all, err := st.ListMappings(ctx, store.MappingFilter{})
	if err != nil {
		return err
	}
	matched := matchSourceRel(all, opts.Old, opts.Exact)
	if len(matched) == 0 {
		return fmt.Errorf("map mv: no rows match source_rel %q (exact=%t)", opts.Old, opts.Exact)
	}

	// Plan new source_rel per row.
	renames := make([]store.SourceRename, 0, len(matched))
	result := mapMvResult{
		Profile: profName, OldPrefix: opts.Old, NewPrefix: opts.New,
		Exact: opts.Exact, Matched: len(matched), DryRun: a.DryRun,
	}
	for _, m := range matched {
		newRel := rewriteSourceRel(m.SourceRel, opts.Old, opts.New)
		renames = append(renames, store.SourceRename{ID: m.ID, NewSourceRel: newRel})
	}

	// Confirmation unless --yes / --dry-run / machine-mode.
	if !opts.Yes && !a.DryRun && !a.JSON && !a.NonInteractive {
		fmt.Fprintf(a.Stdout, "about to rename %d mapping(s) source_rel %q → %q. Proceed? [y/N] ",
			len(matched), opts.Old, opts.New)
		r := bufio.NewReader(os.Stdin)
		line, _ := r.ReadString('\n')
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "y") {
			fmt.Fprintln(a.Stdout, "aborted")
			return nil
		}
	}

	// DB mutation (transactional).
	if !a.DryRun {
		if err := st.UpdateMappingSources(ctx, renames); err != nil {
			return fmt.Errorf("rewrite source_rel: %w", err)
		}
		result.DBUpdated = len(renames)
	}

	// Best-effort symlink refresh in current repo only.
	repoRoot, repoURL, repoErr := gitremote.ResolveFromCWD(mustCWD())
	prof0, _ := st.EnsureProfile(ctx, profName, hostnameOr("unknown"))

	for i, m := range matched {
		newRel := renames[i].NewSourceRel
		act := mvAction{
			MappingID: m.ID, LinkName: m.LinkName, RepoURL: m.RepoURL,
			OldSourceRel: m.SourceRel, NewSourceRel: newRel,
		}
		if m.State != "active" {
			act.FSAction = "unchanged"
			act.Reason = "state=" + m.State
			result.Actions = append(result.Actions, act)
			continue
		}
		if repoErr != nil || m.RepoURL != repoURL {
			act.FSAction = "needs_sync"
			act.Reason = "run `repolink sync` from inside " + m.RepoURL
			result.FSNeedsSync++
			result.Actions = append(result.Actions, act)
			continue
		}

		target := filepath.Join(repoRoot, m.TargetRel, m.LinkName)
		newSrc := filepath.Join(prof.Dir, newRel)

		if !a.DryRun {
			// Remove old symlink (must be a symlink — S-00), then recreate
			// pointing at new source. If remove refuses, keep plan and
			// report as refused so user can intervene.
			if _, err := os.Lstat(target); err == nil {
				if err := symlinker.RemoveSymlink(target); err != nil {
					act.FSAction = "refused_non_symlink"
					act.Reason = err.Error()
					result.FSRefused++
					result.Actions = append(result.Actions, act)
					continue
				}
			}
			plan := symlinker.Compute([]symlinker.Intent{{
				SourceAbs: newSrc, TargetAbs: target, Kind: m.Kind,
			}})
			res, _ := symlinker.Apply(ctx, plan, symlinker.ApplyOpts{})
			switch {
			case len(res.Applied) == 1:
				act.FSAction = "refreshed"
				result.FSRefreshed++
			case len(res.Refused) == 1:
				act.FSAction = "refused_non_symlink"
				act.Reason = res.Refused[0].Reason
				result.FSRefused++
			default:
				act.FSAction = "refreshed"
				result.FSRefreshed++
			}
		} else {
			act.FSAction = "would_refresh"
		}

		_ = st.LogRun(ctx, store.NewRun{
			ProfileID: prof0.ID,
			MappingID: m.ID,
			Op:        "map_mv",
			Result:    "ok",
			Message:   fmt.Sprintf("%s → %s (fs=%s)", m.SourceRel, newRel, act.FSAction),
		})
		result.Actions = append(result.Actions, act)
	}

	return renderMapMv(a, result)
}

// validateSourceRel rejects non-existent paths or paths that escape
// profile.dir. Called with the USER-supplied `new-src`.
func validateSourceRel(rel, profileDir string) error {
	if rel == "" {
		return errors.New("new-src: empty")
	}
	if filepath.IsAbs(rel) {
		return fmt.Errorf("new-src %q: must be relative to profile dir", rel)
	}
	abs := filepath.Clean(filepath.Join(profileDir, rel))
	if !isInside(abs, profileDir) {
		return fmt.Errorf("new-src %q: escapes profile dir", rel)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("new-src %q: %w", rel, err)
	}
	_ = info
	return nil
}

// matchSourceRel returns rows whose source_rel matches `old` by the
// given semantics. exact=true → source_rel == old; exact=false → source_rel
// == old OR hasPrefix(source_rel, old + "/").
func matchSourceRel(rows []store.Mapping, old string, exact bool) []store.Mapping {
	old = filepath.Clean(old)
	var out []store.Mapping
	for _, m := range rows {
		src := filepath.Clean(m.SourceRel)
		if src == old {
			out = append(out, m)
			continue
		}
		if !exact && strings.HasPrefix(src, old+"/") {
			out = append(out, m)
		}
	}
	return out
}

// rewriteSourceRel swaps the `old` prefix for `new` in src. Assumes
// src == old OR hasPrefix(src, old+"/").
func rewriteSourceRel(src, old, newPrefix string) string {
	src = filepath.Clean(src)
	old = filepath.Clean(old)
	if src == old {
		return filepath.Clean(newPrefix)
	}
	tail := strings.TrimPrefix(src, old+"/")
	return filepath.Clean(filepath.Join(newPrefix, tail))
}

func renderMapMv(a *app.App, r mapMvResult) error {
	if a.JSON {
		return json.NewEncoder(a.Stdout).Encode(map[string]any{
			"ok":      r.FSRefused == 0,
			"version": app.Version,
			"data":    r,
		})
	}
	if r.DryRun {
		fmt.Fprintln(a.Stdout, "(dry-run — no DB or fs changes)")
	}
	fmt.Fprintf(a.Stdout, "map mv %q → %q  (exact=%t)\n", r.OldPrefix, r.NewPrefix, r.Exact)
	for _, ac := range r.Actions {
		fmt.Fprintf(a.Stdout, "  %-20s %s → %s   fs=%s\n",
			ac.LinkName, ac.OldSourceRel, ac.NewSourceRel, ac.FSAction)
		if ac.Reason != "" {
			fmt.Fprintf(a.Stdout, "      (%s)\n", ac.Reason)
		}
	}
	fmt.Fprintf(a.Stdout, "  %d matched · %d db-updated · %d fs-refreshed · %d needs-sync · %d fs-refused\n",
		r.Matched, r.DBUpdated, r.FSRefreshed, r.FSNeedsSync, r.FSRefused)
	return nil
}
