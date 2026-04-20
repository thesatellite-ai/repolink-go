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

// newSyncCmd implements MVP-06:
//
//	repolink sync        — auto-detect repo, create missing symlinks, idempotent
//	repolink             — bare form (aliases sync, wired in root.go)
//	repolink sync --repo <url> — sync from anywhere by explicit repo url
//
// Single-profile for now. Multi-source (opens multiple profile DBs) is a
// follow-up once `.repolink.jsonc` resolver lands (MVP-08).
func newSyncCmd(a *app.App) *cobra.Command {
	var repoURLFlag string

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Create missing symlinks for the current repo (idempotent)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSync(cmd.Context(), a, syncOpts{RepoURL: repoURLFlag})
		},
	}
	cmd.Flags().StringVar(&repoURLFlag, "repo", "", "canonical repo url to sync (default: detect from CWD)")
	return cmd
}

type syncOpts struct {
	RepoURL string // empty = detect from CWD
}

type syncAction struct {
	MappingID string `json:"mapping_id"`
	LinkName  string `json:"link_name"`
	TargetAbs string `json:"target_abs"`
	SourceAbs string `json:"source_abs"`
	Kind      string `json:"kind"`
	Action    string `json:"action"` // create | skip | replace | collision | source_missing
	Reason    string `json:"reason,omitempty"`
}

type syncResult struct {
	Profile      string        `json:"profile"`
	RepoURL      string        `json:"repo_url"`
	RepoRoot     string        `json:"repo_root"`
	Total        int           `json:"total"`
	Created      int           `json:"created"`
	Skipped      int           `json:"skipped"`
	Refused      int           `json:"refused"`
	PausedSkipped int          `json:"paused_skipped"`
	DryRun       bool          `json:"dry_run"`
	Actions      []syncAction  `json:"actions"`
}

func runSync(ctx context.Context, a *app.App, opts syncOpts) error {
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

	var (
		repoRoot string
		repoURL  string
	)
	if opts.RepoURL != "" {
		repoURL = opts.RepoURL
		// When --repo is explicit, repoRoot is still needed to place symlinks.
		// Walk up from CWD; if that fails, we bail with a clear error.
		cwd, _ := os.Getwd()
		if root, err := gitremote.FindRepoRoot(cwd); err == nil {
			repoRoot = root
		} else {
			return fmt.Errorf("--repo given but CWD is not inside any git repo (need repoRoot to place symlinks)")
		}
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getwd: %w", err)
		}
		root, url, err := gitremote.ResolveFromCWD(cwd)
		if err != nil {
			return fmt.Errorf("detect consumer repo: %w", err)
		}
		repoRoot, repoURL = root, url
	}

	// ------ Plan ------
	st, err := store.OpenDB(ctx, prof.Dir)
	if err != nil {
		return fmt.Errorf("open repo.db: %w", err)
	}
	defer st.Close()

	rows, err := st.ListMappings(ctx, store.MappingFilter{
		RepoURL: repoURL,
		States:  []string{"active", "paused"},
	})
	if err != nil {
		return fmt.Errorf("list mappings: %w", err)
	}

	pausedSkipped := 0
	intents := make([]symlinker.Intent, 0, len(rows))
	rowByIdx := make([]store.Mapping, 0, len(rows))
	for _, m := range rows {
		if m.State == "paused" {
			pausedSkipped++
			continue
		}
		intents = append(intents, symlinker.Intent{
			SourceAbs: filepath.Join(prof.Dir, m.SourceRel),
			TargetAbs: filepath.Join(repoRoot, m.TargetRel, m.LinkName),
			Kind:      m.Kind,
			MappingID: m.ID,
		})
		rowByIdx = append(rowByIdx, m)
	}

	plan := symlinker.Compute(intents)

	// ------ Apply ------
	applyOpts := symlinker.ApplyOpts{DryRun: a.DryRun}
	applyRes, err := symlinker.Apply(ctx, plan, applyOpts)
	// We don't return err immediately — partial results are still useful
	// to render. Callers inspect Refused.
	_ = applyRes

	// Build renderer-friendly actions slice from plan + result classification.
	result := syncResult{
		Profile:       profName,
		RepoURL:       repoURL,
		RepoRoot:      repoRoot,
		Total:         len(intents),
		DryRun:        a.DryRun,
		PausedSkipped: pausedSkipped,
		Actions:       make([]syncAction, 0, len(plan.Actions)),
	}

	for i, act := range plan.Actions {
		sa := syncAction{
			MappingID: rowByIdx[i].ID,
			LinkName:  rowByIdx[i].LinkName,
			TargetAbs: act.Intent.TargetAbs,
			SourceAbs: act.Intent.SourceAbs,
			Kind:      act.Intent.Kind,
			Action:    act.Kind.String(),
			Reason:    act.Reason,
		}
		result.Actions = append(result.Actions, sa)
		switch act.Kind {
		case symlinker.ActSkip:
			result.Skipped++
		case symlinker.ActCreate, symlinker.ActReplace:
			result.Created++
		case symlinker.ActCollision, symlinker.ActSourceMissing:
			result.Refused++
		}
	}

	// Log one op=sync run_logs row.
	prof0, _ := st.EnsureProfile(ctx, profName, hostnameOr("unknown"))
	logResult := "ok"
	msg := fmt.Sprintf("repo=%s created=%d skipped=%d refused=%d paused_skipped=%d dry_run=%t",
		repoURL, result.Created, result.Skipped, result.Refused, pausedSkipped, a.DryRun)
	if err != nil || result.Refused > 0 {
		logResult = "error"
		if err != nil {
			msg += " err=" + err.Error()
		}
	}
	_ = st.LogRun(ctx, store.NewRun{
		ProfileID: prof0.ID,
		Op:        "sync",
		Result:    logResult,
		Message:   msg,
	})

	if renderErr := renderSync(a, result); renderErr != nil {
		return renderErr
	}
	if err != nil {
		return err
	}
	if result.Refused > 0 {
		return fmt.Errorf("%d mapping(s) refused (see output above)", result.Refused)
	}
	return nil
}

func renderSync(a *app.App, r syncResult) error {
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
	fmt.Fprintf(a.Stdout, "sync %s → %s\n", r.Profile, r.RepoURL)
	if r.Total == 0 {
		fmt.Fprintln(a.Stdout, "  no mappings for this repo")
		return nil
	}
	for _, a2 := range r.Actions {
		marker := "."
		switch a2.Action {
		case "create":
			marker = "+"
		case "skip":
			marker = "="
		case "replace":
			marker = "~"
		case "collision", "source_missing":
			marker = "!"
		}
		line := fmt.Sprintf("  %s %-24s %s", marker, a2.LinkName, a2.TargetAbs)
		if a2.Reason != "" {
			line += "  (" + a2.Reason + ")"
		}
		fmt.Fprintln(a.Stdout, line)
	}
	fmt.Fprintf(a.Stdout, "  %d created · %d skipped · %d refused · %d paused\n",
		r.Created, r.Skipped, r.Refused, r.PausedSkipped)
	return nil
}
