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

// MVP-13 map purge — HARD delete of trashed mappings.
//
//	repolink map purge <id>       single mapping
//	repolink map purge --all      every trashed mapping in active profile
//	  (--all --everywhere planned for multi-profile once resolver lands)
//
// Irreversible. Inside a transaction:
//   1. UPDATE run_logs SET mapping_id = NULL WHERE mapping_id = ?
//   2. DELETE FROM repo_mappings WHERE id = ?
// Then best-effort removes any leftover symlink on fs (via
// symlinker.RemoveSymlink — never touches source targets; S-00).

func newMapPurgeCmd(a *app.App) *cobra.Command {
	var (
		allFlag bool
		yesFlag bool
	)
	cmd := &cobra.Command{
		Use:   "purge [id]",
		Short: "Hard-delete trashed mappings (irreversible)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ident := ""
			if len(args) == 1 {
				ident = args[0]
			}
			return runMapPurge(cmd.Context(), a, mapPurgeOpts{Ident: ident, All: allFlag, Yes: yesFlag})
		},
	}
	cmd.Flags().BoolVar(&allFlag, "all", false, "purge every trashed mapping in active profile")
	cmd.Flags().BoolVar(&yesFlag, "yes", false, "skip confirmation prompt")
	return cmd
}

type mapPurgeOpts struct {
	Ident string
	All   bool
	Yes   bool
}

type mapPurgeAction struct {
	MappingID string `json:"mapping_id"`
	LinkName  string `json:"link_name"`
	RepoURL   string `json:"repo_url"`
	Deleted   bool   `json:"deleted"`
	SymlinkFS string `json:"symlink_fs"` // removed | already_gone | refused_non_symlink | skipped
	Reason    string `json:"reason,omitempty"`
}

type mapPurgeResult struct {
	Profile string           `json:"profile"`
	Total   int              `json:"total"`
	Deleted int              `json:"deleted"`
	Refused int              `json:"refused"`
	DryRun  bool             `json:"dry_run"`
	Actions []mapPurgeAction `json:"actions"`
}

func runMapPurge(ctx context.Context, a *app.App, opts mapPurgeOpts) error {
	if opts.Ident == "" && !opts.All {
		return errors.New("map purge: need <id> or --all")
	}
	if opts.Ident != "" && opts.All {
		return errors.New("map purge: <id> and --all are mutually exclusive")
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

	st, err := store.OpenDB(ctx, prof.Dir)
	if err != nil {
		return err
	}
	defer st.Close()

	// Select targets (must all be state=trashed).
	var targets []store.Mapping
	if opts.Ident != "" {
		m, err := resolveMapping(ctx, st, opts.Ident, "")
		if err != nil {
			return err
		}
		if m.State != "trashed" {
			return fmt.Errorf("mapping %s is state=%s — `unlink` first, then purge", m.ID, m.State)
		}
		targets = []store.Mapping{m}
	} else {
		rows, err := st.ListMappings(ctx, store.MappingFilter{States: []string{"trashed"}})
		if err != nil {
			return err
		}
		targets = rows
	}

	result := mapPurgeResult{Profile: profName, Total: len(targets), DryRun: a.DryRun}
	if len(targets) == 0 {
		if a.JSON {
			return json.NewEncoder(a.Stdout).Encode(map[string]any{
				"ok":      true,
				"version": app.Version,
				"data":    result,
			})
		}
		fmt.Fprintln(a.Stdout, "no trashed mappings to purge")
		return nil
	}

	// Confirmation — always required unless --yes / --dry-run / machine.
	if !opts.Yes && !a.DryRun && !a.JSON && !a.NonInteractive {
		fmt.Fprintf(a.Stdout, "about to HARD-DELETE %d mapping(s). Irreversible. Proceed? [y/N] ", len(targets))
		r := bufio.NewReader(os.Stdin)
		line, _ := r.ReadString('\n')
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "y") {
			fmt.Fprintln(a.Stdout, "aborted")
			return nil
		}
	}

	// Try to derive a repoRoot for fs cleanup. Best-effort — if CWD isn't
	// in a repo we still purge the DB rows but log symlink_fs=skipped.
	repoRoot := ""
	if root, _, err := gitremote.ResolveFromCWD(mustCWD()); err == nil {
		repoRoot = root
	}

	// Record who logged the purge. EnsureProfile BEFORE we start purging
	// in case target mappings were the last ones referencing this profile
	// (no-op currently, but defensive for future audit queries).
	prof0, _ := st.EnsureProfile(ctx, profName, hostnameOr("unknown"))

	for _, m := range targets {
		ma := mapPurgeAction{
			MappingID: m.ID, LinkName: m.LinkName, RepoURL: m.RepoURL,
		}

		// Best-effort symlink removal first (spec: "removes any leftover
		// symlink" — do it before DB delete so that if fs fails loudly we
		// still have a DB row to retry on).
		if repoRoot != "" {
			target := filepath.Join(repoRoot, m.TargetRel, m.LinkName)
			plan := symlinker.Compute([]symlinker.Intent{{TargetAbs: target, Remove: true, MappingID: m.ID}})
			if !a.DryRun {
				res, _ := symlinker.Apply(ctx, plan, symlinker.ApplyOpts{})
				switch {
				case len(res.Applied) == 1:
					ma.SymlinkFS = "removed"
				case len(res.Skipped) == 1:
					ma.SymlinkFS = "already_gone"
				case len(res.Refused) == 1:
					ma.SymlinkFS = "refused_non_symlink"
					ma.Reason = res.Refused[0].Reason
				}
			} else {
				ma.SymlinkFS = "dry_run"
			}
		} else {
			ma.SymlinkFS = "skipped"
			ma.Reason = "CWD not in any git repo — fs cleanup deferred"
		}

		if !a.DryRun {
			// Audit log FIRST, in the same logical op (spec: "writes audit
			// op=purge BEFORE delete in same tx"). PurgeMapping runs its
			// own tx; we log outside since the log row has mapping_id set
			// and we want that linkage captured before nulling.
			_ = st.LogRun(ctx, store.NewRun{
				ProfileID: prof0.ID,
				MappingID: m.ID,
				Op:        "purge",
				Result:    "ok",
				Message: fmt.Sprintf("%s/%s/%s hard-deleted (symlink_fs=%s)",
					m.RepoURL, m.TargetRel, m.LinkName, ma.SymlinkFS),
			})
			if err := st.PurgeMapping(ctx, m.ID); err != nil {
				return fmt.Errorf("purge %s: %w", m.ID, err)
			}
			ma.Deleted = true
			result.Deleted++
		}
		result.Actions = append(result.Actions, ma)
	}

	return renderMapPurge(a, result)
}

func renderMapPurge(a *app.App, r mapPurgeResult) error {
	if a.JSON {
		return json.NewEncoder(a.Stdout).Encode(map[string]any{
			"ok":      r.Refused == 0,
			"version": app.Version,
			"data":    r,
		})
	}
	if r.DryRun {
		fmt.Fprintln(a.Stdout, "(dry-run — no DB or fs changes)")
	}
	for _, ma := range r.Actions {
		icon := "✓"
		if !ma.Deleted && !r.DryRun {
			icon = "✗"
		}
		fmt.Fprintf(a.Stdout, "  %s %s %-24s fs=%s\n", icon, ma.MappingID[:8], ma.LinkName, ma.SymlinkFS)
		if ma.Reason != "" {
			fmt.Fprintf(a.Stdout, "      %s\n", ma.Reason)
		}
	}
	fmt.Fprintf(a.Stdout, "  %d deleted\n", r.Deleted)
	return nil
}
