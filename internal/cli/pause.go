package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/khanakia/repolink-go/internal/app"
	"github.com/khanakia/repolink-go/internal/config"
	"github.com/khanakia/repolink-go/internal/gitremote"
	"github.com/khanakia/repolink-go/internal/store"
	"github.com/khanakia/repolink-go/internal/symlinker"
)

// MVP-11 pause/resume. Pair:
//
//	pause  <id|link_name>     active → paused; removes symlink
//	pause  --all-in-repo      every active mapping for current repo
//	resume <id|link_name>     paused → active; recreates symlink
//	resume --all-in-repo      every paused mapping for current repo
//
// Neither command touches DB rows that are already in the target state —
// pause on an already-paused row is a no-op; same for resume.

func newPauseCmd(a *app.App) *cobra.Command {
	var allInRepo bool
	cmd := &cobra.Command{
		Use:   "pause [id|link_name]",
		Short: "Pause a mapping: removes symlink, preserves DB row",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ident := ""
			if len(args) == 1 {
				ident = args[0]
			}
			return runTransition(cmd.Context(), a, transitionOpts{
				Op: "pause", Ident: ident, AllInRepo: allInRepo,
				From: "active", To: "paused",
			})
		},
	}
	cmd.Flags().BoolVar(&allInRepo, "all-in-repo", false, "pause every active mapping in the current repo")
	return cmd
}

func newResumeCmd(a *app.App) *cobra.Command {
	var allInRepo bool
	cmd := &cobra.Command{
		Use:   "resume [id|link_name]",
		Short: "Resume a paused mapping: recreates symlink",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ident := ""
			if len(args) == 1 {
				ident = args[0]
			}
			return runTransition(cmd.Context(), a, transitionOpts{
				Op: "resume", Ident: ident, AllInRepo: allInRepo,
				From: "paused", To: "active",
			})
		},
	}
	cmd.Flags().BoolVar(&allInRepo, "all-in-repo", false, "resume every paused mapping in the current repo")
	return cmd
}

type transitionOpts struct {
	Op        string // "pause" | "resume"
	Ident     string
	AllInRepo bool
	From      string
	To        string
}

type transitionAction struct {
	MappingID string `json:"mapping_id"`
	LinkName  string `json:"link_name"`
	TargetAbs string `json:"target_abs"`
	FromState string `json:"from_state"`
	ToState   string `json:"to_state"`
	FSAction  string `json:"fs_action"` // removed | created | skipped | refused
	Reason    string `json:"reason,omitempty"`
}

type transitionResult struct {
	Op      string             `json:"op"`
	Profile string             `json:"profile"`
	RepoURL string             `json:"repo_url,omitempty"`
	Total   int                `json:"total"`
	Changed int                `json:"changed"`
	Refused int                `json:"refused"`
	DryRun  bool               `json:"dry_run"`
	Actions []transitionAction `json:"actions"`
}

func runTransition(ctx context.Context, a *app.App, opts transitionOpts) error {
	if opts.Ident == "" && !opts.AllInRepo {
		return fmt.Errorf("%s: need <id|link_name> or --all-in-repo", opts.Op)
	}
	if opts.Ident != "" && opts.AllInRepo {
		return fmt.Errorf("%s: <id|link_name> and --all-in-repo are mutually exclusive", opts.Op)
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

	// Current repo (required for --all-in-repo and for scoped link_name lookup).
	repoRoot, repoURL, repoErr := gitremote.ResolveFromCWD(mustCWD())

	st, err := store.OpenDB(ctx, prof.Dir)
	if err != nil {
		return err
	}
	defer st.Close()

	// Select target rows.
	var targets []store.Mapping
	if opts.Ident != "" {
		// Single-id mode; repoURL optional for scoping.
		scope := ""
		if repoErr == nil {
			scope = repoURL
		}
		m, err := resolveMapping(ctx, st, opts.Ident, scope)
		if err != nil {
			return err
		}
		targets = []store.Mapping{m}
	} else {
		if repoErr != nil {
			return fmt.Errorf("--all-in-repo: %w", repoErr)
		}
		rows, err := st.ListMappings(ctx, store.MappingFilter{
			RepoURL: repoURL,
			States:  []string{opts.From},
		})
		if err != nil {
			return err
		}
		targets = rows
	}

	result := transitionResult{
		Op: opts.Op, Profile: profName, RepoURL: repoURL,
		Total: len(targets), DryRun: a.DryRun,
	}

	// Build intents per op.
	intents := make([]symlinker.Intent, 0, len(targets))
	actionable := make([]store.Mapping, 0, len(targets))
	for _, m := range targets {
		if m.State != opts.From {
			result.Actions = append(result.Actions, transitionAction{
				MappingID: m.ID, LinkName: m.LinkName,
				FromState: m.State, ToState: m.State, // unchanged
				FSAction: "skipped",
				Reason:   fmt.Sprintf("state=%s (need %s)", m.State, opts.From),
			})
			continue
		}
		// repoRoot for this mapping: if we resolved from CWD use that;
		// otherwise bail (single-id mode without CWD repo can't place fs).
		root := repoRoot
		if root == "" {
			return fmt.Errorf("%s: need CWD inside some git repo for fs ops (mapping %s)", opts.Op, m.ID)
		}
		intent := symlinker.Intent{
			TargetAbs: filepath.Join(root, m.TargetRel, m.LinkName),
			MappingID: m.ID,
			Kind:      m.Kind,
		}
		if opts.Op == "pause" {
			intent.Remove = true
		} else {
			intent.SourceAbs = filepath.Join(prof.Dir, m.SourceRel)
		}
		intents = append(intents, intent)
		actionable = append(actionable, m)
	}

	plan := symlinker.Compute(intents)
	applyRes, applyErr := symlinker.Apply(ctx, plan, symlinker.ApplyOpts{DryRun: a.DryRun})
	_ = applyRes

	prof0, _ := st.EnsureProfile(ctx, profName, hostnameOr("unknown"))

	for i, act := range plan.Actions {
		m := actionable[i]
		fsAction := "skipped"
		switch act.Kind {
		case symlinker.ActRemove:
			fsAction = "removed"
		case symlinker.ActCreate:
			fsAction = "created"
		case symlinker.ActReplace:
			fsAction = "replaced"
		case symlinker.ActSkip:
			fsAction = "skipped"
		case symlinker.ActCollision, symlinker.ActSourceMissing:
			fsAction = "refused"
		}

		ta := transitionAction{
			MappingID: m.ID, LinkName: m.LinkName,
			TargetAbs: act.Intent.TargetAbs,
			FromState: m.State, ToState: opts.To,
			FSAction: fsAction, Reason: act.Reason,
		}

		if fsAction == "refused" {
			// DB row stays put on refusal so user can retry.
			ta.ToState = m.State
			result.Actions = append(result.Actions, ta)
			result.Refused++
			continue
		}

		if !a.DryRun {
			if err := st.UpdateMappingState(ctx, m.ID, opts.To); err != nil {
				return fmt.Errorf("update %s → %s: %w", m.ID, opts.To, err)
			}
			_ = st.LogRun(ctx, store.NewRun{
				ProfileID: prof0.ID,
				MappingID: m.ID,
				Op:        opts.Op,
				Result:    "ok",
				Message:   fmt.Sprintf("%s → %s (%s)", m.State, opts.To, fsAction),
			})
		}
		result.Actions = append(result.Actions, ta)
		result.Changed++
	}

	// Reconcile consumer .gitignore — pause removes entries (active→paused),
	// resume adds them back (paused→active). syncConsumerGitignore reads
	// the current DB state, so it handles both directions uniformly.
	if !a.DryRun && repoErr == nil {
		if err := syncConsumerGitignore(ctx, st, repoURL, repoRoot); err != nil {
			fmt.Fprintf(a.Stderr, "warning: update .gitignore: %v\n", err)
		}
	}

	if renderErr := renderTransition(a, result); renderErr != nil {
		return renderErr
	}
	if applyErr != nil {
		return applyErr
	}
	if result.Refused > 0 {
		return fmt.Errorf("%d mapping(s) refused", result.Refused)
	}
	return nil
}

func mustCWD() string {
	wd, _ := os.Getwd()
	return wd
}

// renderTransition prints a pause/resume outcome.
func renderTransition(a *app.App, r transitionResult) error {
	if a.JSON {
		return json.NewEncoder(a.Stdout).Encode(map[string]any{
			"ok":      r.Refused == 0,
			"version": app.Version,
			"data":    r,
		})
	}
	if r.DryRun {
		fmt.Fprintln(a.Stdout, "(dry-run — no fs or DB changes)")
	}
	fmt.Fprintf(a.Stdout, "%s: %s\n", r.Op, r.Profile)
	for _, a2 := range r.Actions {
		marker := "."
		switch a2.FSAction {
		case "removed":
			marker = "-"
		case "created", "replaced":
			marker = "+"
		case "skipped":
			marker = "="
		case "refused":
			marker = "✗"
		}
		fmt.Fprintf(a.Stdout, "  %s %-24s %s → %s  %s\n",
			marker, a2.LinkName, a2.FromState, a2.ToState, a2.TargetAbs)
		if a2.Reason != "" && a2.FSAction != "created" && a2.FSAction != "removed" {
			fmt.Fprintf(a.Stdout, "      (%s)\n", a2.Reason)
		}
	}
	fmt.Fprintf(a.Stdout, "  %d changed · %d refused · %d total\n", r.Changed, r.Refused, r.Total)
	return nil
}
