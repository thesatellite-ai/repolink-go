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
	"github.com/khanakia/repolink-go/internal/store"
)

// newSetupCmd implements MVP-04. Registers a private-repo clone with
// repolink: creates/updates ~/.repolink/config.jsonc, opens <dir>/repo.db
// (creating + migrating if missing), and inserts the repo_meta singleton.
// Idempotent — safe to re-run.
func newSetupCmd(a *app.App) *cobra.Command {
	var (
		dirFlag         string
		nameFlag        string
		makeDefaultFlag bool
	)

	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Register a private-repo clone (create config + repo.db)",
		Long: `setup is idempotent. Zero-flag form uses the current working directory
as the private-repo dir and its basename as the profile name.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSetup(cmd.Context(), a, setupOpts{
				Dir:         dirFlag,
				Name:        nameFlag,
				MakeDefault: makeDefaultFlag,
			})
		},
	}

	cmd.Flags().StringVar(&dirFlag, "dir", "", "private-repo directory (default: CWD)")
	cmd.Flags().StringVar(&nameFlag, "name", "", "profile name (default: basename of --dir)")
	cmd.Flags().BoolVar(&makeDefaultFlag, "make-default", false, "set this profile as default_profile")

	return cmd
}

type setupOpts struct {
	Dir         string
	Name        string
	MakeDefault bool
}

// setupResult is the renderer-friendly outcome for both human + JSON modes.
type setupResult struct {
	Profile       string `json:"profile"`
	Dir           string `json:"dir"`
	ConfigPath    string `json:"config_path"`
	DBPath        string `json:"db_path"`
	RepoMetaID    string `json:"repo_meta_id"`
	DisplayName   string `json:"display_name"`
	ConfigCreated bool   `json:"config_created"`
	DBCreated     bool   `json:"db_created"`
	MetaCreated   bool   `json:"meta_created"`
	DefaultSet    bool   `json:"default_set"`
}

func runSetup(ctx context.Context, a *app.App, opts setupOpts) error {
	// ------ Compute ------
	dir, err := resolveSetupDir(opts.Dir)
	if err != nil {
		return err
	}
	name := opts.Name
	if name == "" {
		name = filepath.Base(dir)
	}

	cfgPath, err := config.DefaultPath()
	if err != nil {
		return err
	}
	if a.ConfigPath != "" {
		cfgPath = a.ConfigPath
	}

	// ------ Plan ------
	cfg, cfgCreated, err := loadOrBootstrapConfig(cfgPath)
	if err != nil {
		return err
	}

	res := setupResult{
		Profile:       name,
		Dir:           dir,
		ConfigPath:    cfgPath,
		DBPath:        filepath.Join(dir, "repo.db"),
		ConfigCreated: cfgCreated,
	}

	// ------ Apply ------
	if err := cfg.AddProfile(name, config.Profile{Dir: dir}); err != nil {
		return fmt.Errorf("add profile: %w", err)
	}
	if opts.MakeDefault {
		if err := cfg.SetDefaultProfile(name); err != nil {
			return fmt.Errorf("set default_profile: %w", err)
		}
		res.DefaultSet = true
	} else if cfg.DefaultProfile == name {
		// AddProfile auto-set because it was the first profile.
		res.DefaultSet = true
	}
	if err := cfg.WriteFile(); err != nil {
		return fmt.Errorf("write %s: %w", cfgPath, err)
	}

	if _, err := os.Stat(res.DBPath); errors.Is(err, os.ErrNotExist) {
		res.DBCreated = true
	} else if err != nil {
		return fmt.Errorf("stat %s: %w", res.DBPath, err)
	}

	// Ensure <dir>/.gitignore contains SQLite sidecar patterns so WAL/SHM
	// files don't pollute the committed repo. Idempotent: appends only if
	// patterns aren't already present.
	if err := ensureProfileGitignore(dir); err != nil {
		return fmt.Errorf("write .gitignore: %w", err)
	}

	st, err := store.OpenDB(ctx, dir)
	if err != nil {
		return fmt.Errorf("open repo.db: %w", err)
	}
	defer st.Close()

	existing, err := st.GetRepoMeta(ctx)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("get repo_meta: %w", err)
	}
	if errors.Is(err, store.ErrNotFound) {
		meta, err := st.EnsureRepoMeta(ctx, filepath.Base(dir))
		if err != nil {
			return fmt.Errorf("insert repo_meta: %w", err)
		}
		res.RepoMetaID = meta.ID
		res.DisplayName = meta.DisplayName
		res.MetaCreated = true
	} else {
		res.RepoMetaID = existing.ID
		res.DisplayName = existing.DisplayName
	}

	host, _ := os.Hostname()
	if host == "" {
		host = "unknown"
	}
	prof, err := st.EnsureProfile(ctx, name, host)
	if err != nil {
		return fmt.Errorf("ensure profile row: %w", err)
	}
	if err := st.LogRun(ctx, store.NewRun{
		ProfileID: prof.ID,
		Op:        "setup",
		Result:    "ok",
		Message:   fmt.Sprintf("profile=%s dir=%s meta_created=%t db_created=%t", name, dir, res.MetaCreated, res.DBCreated),
	}); err != nil {
		return fmt.Errorf("log run: %w", err)
	}

	return renderSetup(a, res)
}

// resolveSetupDir picks dir: explicit --dir → as-is (must be abs); empty → CWD.
func resolveSetupDir(flag string) (string, error) {
	if flag != "" {
		if !filepath.IsAbs(flag) {
			abs, err := filepath.Abs(flag)
			if err != nil {
				return "", fmt.Errorf("resolve %s: %w", flag, err)
			}
			flag = abs
		}
		if info, err := os.Stat(flag); err != nil || !info.IsDir() {
			return "", fmt.Errorf("--dir %s: not a directory", flag)
		}
		return filepath.Clean(flag), nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	return cwd, nil
}

func loadOrBootstrapConfig(path string) (*config.Config, bool, error) {
	c, err := config.Load(path)
	if err == nil {
		return c, false, nil
	}
	if errors.Is(err, config.ErrNotFound) {
		return config.BootstrapEmpty(path), true, nil
	}
	return nil, false, err
}

func renderSetup(a *app.App, r setupResult) error {
	if a.JSON {
		return json.NewEncoder(a.Stdout).Encode(map[string]any{
			"ok":      true,
			"version": app.Version,
			"data":    r,
		})
	}
	fmt.Fprintf(a.Stdout, "✓ profile %q → %s\n", r.Profile, r.Dir)
	fmt.Fprintf(a.Stdout, "  config: %s%s\n", r.ConfigPath, ifElse(r.ConfigCreated, " (created)", ""))
	fmt.Fprintf(a.Stdout, "  db:     %s%s\n", r.DBPath, ifElse(r.DBCreated, " (created)", ""))
	fmt.Fprintf(a.Stdout, "  meta:   %s [%s]%s\n", r.DisplayName, r.RepoMetaID, ifElse(r.MetaCreated, " (created)", ""))
	if r.DefaultSet {
		fmt.Fprintln(a.Stdout, "  default_profile set")
	}
	return nil
}

func ifElse(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

// ensureProfileGitignore appends the SQLite sidecar patterns (repo.db-wal,
// repo.db-shm) to <dir>/.gitignore if not already present. Creates the
// file if missing. Idempotent.
func ensureProfileGitignore(dir string) error {
	path := filepath.Join(dir, ".gitignore")
	want := []string{"repo.db-wal", "repo.db-shm"}

	var existing []byte
	if data, err := os.ReadFile(path); err == nil {
		existing = data
	} else if !os.IsNotExist(err) {
		return err
	}

	var missing []string
	for _, pat := range want {
		if !containsLine(existing, pat) {
			missing = append(missing, pat)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}
	if len(existing) == 0 {
		if _, err := f.WriteString("# repolink SQLite sidecar files\n"); err != nil {
			return err
		}
	}
	for _, pat := range missing {
		if _, err := f.WriteString(pat + "\n"); err != nil {
			return err
		}
	}
	return nil
}

// containsLine returns true if body contains `line` as a complete line
// (not a substring of some longer word). Cheap byte scan.
func containsLine(body []byte, line string) bool {
	start := 0
	for i := 0; i <= len(body); i++ {
		if i == len(body) || body[i] == '\n' {
			if string(body[start:i]) == line {
				return true
			}
			start = i + 1
		}
	}
	return false
}
