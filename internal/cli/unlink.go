package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/khanakia/repolink-go/internal/app"
	"github.com/khanakia/repolink-go/internal/config"
	"github.com/khanakia/repolink-go/internal/gitremote"
	"github.com/khanakia/repolink-go/internal/store"
)

// newUnlinkCmd implements MVP-09:
//
//	repolink unlink <id|link_name>       soft-delete a single mapping
//	repolink unlink --all-in-repo        soft-delete every active/paused mapping
//	                                      in the current repo
//
// Soft-delete ONLY. Flips rows to state=trashed; no filesystem change.
// Run `repolink cleanup` after to actually remove symlink files.
func newUnlinkCmd(a *app.App) *cobra.Command {
	var (
		allInRepo bool
		yesFlag   bool
	)
	cmd := &cobra.Command{
		Use:   "unlink [id|link_name]",
		Short: "Soft-delete one (or all, in current repo) mapping(s) — no fs change",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ident := ""
			if len(args) == 1 {
				ident = args[0]
			}
			return runUnlink(cmd.Context(), a, unlinkOpts{
				Ident: ident, AllInRepo: allInRepo, Yes: yesFlag,
			})
		},
	}
	cmd.Flags().BoolVar(&allInRepo, "all-in-repo", false, "soft-delete every active/paused mapping in the current repo")
	cmd.Flags().BoolVar(&yesFlag, "yes", false, "skip confirmation prompt (for --all-in-repo)")
	return cmd
}

type unlinkOpts struct {
	Ident     string
	AllInRepo bool
	Yes       bool
}

type unlinkAction struct {
	MappingID string `json:"mapping_id"`
	LinkName  string `json:"link_name"`
	RepoURL   string `json:"repo_url"`
	From      string `json:"from_state"`
	To        string `json:"to_state"`
}

type unlinkResult struct {
	Actions []unlinkAction `json:"actions"`
	Trashed int            `json:"trashed"`
	DryRun  bool           `json:"dry_run"`
}

func runUnlink(ctx context.Context, a *app.App, opts unlinkOpts) error {
	if opts.Ident == "" && !opts.AllInRepo {
		return errors.New("unlink: need <id|link_name> or --all-in-repo")
	}
	if opts.Ident != "" && opts.AllInRepo {
		return errors.New("unlink: <id|link_name> and --all-in-repo are mutually exclusive")
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

	// Resolve current repo URL. Required for --all-in-repo; optional for
	// single-id mode (scopes link_name lookups to current repo).
	repoURL := ""
	if cwd, err := os.Getwd(); err == nil {
		if _, url, err := gitremote.ResolveFromCWD(cwd); err == nil {
			repoURL = url
		}
	}
	if opts.AllInRepo && repoURL == "" {
		return errors.New("unlink --all-in-repo: CWD is not inside a git repo")
	}

	st, err := store.OpenDB(ctx, prof.Dir)
	if err != nil {
		return err
	}
	defer st.Close()

	// Collect targets.
	var targets []store.Mapping
	if opts.AllInRepo {
		rows, err := st.ListMappings(ctx, store.MappingFilter{
			RepoURL: repoURL,
			States:  []string{"active", "paused"},
		})
		if err != nil {
			return err
		}
		targets = rows
	} else {
		m, err := resolveMapping(ctx, st, opts.Ident, repoURL)
		if err != nil {
			return err
		}
		if m.State == "trashed" {
			return fmt.Errorf("mapping %s (%s) already trashed", m.ID, m.LinkName)
		}
		targets = []store.Mapping{m}
	}

	if len(targets) == 0 {
		if a.JSON {
			return json.NewEncoder(a.Stdout).Encode(map[string]any{
				"ok": true, "version": app.Version,
				"data": unlinkResult{DryRun: a.DryRun},
			})
		}
		fmt.Fprintln(a.Stdout, "nothing to unlink")
		return nil
	}

	// Confirmation only for --all-in-repo (single-id unlink is already scoped tight).
	if opts.AllInRepo && !opts.Yes && !a.DryRun && !a.JSON && !a.NonInteractive {
		fmt.Fprintf(a.Stdout, "about to soft-delete %d mapping(s) in %s (no fs change). Proceed? [y/N] ",
			len(targets), repoURL)
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "y") {
			fmt.Fprintln(a.Stdout, "aborted")
			return nil
		}
	}

	result := unlinkResult{DryRun: a.DryRun}
	prof0, _ := st.EnsureProfile(ctx, profName, hostnameOr("unknown"))

	for _, m := range targets {
		act := unlinkAction{
			MappingID: m.ID, LinkName: m.LinkName, RepoURL: m.RepoURL,
			From: m.State, To: "trashed",
		}
		if !a.DryRun {
			if err := st.UpdateMappingState(ctx, m.ID, "trashed"); err != nil {
				return fmt.Errorf("update state %s: %w", m.ID, err)
			}
			_ = st.LogRun(ctx, store.NewRun{
				ProfileID: prof0.ID,
				MappingID: m.ID,
				Op:        "unlink",
				Result:    "ok",
				Message:   fmt.Sprintf("%s → trashed", m.State),
			})
		}
		result.Actions = append(result.Actions, act)
		result.Trashed++
	}

	// Reconcile consumer .gitignore with the new active-mappings set.
	if !a.DryRun && repoURL != "" {
		if root, _, err := gitremote.ResolveFromCWD(mustCWD()); err == nil {
			if err := syncConsumerGitignore(ctx, st, repoURL, root); err != nil {
				fmt.Fprintf(a.Stderr, "warning: update .gitignore: %v\n", err)
			}
		}
	}

	return renderUnlink(a, result, opts.AllInRepo)
}

func renderUnlink(a *app.App, r unlinkResult, multi bool) error {
	if a.JSON {
		return json.NewEncoder(a.Stdout).Encode(map[string]any{
			"ok":      true,
			"version": app.Version,
			"data":    r,
		})
	}
	prefix := "✓"
	if r.DryRun {
		prefix = "(dry-run)"
	}
	if multi {
		for _, act := range r.Actions {
			fmt.Fprintf(a.Stdout, "  %s %-24s %s → trashed\n", prefix, act.LinkName, act.From)
		}
		fmt.Fprintf(a.Stdout, "\n  %d mapping(s) trashed — run `repolink cleanup --yes` to remove the symlink files\n", r.Trashed)
		return nil
	}
	// Single-id mode.
	act := r.Actions[0]
	fmt.Fprintf(a.Stdout, "%s unlinked %s (%s → trashed)\n", prefix, act.LinkName, act.From)
	fmt.Fprintf(a.Stdout, "  id: %s\n", act.MappingID)
	fmt.Fprintln(a.Stdout, "  symlink untouched — run `repolink cleanup` to remove from fs")
	return nil
}
