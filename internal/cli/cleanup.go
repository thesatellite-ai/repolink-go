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

// newCleanupCmd implements MVP-10:
//
//	repolink cleanup              — remove symlinks for trashed mappings in current repo
//	repolink cleanup <id>         — single mapping (must be trashed)
//	repolink cleanup --all --yes  — every trashed mapping across every repo in active profile
//
// Only touches symlink files, never their targets (safety rule S-00 —
// enforced by `symlinker.RemoveSymlink` which refuses non-symlinks).
func newCleanupCmd(a *app.App) *cobra.Command {
	var (
		allFlag bool
		yesFlag bool
	)
	cmd := &cobra.Command{
		Use:   "cleanup [id]",
		Short: "Remove fs symlinks for trashed mappings (never targets)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ident := ""
			if len(args) == 1 {
				ident = args[0]
			}
			return runCleanup(cmd.Context(), a, cleanupOpts{
				Ident: ident,
				All:   allFlag,
				Yes:   yesFlag,
			})
		},
	}
	cmd.Flags().BoolVar(&allFlag, "all", false, "sweep every repo in active profile (not just current)")
	cmd.Flags().BoolVar(&yesFlag, "yes", false, "skip confirmation prompt")
	return cmd
}

type cleanupOpts struct {
	Ident string
	All   bool
	Yes   bool
}

type cleanupAction struct {
	MappingID string `json:"mapping_id"`
	LinkName  string `json:"link_name"`
	TargetAbs string `json:"target_abs"`
	Action    string `json:"action"` // removed | already_gone | refused_non_symlink | dry_run
	Reason    string `json:"reason,omitempty"`
}

type cleanupResult struct {
	Profile string          `json:"profile"`
	Total   int             `json:"total"`
	Removed int             `json:"removed"`
	Missing int             `json:"missing"`
	Refused int             `json:"refused"`
	DryRun  bool            `json:"dry_run"`
	Actions []cleanupAction `json:"actions"`
}

func runCleanup(ctx context.Context, a *app.App, opts cleanupOpts) error {
	if opts.Ident != "" && opts.All {
		return errors.New("cleanup: <id> and --all are mutually exclusive")
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

	// Select target mappings.
	targets, repoRoots, err := selectCleanupTargets(ctx, st, opts)
	if err != nil {
		return err
	}

	if len(targets) == 0 {
		if a.JSON {
			return json.NewEncoder(a.Stdout).Encode(map[string]any{
				"ok":      true,
				"version": app.Version,
				"data":    cleanupResult{Profile: profName},
			})
		}
		fmt.Fprintln(a.Stdout, "no trashed mappings to clean up")
		return nil
	}

	// Confirm unless --yes / --dry-run / --json (machine-interactive).
	if !opts.Yes && !a.DryRun && !a.JSON && !a.NonInteractive {
		fmt.Fprintf(a.Stdout, "about to remove %d symlink file(s) (sources will NOT be touched). Proceed? [y/N] ", len(targets))
		r := bufio.NewReader(os.Stdin)
		line, _ := r.ReadString('\n')
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "y") {
			fmt.Fprintln(a.Stdout, "aborted")
			return nil
		}
	}

	// Build symlinker intents (Remove mode). Each intent's TargetAbs is
	// computed from (repoRoot, target_rel, link_name). repoRoot resolution
	// depends on mode (current-repo vs --all).
	result := cleanupResult{Profile: profName, DryRun: a.DryRun, Total: len(targets)}

	intents := make([]symlinker.Intent, 0, len(targets))
	for _, m := range targets {
		root := repoRoots[m.ID]
		intents = append(intents, symlinker.Intent{
			TargetAbs: filepath.Join(root, m.TargetRel, m.LinkName),
			MappingID: m.ID,
			Remove:    true,
		})
	}

	plan := symlinker.Compute(intents)
	applyRes, applyErr := symlinker.Apply(ctx, plan, symlinker.ApplyOpts{DryRun: a.DryRun})

	for i, act := range plan.Actions {
		m := targets[i]
		ca := cleanupAction{
			MappingID: m.ID,
			LinkName:  m.LinkName,
			TargetAbs: act.Intent.TargetAbs,
			Reason:    act.Reason,
		}
		switch act.Kind {
		case symlinker.ActRemove:
			ca.Action = "removed"
			if a.DryRun {
				ca.Action = "dry_run_remove"
			}
			result.Removed++
		case symlinker.ActSkip:
			ca.Action = "already_gone"
			result.Missing++
		case symlinker.ActCollision:
			ca.Action = "refused_non_symlink"
			result.Refused++
		}
		result.Actions = append(result.Actions, ca)
	}

	// Log one op=cleanup per mapping actually removed.
	prof0, _ := st.EnsureProfile(ctx, profName, hostnameOr("unknown"))
	for _, ca := range result.Actions {
		if ca.Action == "removed" {
			_ = st.LogRun(ctx, store.NewRun{
				ProfileID: prof0.ID,
				MappingID: ca.MappingID,
				Op:        "cleanup",
				Result:    "ok",
				Message:   "removed " + ca.TargetAbs,
			})
		}
	}

	if err := renderCleanup(a, result); err != nil {
		return err
	}
	_ = applyRes
	if applyErr != nil {
		return applyErr
	}
	if result.Refused > 0 {
		return fmt.Errorf("%d symlink(s) refused (not a symlink)", result.Refused)
	}
	return nil
}

// selectCleanupTargets returns the trashed mappings to operate on plus a
// per-mapping repoRoot. For current-repo mode we walk up from CWD. For
// --all we need a repoRoot for each mapping: the spec says cleanup only
// touches symlink files inside consumer repos, but we can't know every
// consumer's checkout location on this machine. For v0.1 we default the
// "--all" mode's repoRoot to "" → skip anything that doesn't start with
// a detectable absolute path, but the user's typical setup has mappings
// with target_rel + link_name relative to a consumer repo they're
// currently inside. Practical compromise:
//   - current-repo mode: walk-up for repoRoot
//   - single-id mode:    walk-up (required for file placement)
//   - --all mode:        skip mappings whose absolute target can't be
//     derived (logged as missing)
//
// When `ident` is set, only the matched mapping is returned.
func selectCleanupTargets(ctx context.Context, st store.Store, opts cleanupOpts) ([]store.Mapping, map[string]string, error) {
	trashed, err := st.ListMappings(ctx, store.MappingFilter{States: []string{"trashed"}})
	if err != nil {
		return nil, nil, err
	}

	// Single-id mode.
	if opts.Ident != "" {
		m, err := resolveMapping(ctx, st, opts.Ident, "")
		if err != nil {
			return nil, nil, err
		}
		if m.State != "trashed" {
			return nil, nil, fmt.Errorf("mapping %s is state=%s, cleanup refuses (unlink first)", m.ID, m.State)
		}
		root, err := walkRepoRoot()
		if err != nil {
			return nil, nil, err
		}
		return []store.Mapping{m}, map[string]string{m.ID: root}, nil
	}

	// --all mode: every trashed mapping, best-effort repoRoot from CWD.
	if opts.All {
		// Use CWD walk-up for all; mappings with different repoURLs will
		// get placed relative to this root, which matches spec semantics
		// only when user runs from a parent dir. A smarter resolver can
		// land later with `scan_roots`-based lookup.
		root, err := walkRepoRoot()
		if err != nil {
			// --all is allowed from anywhere — but we lose the ability to
			// resolve target paths. Refuse with clear error.
			return nil, nil, fmt.Errorf("--all requires CWD inside a git repo (or will be extended with scan_roots resolver later): %w", err)
		}
		roots := make(map[string]string, len(trashed))
		for _, m := range trashed {
			roots[m.ID] = root
		}
		return trashed, roots, nil
	}

	// Current-repo mode.
	root, err := walkRepoRoot()
	if err != nil {
		return nil, nil, err
	}
	_, repoURL, _ := gitremote.ResolveFromCWD(root)
	var cur []store.Mapping
	roots := map[string]string{}
	for _, m := range trashed {
		if m.RepoURL == repoURL {
			cur = append(cur, m)
			roots[m.ID] = root
		}
	}
	return cur, roots, nil
}

func walkRepoRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return gitremote.FindRepoRoot(cwd)
}

func renderCleanup(a *app.App, r cleanupResult) error {
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
	for _, ca := range r.Actions {
		marker := "-"
		switch ca.Action {
		case "removed", "dry_run_remove":
			marker = "✓"
		case "already_gone":
			marker = "-"
		case "refused_non_symlink":
			marker = "✗"
		}
		fmt.Fprintf(a.Stdout, "  %s %s\n", marker, ca.TargetAbs)
	}
	fmt.Fprintf(a.Stdout, "  %d removed · %d already gone · %d refused\n",
		r.Removed, r.Missing, r.Refused)
	return nil
}
