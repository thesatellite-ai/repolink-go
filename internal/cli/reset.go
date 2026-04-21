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
	"github.com/khanakia/repolink-go/internal/store"
)

// Reset is the nuclear-option cleanup verb.
//
//	repolink reset --profile <name> --yes    remove one profile + its repo.db
//	repolink reset --all --yes               nuke config + every profile's repo.db
//
// Safety guarantees (see safety rule S-00 in docs/PROBLEM.md):
//   - NEVER touches source directories or their contents.
//   - NEVER touches consumer-repo symlinks. Run `repolink unsync --all` in
//     each consumer repo first if you want to clean the fs too — otherwise
//     consumer repos will contain dangling symlinks after reset.
//   - Refuses to proceed if any targeted profile still has active or paused
//     mappings, unless --force is given.

func newResetCmd(a *app.App) *cobra.Command {
	var (
		profileFlag string
		allFlag     bool
		yesFlag     bool
		forceFlag   bool
	)
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Remove profile(s) + their repo.db (NEVER touches sources or consumer symlinks)",
		Long: `Reset repolink state.

Examples:
  repolink reset --profile work --yes       # drop one profile + its repo.db
  repolink reset --all --yes                # nuke config + every profile's repo.db

Safety:
  - Sources (the directories pointed at by symlinks) are never touched.
  - Symlinks in consumer repos are left alone. Run
    ` + "`repolink unsync --all`" + ` in each consumer repo first if you want
    the filesystem cleaned too, or delete them manually.
  - Refuses if any targeted profile still has active/paused mappings.
    Use --force to override (deliberately leaves dangling symlinks behind).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReset(cmd.Context(), a, resetOpts{
				Profile: profileFlag, All: allFlag, Yes: yesFlag, Force: forceFlag,
			})
		},
	}
	cmd.Flags().StringVar(&profileFlag, "profile", "", "single profile to reset")
	cmd.Flags().BoolVar(&allFlag, "all", false, "reset every configured profile + delete config.jsonc")
	cmd.Flags().BoolVar(&yesFlag, "yes", false, "skip confirmation prompt (required for non-dry-run)")
	cmd.Flags().BoolVar(&forceFlag, "force", false, "proceed even if profiles still have active/paused mappings")
	return cmd
}

type resetOpts struct {
	Profile string
	All     bool
	Yes     bool
	Force   bool
}

type resetAction struct {
	Profile          string `json:"profile"`
	Dir              string `json:"dir"`
	DBRemoved        bool   `json:"db_removed"`
	SidecarRemoved   int    `json:"sidecar_files_removed"`
	GitignoreRemoved bool   `json:"gitignore_removed"`
	ConfigRemoved    bool   `json:"config_removed,omitempty"`
	ActiveBefore     int    `json:"active_mappings_before"`
	PausedBefore     int    `json:"paused_mappings_before"`
	Skipped          bool   `json:"skipped,omitempty"`
	Reason           string `json:"reason,omitempty"`
}

type resetResult struct {
	Targets       []resetAction `json:"targets"`
	ConfigRemoved bool          `json:"config_removed"`
	ConfigPath    string        `json:"config_path"`
	DryRun        bool          `json:"dry_run"`
}

func runReset(ctx context.Context, a *app.App, opts resetOpts) error {
	if opts.Profile == "" && !opts.All {
		return errors.New("reset: need --profile <name> or --all")
	}
	if opts.Profile != "" && opts.All {
		return errors.New("reset: --profile and --all are mutually exclusive")
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
			return fmt.Errorf("nothing to reset: no config at %s", cfgPath)
		}
		return err
	}

	// Pick targets.
	var names []string
	if opts.All {
		for n := range cfg.Profiles {
			names = append(names, n)
		}
	} else {
		if _, ok := cfg.Profiles[opts.Profile]; !ok {
			return fmt.Errorf("reset: profile %q does not exist (known: %s)", opts.Profile, knownConfigNames(cfg))
		}
		names = []string{opts.Profile}
	}

	// Inspect each profile's DB so the confirmation + result can show what
	// will actually disappear. Also enforces the active/paused guard.
	result := resetResult{ConfigPath: cfgPath, DryRun: a.DryRun}
	var blockers []string
	for _, n := range names {
		p := cfg.Profiles[n]
		act := resetAction{Profile: n, Dir: p.Dir}

		active, paused := countMappings(ctx, p.Dir)
		act.ActiveBefore, act.PausedBefore = active, paused
		if (active+paused > 0) && !opts.Force {
			act.Skipped = true
			act.Reason = fmt.Sprintf("refuse: %d active + %d paused mapping(s) — run `repolink unsync --all` in each consumer first, or pass --force", active, paused)
			blockers = append(blockers, n)
		}
		result.Targets = append(result.Targets, act)
	}

	if len(blockers) > 0 && !opts.Force {
		if renderErr := renderReset(a, result); renderErr != nil {
			return renderErr
		}
		return fmt.Errorf("reset refused — %d profile(s) still have live mappings: %s", len(blockers), strings.Join(blockers, ", "))
	}

	// Confirmation gate. --yes / --dry-run / --json / non-interactive all skip.
	if !opts.Yes && !a.DryRun && !a.JSON && !a.NonInteractive {
		scope := fmt.Sprintf("profile %q", opts.Profile)
		if opts.All {
			scope = fmt.Sprintf("ALL %d profile(s) + ~/.repolink/config.jsonc", len(names))
		}
		fmt.Fprintf(a.Stdout, "about to reset %s. This deletes repo.db + sidecars. Sources and consumer symlinks are NOT touched. Proceed? [y/N] ", scope)
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "y") {
			fmt.Fprintln(a.Stdout, "aborted")
			return nil
		}
	}

	// Apply.
	for i := range result.Targets {
		t := &result.Targets[i]
		if t.Skipped {
			continue
		}
		if a.DryRun {
			// Populate "would-have" counters without mutating.
			t.DBRemoved = fileExists(filepath.Join(t.Dir, "repo.db"))
			t.SidecarRemoved = existingSidecars(t.Dir)
			t.GitignoreRemoved = isOurGitignore(filepath.Join(t.Dir, ".gitignore"))
			continue
		}
		removeProfileFiles(t)
	}

	// Config rewrite: for --all nuke the whole file; for --profile unset it.
	if !a.DryRun && len(blockers) == 0 {
		if opts.All {
			if err := os.Remove(cfgPath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove %s: %w", cfgPath, err)
			}
			result.ConfigRemoved = true
		} else {
			// Unset profiles.<name> + if default_profile matched, unset that too.
			if err := cfg.Unset("profiles." + opts.Profile + ".dir"); err != nil {
				// .dir may already have been scrubbed — try removing the whole profile key.
				_ = cfg.Unset("profiles." + opts.Profile + ".scan_roots")
			}
			// There's no first-class "unset profile block" — fallback: fully
			// rewrite via a fresh patch. Simplest: write the remaining
			// profiles as a new bootstrap + re-Add each.
			if err := rewriteConfigWithout(cfgPath, cfg, opts.Profile); err != nil {
				return fmt.Errorf("rewrite config: %w", err)
			}
		}
	}

	return renderReset(a, result)
}

// countMappings opens <dir>/repo.db read-only-ish and counts active/paused
// rows. Missing file / open error returns zeros and no error — caller
// treats "no DB" as "nothing to protect".
func countMappings(ctx context.Context, dir string) (active, paused int) {
	if _, err := os.Stat(filepath.Join(dir, "repo.db")); err != nil {
		return 0, 0
	}
	st, err := store.OpenDB(ctx, dir)
	if err != nil {
		return 0, 0
	}
	defer st.Close()
	if rows, err := st.ListMappings(ctx, store.MappingFilter{States: []string{"active"}}); err == nil {
		active = len(rows)
	}
	if rows, err := st.ListMappings(ctx, store.MappingFilter{States: []string{"paused"}}); err == nil {
		paused = len(rows)
	}
	return
}

// removeProfileFiles deletes repo.db, its -wal / -shm sidecars, and the
// auto-written .gitignore (only if its contents match what setup writes,
// so we never clobber user-authored ignores).
func removeProfileFiles(t *resetAction) {
	for _, f := range []string{"repo.db", "repo.db-wal", "repo.db-shm"} {
		p := filepath.Join(t.Dir, f)
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if err := os.Remove(p); err == nil {
			if f == "repo.db" {
				t.DBRemoved = true
			} else {
				t.SidecarRemoved++
			}
		}
	}
	gi := filepath.Join(t.Dir, ".gitignore")
	if isOurGitignore(gi) {
		if err := os.Remove(gi); err == nil {
			t.GitignoreRemoved = true
		}
	}
}

// isOurGitignore reports whether a .gitignore looks like one repolink
// wrote (and only wrote). Conservative: requires both our marker header
// AND only our patterns present.
func isOurGitignore(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	s := strings.TrimSpace(string(data))
	lines := strings.Split(s, "\n")
	hasMarker := false
	validOnly := true
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		if strings.HasPrefix(ln, "#") {
			if strings.Contains(ln, "repolink SQLite sidecar files") {
				hasMarker = true
			}
			continue
		}
		if ln != "repo.db-wal" && ln != "repo.db-shm" {
			validOnly = false
			break
		}
	}
	return hasMarker && validOnly
}

func existingSidecars(dir string) int {
	n := 0
	for _, f := range []string{"repo.db-wal", "repo.db-shm"} {
		if fileExists(filepath.Join(dir, f)) {
			n++
		}
	}
	return n
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// rewriteConfigWithout produces a fresh config.jsonc containing every
// profile except `drop`. Used when --profile resets a single entry and
// we want to avoid the RFC 6902 remove-profile-block hole in the
// existing API. Comments are lost only for the removed block; top-level
// comments survive because we use BootstrapEmpty + AddProfile.
func rewriteConfigWithout(path string, cfg *config.Config, drop string) error {
	fresh := config.BootstrapEmpty(path)
	var remaining []string
	for name := range cfg.Profiles {
		if name == drop {
			continue
		}
		remaining = append(remaining, name)
	}
	for _, name := range remaining {
		if err := fresh.AddProfile(name, cfg.Profiles[name]); err != nil {
			return err
		}
	}
	if cfg.DefaultProfile != "" && cfg.DefaultProfile != drop {
		if err := fresh.SetDefaultProfile(cfg.DefaultProfile); err != nil {
			return err
		}
	}
	return fresh.WriteFile()
}

func knownConfigNames(cfg *config.Config) string {
	if len(cfg.Profiles) == 0 {
		return "<none>"
	}
	names := make([]string, 0, len(cfg.Profiles))
	for n := range cfg.Profiles {
		names = append(names, n)
	}
	return strings.Join(names, ", ")
}

func renderReset(a *app.App, r resetResult) error {
	if a.JSON {
		return json.NewEncoder(a.Stdout).Encode(map[string]any{
			"ok":      true,
			"version": app.Version,
			"data":    r,
		})
	}
	if r.DryRun {
		fmt.Fprintln(a.Stdout, "(dry-run — no changes)")
	}
	for _, t := range r.Targets {
		if t.Skipped {
			fmt.Fprintf(a.Stdout, "  ✗ %s — skipped (%s)\n", t.Profile, t.Reason)
			continue
		}
		fmt.Fprintf(a.Stdout, "  ✓ %s\n", t.Profile)
		fmt.Fprintf(a.Stdout, "      dir: %s\n", t.Dir)
		if t.DBRemoved {
			fmt.Fprintln(a.Stdout, "      repo.db: removed")
		}
		if t.SidecarRemoved > 0 {
			fmt.Fprintf(a.Stdout, "      sidecars: removed %d\n", t.SidecarRemoved)
		}
		if t.GitignoreRemoved {
			fmt.Fprintln(a.Stdout, "      .gitignore: removed (repolink-owned)")
		}
	}
	if r.ConfigRemoved {
		fmt.Fprintf(a.Stdout, "  ✓ config %s removed\n", r.ConfigPath)
	}
	return nil
}
