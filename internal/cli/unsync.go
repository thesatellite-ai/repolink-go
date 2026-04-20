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

// MVP-12 unsync — inverse of sync. Removes symlink files without changing
// any DB state. Next `sync` recreates them.
//
// Forms:
//
//	repolink unsync                 — current repo's active mappings
//	repolink unsync <id|link_name>  — single (must belong to current repo for link_name match)
//	repolink unsync --all           — sweep across EVERY repo in active profile's DB
//
// DB-read-only. CI hygiene gate will grep-check this file for ent mutations.

func newUnsyncCmd(a *app.App) *cobra.Command {
	var (
		allFlag bool
		yesFlag bool
	)
	cmd := &cobra.Command{
		Use:   "unsync [id|link_name]",
		Short: "Remove symlinks without changing DB (inverse of sync)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ident := ""
			if len(args) == 1 {
				ident = args[0]
			}
			return runUnsync(cmd.Context(), a, unsyncOpts{
				Ident: ident, All: allFlag, Yes: yesFlag,
			})
		},
	}
	cmd.Flags().BoolVar(&allFlag, "all", false, "sweep across every repo in active profile")
	cmd.Flags().BoolVar(&yesFlag, "yes", false, "skip confirmation prompt")
	return cmd
}

type unsyncOpts struct {
	Ident string
	All   bool
	Yes   bool
}

type unsyncAction struct {
	MappingID string `json:"mapping_id"`
	LinkName  string `json:"link_name"`
	TargetAbs string `json:"target_abs"`
	Action    string `json:"action"` // removed | already_gone | refused_non_symlink | dry_run_remove
	Reason    string `json:"reason,omitempty"`
}

type unsyncResult struct {
	Profile string         `json:"profile"`
	RepoURL string         `json:"repo_url,omitempty"`
	Total   int            `json:"total"`
	Removed int            `json:"removed"`
	Missing int            `json:"missing"`
	Refused int            `json:"refused"`
	DryRun  bool           `json:"dry_run"`
	Actions []unsyncAction `json:"actions"`
}

func runUnsync(ctx context.Context, a *app.App, opts unsyncOpts) error {
	if opts.Ident != "" && opts.All {
		return errors.New("unsync: <id|link_name> and --all are mutually exclusive")
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

	repoRoot, repoURL, repoErr := gitremote.ResolveFromCWD(mustCWD())

	st, err := store.OpenDB(ctx, prof.Dir)
	if err != nil {
		return err
	}
	defer st.Close()

	// Select mappings + decide their repoRoot.
	var (
		targets []store.Mapping
		roots   = map[string]string{}
	)
	switch {
	case opts.Ident != "":
		scope := ""
		if repoErr == nil {
			scope = repoURL
		}
		m, err := resolveMapping(ctx, st, opts.Ident, scope)
		if err != nil {
			return err
		}
		if m.State != "active" {
			return fmt.Errorf("mapping %s is state=%s — nothing to unsync (use `pause` or `cleanup`)", m.ID, m.State)
		}
		if repoErr != nil {
			return fmt.Errorf("unsync <id>: CWD is not inside a git repo, needed to place target path")
		}
		targets = []store.Mapping{m}
		roots[m.ID] = repoRoot
	case opts.All:
		if repoErr != nil {
			return fmt.Errorf("unsync --all: CWD is not inside a git repo (needed to derive fs roots until resolver lands): %w", repoErr)
		}
		rows, err := st.ListMappings(ctx, store.MappingFilter{States: []string{"active"}})
		if err != nil {
			return err
		}
		targets = rows
		for _, m := range rows {
			roots[m.ID] = repoRoot
		}
	default:
		if repoErr != nil {
			return fmt.Errorf("unsync: CWD is not inside a git repo (or use --all): %w", repoErr)
		}
		rows, err := st.ListMappings(ctx, store.MappingFilter{
			RepoURL: repoURL,
			States:  []string{"active"},
		})
		if err != nil {
			return err
		}
		targets = rows
		for _, m := range rows {
			roots[m.ID] = repoRoot
		}
	}

	result := unsyncResult{
		Profile: profName, RepoURL: repoURL,
		Total: len(targets), DryRun: a.DryRun,
	}

	if len(targets) == 0 {
		if a.JSON {
			return json.NewEncoder(a.Stdout).Encode(map[string]any{
				"ok":      true,
				"version": app.Version,
				"data":    result,
			})
		}
		fmt.Fprintln(a.Stdout, "nothing to unsync")
		return nil
	}

	// Confirm for --all only, unless --yes / --dry-run / machine-mode.
	if opts.All && !opts.Yes && !a.DryRun && !a.JSON && !a.NonInteractive {
		fmt.Fprintf(a.Stdout, "about to remove %d symlink file(s) across all repos (sources untouched). Proceed? [y/N] ", len(targets))
		r := bufio.NewReader(os.Stdin)
		line, _ := r.ReadString('\n')
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "y") {
			fmt.Fprintln(a.Stdout, "aborted")
			return nil
		}
	}

	intents := make([]symlinker.Intent, 0, len(targets))
	for _, m := range targets {
		intents = append(intents, symlinker.Intent{
			TargetAbs: filepath.Join(roots[m.ID], m.TargetRel, m.LinkName),
			MappingID: m.ID,
			Remove:    true,
		})
	}
	plan := symlinker.Compute(intents)
	applyRes, applyErr := symlinker.Apply(ctx, plan, symlinker.ApplyOpts{DryRun: a.DryRun})
	_ = applyRes

	for i, act := range plan.Actions {
		m := targets[i]
		ua := unsyncAction{
			MappingID: m.ID, LinkName: m.LinkName,
			TargetAbs: act.Intent.TargetAbs, Reason: act.Reason,
		}
		switch act.Kind {
		case symlinker.ActRemove:
			if a.DryRun {
				ua.Action = "dry_run_remove"
			} else {
				ua.Action = "removed"
			}
			result.Removed++
		case symlinker.ActSkip:
			ua.Action = "already_gone"
			result.Missing++
		case symlinker.ActCollision:
			ua.Action = "refused_non_symlink"
			result.Refused++
		}
		result.Actions = append(result.Actions, ua)
	}

	// Log one op=unsync row per removed (DB-read-only otherwise — this
	// logging is the single mutation we allow in this file).
	prof0, _ := st.EnsureProfile(ctx, profName, hostnameOr("unknown"))
	for _, ua := range result.Actions {
		if ua.Action == "removed" {
			_ = st.LogRun(ctx, store.NewRun{
				ProfileID: prof0.ID,
				MappingID: ua.MappingID,
				Op:        "unsync",
				Result:    "ok",
				Message:   "removed " + ua.TargetAbs,
			})
		}
	}

	if err := renderUnsync(a, result); err != nil {
		return err
	}
	if applyErr != nil {
		return applyErr
	}
	if result.Refused > 0 {
		return fmt.Errorf("%d symlink(s) refused (not a symlink)", result.Refused)
	}
	return nil
}

func renderUnsync(a *app.App, r unsyncResult) error {
	if a.JSON {
		return json.NewEncoder(a.Stdout).Encode(map[string]any{
			"ok":      r.Refused == 0,
			"version": app.Version,
			"data":    r,
		})
	}
	if r.DryRun {
		fmt.Fprintln(a.Stdout, "(dry-run — no fs changes)")
	}
	for _, ua := range r.Actions {
		marker := "-"
		switch ua.Action {
		case "removed", "dry_run_remove":
			marker = "-"
		case "already_gone":
			marker = "."
		case "refused_non_symlink":
			marker = "✗"
		}
		fmt.Fprintf(a.Stdout, "  %s %s\n", marker, ua.TargetAbs)
	}
	fmt.Fprintf(a.Stdout, "  %d removed · %d already gone · %d refused (DB unchanged — run `sync` to restore)\n",
		r.Removed, r.Missing, r.Refused)
	return nil
}
