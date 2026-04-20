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
	"github.com/khanakia/repolink-go/internal/resolver"
	"github.com/khanakia/repolink-go/internal/store"
	"github.com/khanakia/repolink-go/internal/symlinker"
)

// newSyncCmd implements MVP-06:
//
//	repolink sync        — auto-detect repo, create missing symlinks, idempotent
//	repolink             — bare form (aliases sync, wired in root.go)
//	repolink sync --repo <url> — sync from anywhere by explicit repo url
//
// Multi-source (MVP-08) works via:
//   * `-p a,b` CLI flag                        (comma-separated profiles)
//   * `.repolink.jsonc` in CWD                 (profile/profiles/sources)
//   * `default_profile` in config              (single-profile default)
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
	Profile   string `json:"profile"`
	MappingID string `json:"mapping_id"`
	LinkName  string `json:"link_name"`
	TargetAbs string `json:"target_abs"`
	SourceAbs string `json:"source_abs"`
	Kind      string `json:"kind"`
	Action    string `json:"action"` // create | skip | replace | collision | source_missing
	Reason    string `json:"reason,omitempty"`
}

type syncResult struct {
	Profiles      []string     `json:"profiles"`
	RepoURL       string       `json:"repo_url"`
	RepoRoot      string       `json:"repo_root"`
	Total         int          `json:"total"`
	Created       int          `json:"created"`
	Skipped       int          `json:"skipped"`
	Refused       int          `json:"refused"`
	PausedSkipped int          `json:"paused_skipped"`
	DryRun        bool         `json:"dry_run"`
	Actions       []syncAction `json:"actions"`
	Warnings      []string     `json:"warnings,omitempty"`
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

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	var repoRoot, repoURL string
	if opts.RepoURL != "" {
		repoURL = opts.RepoURL
		if root, err := gitremote.FindRepoRoot(cwd); err == nil {
			repoRoot = root
		} else {
			return fmt.Errorf("--repo given but CWD is not inside any git repo (need repoRoot to place symlinks)")
		}
	} else {
		root, url, err := gitremote.ResolveFromCWD(cwd)
		if err != nil {
			return fmt.Errorf("detect consumer repo: %w", err)
		}
		repoRoot, repoURL = root, url
	}

	// ------ Resolve profiles (multi-source aware) ------
	resolved, warnings, err := resolveSyncProfiles(ctx, a, cfg, cwd)
	if err != nil {
		return err
	}
	if len(resolved) == 0 {
		return errors.New("sync: no profiles resolved")
	}

	// ------ Plan + Apply across all resolved profiles ------
	result := syncResult{
		RepoURL: repoURL, RepoRoot: repoRoot, DryRun: a.DryRun,
		Warnings: warnings,
	}
	for _, r := range resolved {
		result.Profiles = append(result.Profiles, r.ProfileName)
	}

	// Cross-profile collision detection: reject if the same
	// (repo_url, target_rel, link_name) is claimed by more than one profile.
	type targetKey struct{ repoURL, targetRel, linkName string }
	claimers := map[targetKey]string{}

	type pendingRow struct {
		profile string
		prof    config.Profile
		m       store.Mapping
	}
	var pending []pendingRow

	for _, r := range resolved {
		st, err := store.OpenDB(ctx, r.Profile.Dir)
		if err != nil {
			return fmt.Errorf("open %s repo.db: %w", r.ProfileName, err)
		}
		rows, err := st.ListMappings(ctx, store.MappingFilter{
			RepoURL: repoURL,
			States:  []string{"active", "paused"},
		})
		_ = st.Close()
		if err != nil {
			return fmt.Errorf("%s: list mappings: %w", r.ProfileName, err)
		}
		for _, m := range rows {
			if m.State == "paused" {
				result.PausedSkipped++
				continue
			}
			key := targetKey{m.RepoURL, m.TargetRel, m.LinkName}
			if prev, ok := claimers[key]; ok && prev != r.ProfileName {
				return fmt.Errorf("cross-profile collision on %s/%s/%s: claimed by both %q and %q",
					m.RepoURL, m.TargetRel, m.LinkName, prev, r.ProfileName)
			}
			claimers[key] = r.ProfileName
			pending = append(pending, pendingRow{profile: r.ProfileName, prof: r.Profile, m: m})
		}
	}

	intents := make([]symlinker.Intent, 0, len(pending))
	for _, p := range pending {
		intents = append(intents, symlinker.Intent{
			SourceAbs: filepath.Join(p.prof.Dir, p.m.SourceRel),
			TargetAbs: filepath.Join(repoRoot, p.m.TargetRel, p.m.LinkName),
			Kind:      p.m.Kind,
			MappingID: p.m.ID,
		})
	}
	plan := symlinker.Compute(intents)
	applyRes, applyErr := symlinker.Apply(ctx, plan, symlinker.ApplyOpts{DryRun: a.DryRun})
	_ = applyRes
	result.Total = len(pending)

	for i, act := range plan.Actions {
		p := pending[i]
		sa := syncAction{
			Profile:   p.profile,
			MappingID: p.m.ID,
			LinkName:  p.m.LinkName,
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

	// One op=sync run_log per contributing profile.
	for _, r := range resolved {
		st, err := store.OpenDB(ctx, r.Profile.Dir)
		if err != nil {
			continue
		}
		prof0, _ := st.EnsureProfile(ctx, r.ProfileName, hostnameOr("unknown"))
		logResult := "ok"
		if applyErr != nil || result.Refused > 0 {
			logResult = "error"
		}
		_ = st.LogRun(ctx, store.NewRun{
			ProfileID: prof0.ID,
			Op:        "sync",
			Result:    logResult,
			Message: fmt.Sprintf("repo=%s created=%d skipped=%d refused=%d paused=%d dry=%t",
				repoURL, result.Created, result.Skipped, result.Refused, result.PausedSkipped, a.DryRun),
		})
		_ = st.Close()
	}

	if renderErr := renderSync(a, result); renderErr != nil {
		return renderErr
	}
	if applyErr != nil {
		return applyErr
	}
	if result.Refused > 0 {
		return fmt.Errorf("%d mapping(s) refused (see output above)", result.Refused)
	}
	return nil
}

// resolveSyncProfiles picks the set of profiles sync should union across,
// applying the documented precedence:
//  1. --profile / -p  (comma-split for multi-source)
//  2. CWD's .repolink.jsonc
//  3. config.DefaultProfile
func resolveSyncProfiles(ctx context.Context, a *app.App, cfg *config.Config, cwd string) ([]resolver.Resolved, []string, error) {
	// 1. CLI override.
	if a.ProfileOverride != "" {
		names := strings.Split(a.ProfileOverride, ",")
		return resolveNames(cfg, names)
	}
	// 2. .repolink.jsonc
	pin, err := resolver.ReadPin(cwd)
	if err == nil {
		res, warns, err := resolver.Resolve(ctx, cfg, pin)
		if err != nil {
			return nil, nil, err
		}
		return res, warningsToStrings(warns), nil
	} else if !errors.Is(err, resolver.ErrPinNotFound) {
		return nil, nil, err
	}
	// 3. default_profile
	name, prof, err := cfg.Resolve("")
	if err != nil {
		return nil, nil, err
	}
	return []resolver.Resolved{
		{ProfileName: name, Profile: prof, MatchedBy: "default", Source: name},
	}, nil, nil
}

func resolveNames(cfg *config.Config, names []string) ([]resolver.Resolved, []string, error) {
	var out []resolver.Resolved
	var warns []string
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		p, ok := cfg.Profiles[n]
		if !ok {
			return nil, warns, fmt.Errorf("profile %q not in config", n)
		}
		if _, err := os.Stat(p.Dir); err != nil {
			warns = append(warns, fmt.Sprintf("profile %q dir missing: %s", n, p.Dir))
			continue
		}
		out = append(out, resolver.Resolved{
			ProfileName: n, Profile: p, MatchedBy: "cli", Source: n,
		})
	}
	return out, warns, nil
}

func warningsToStrings(ws []resolver.Warning) []string {
	if len(ws) == 0 {
		return nil
	}
	out := make([]string, 0, len(ws))
	for _, w := range ws {
		out = append(out, w.ProfileName+": "+w.Message)
	}
	return out
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
	fmt.Fprintf(a.Stdout, "sync %s → %s\n", strings.Join(r.Profiles, ","), r.RepoURL)
	for _, w := range r.Warnings {
		fmt.Fprintf(a.Stdout, "  ! warning: %s\n", w)
	}
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
		prefix := "  "
		if len(r.Profiles) > 1 {
			prefix = fmt.Sprintf("  [%s] ", a2.Profile)
		}
		line := fmt.Sprintf("%s%s %-24s %s", prefix, marker, a2.LinkName, a2.TargetAbs)
		if a2.Reason != "" {
			line += "  (" + a2.Reason + ")"
		}
		fmt.Fprintln(a.Stdout, line)
	}
	fmt.Fprintf(a.Stdout, "  %d created · %d skipped · %d refused · %d paused\n",
		r.Created, r.Skipped, r.Refused, r.PausedSkipped)
	return nil
}
