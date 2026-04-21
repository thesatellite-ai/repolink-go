package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/khanakia/repolink-go/internal/app"
	"github.com/khanakia/repolink-go/internal/config"
	"github.com/khanakia/repolink-go/internal/gitremote"
	"github.com/khanakia/repolink-go/internal/store"
)

// MVP-19 state — full machine-state snapshot.
//
// Primary consumer: `repolink mcp` (v0.2) + AI agents / scripts that want
// one call returning config + every configured profile's repo_meta +
// mappings, plus whatever we can detect about CWD's git repo.
//
// JSON is the canonical output; human-mode prints a short summary.

func newStateCmd(a *app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "state",
		Short: "Full machine-state snapshot (always JSON-friendly)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runState(cmd.Context(), a)
		},
	}
}

type stateCWD struct {
	Path     string `json:"path"`
	RepoRoot string `json:"repo_root,omitempty"`
	RepoURL  string `json:"repo_url,omitempty"`
	Error    string `json:"error,omitempty"`
}

type stateMapping struct {
	ID        string    `json:"id"`
	SourceRel string    `json:"source_rel"`
	TargetRel string    `json:"target_rel"`
	LinkName  string    `json:"link_name"`
	RepoURL   string    `json:"repo_url"`
	Kind      string    `json:"kind"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type stateMeta struct {
	ID            string    `json:"id"`
	PrivateRepoID string    `json:"private_repo_id"`
	DisplayName   string    `json:"display_name"`
	CreatedAt     time.Time `json:"created_at"`
}

type stateProfile struct {
	Name      string         `json:"name"`
	Dir       string         `json:"dir"`
	ScanRoots []string       `json:"scan_roots,omitempty"`
	Reachable bool           `json:"reachable"`
	Error     string         `json:"error,omitempty"`
	Meta      *stateMeta     `json:"meta,omitempty"`
	Mappings  []stateMapping `json:"mappings,omitempty"`
	Counts    map[string]int `json:"counts,omitempty"` // active/paused/trashed
}

type stateResult struct {
	Version        string         `json:"version"`
	ConfigPath     string         `json:"config_path"`
	DefaultProfile string         `json:"default_profile"`
	ActiveProfile  string         `json:"active_profile"`
	Hostname       string         `json:"hostname"`
	CWD            stateCWD       `json:"cwd"`
	Profiles       []stateProfile `json:"profiles"`
}

func runState(ctx context.Context, a *app.App) error {
	cfgPath, err := config.DefaultPath()
	if err != nil {
		return err
	}
	if a.ConfigPath != "" {
		cfgPath = a.ConfigPath
	}

	result := stateResult{
		Version:    app.Version,
		ConfigPath: cfgPath,
		Hostname:   hostnameOr("unknown"),
	}

	// CWD + current-repo detection (non-fatal).
	if wd, err := os.Getwd(); err == nil {
		result.CWD.Path = wd
		if root, url, err := gitremote.ResolveFromCWD(wd); err == nil {
			result.CWD.RepoRoot = root
			result.CWD.RepoURL = url
		} else {
			result.CWD.Error = err.Error()
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			// Config not yet bootstrapped — still emit a valid envelope
			// so tooling can distinguish "no repolink setup" from error.
			return renderState(a, result)
		}
		return err
	}

	result.DefaultProfile = cfg.DefaultProfile
	if activeName, _, err := cfg.Resolve(a.ProfileOverride); err == nil {
		result.ActiveProfile = activeName
	}

	// Iterate every profile and gather its per-DB state. A missing dir
	// or open error is reported on that profile, not bubbled up — state
	// must be best-effort so one broken profile doesn't mask the others.
	names := make([]string, 0, len(cfg.Profiles))
	for n := range cfg.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		p := cfg.Profiles[name]
		sp := stateProfile{
			Name:      name,
			Dir:       p.Dir,
			ScanRoots: p.ScanRoots,
		}
		if _, err := os.Stat(p.Dir); err != nil {
			sp.Error = "profile dir: " + err.Error()
			result.Profiles = append(result.Profiles, sp)
			continue
		}
		// Read-only guard: state must NEVER create a repo.db. If the
		// file is missing, the profile was added via `config --add-profile`
		// but never setup — report that cleanly instead of silently
		// migrating an empty DB into place.
		if _, err := os.Stat(filepath.Join(p.Dir, "repo.db")); err != nil {
			if os.IsNotExist(err) {
				sp.Error = "repo.db not initialized (run `repolink setup --dir " + p.Dir + "`)"
			} else {
				sp.Error = "stat repo.db: " + err.Error()
			}
			result.Profiles = append(result.Profiles, sp)
			continue
		}
		st, err := store.OpenDB(ctx, p.Dir)
		if err != nil {
			sp.Error = "open repo.db: " + err.Error()
			result.Profiles = append(result.Profiles, sp)
			continue
		}
		sp.Reachable = true

		if meta, err := st.GetRepoMeta(ctx); err == nil {
			sp.Meta = &stateMeta{
				ID:            meta.ID,
				PrivateRepoID: meta.PrivateRepoID,
				DisplayName:   meta.DisplayName,
				CreatedAt:     meta.CreatedAt,
			}
		} else if !errors.Is(err, store.ErrNotFound) {
			sp.Error = "read repo_meta: " + err.Error()
		}

		rows, err := st.ListMappings(ctx, store.MappingFilter{})
		if err == nil {
			sp.Mappings = make([]stateMapping, 0, len(rows))
			counts := map[string]int{"active": 0, "paused": 0, "trashed": 0}
			for _, m := range rows {
				counts[m.State]++
				sp.Mappings = append(sp.Mappings, stateMapping{
					ID: m.ID, SourceRel: m.SourceRel,
					TargetRel: m.TargetRel, LinkName: m.LinkName,
					RepoURL: m.RepoURL, Kind: m.Kind, State: m.State,
					CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
				})
			}
			sp.Counts = counts
		}

		_ = st.Close()
		result.Profiles = append(result.Profiles, sp)
	}

	return renderState(a, result)
}

func renderState(a *app.App, r stateResult) error {
	if a.JSON {
		return json.NewEncoder(a.Stdout).Encode(map[string]any{
			"ok":      true,
			"version": app.Version,
			"data":    r,
		})
	}
	fmt.Fprintf(a.Stdout, "config:          %s\n", r.ConfigPath)
	fmt.Fprintf(a.Stdout, "hostname:        %s\n", r.Hostname)
	fmt.Fprintf(a.Stdout, "default_profile: %s\n", r.DefaultProfile)
	fmt.Fprintf(a.Stdout, "active_profile:  %s\n", r.ActiveProfile)
	if r.CWD.RepoURL != "" {
		fmt.Fprintf(a.Stdout, "cwd_repo:        %s\n", r.CWD.RepoURL)
	} else {
		fmt.Fprintf(a.Stdout, "cwd_repo:        (none detected)\n")
	}
	fmt.Fprintln(a.Stdout)
	if len(r.Profiles) == 0 {
		fmt.Fprintln(a.Stdout, "(no profiles configured — run `repolink setup`)")
		return nil
	}
	for _, p := range r.Profiles {
		marker := "  "
		if p.Name == r.DefaultProfile {
			marker = "* "
		}
		fmt.Fprintf(a.Stdout, "%s%s\n", marker, p.Name)
		fmt.Fprintf(a.Stdout, "    dir: %s\n", p.Dir)
		if !p.Reachable {
			fmt.Fprintf(a.Stdout, "    status: UNREACHABLE (%s)\n", p.Error)
			continue
		}
		if p.Meta != nil {
			fmt.Fprintf(a.Stdout, "    display_name:    %s\n", p.Meta.DisplayName)
			fmt.Fprintf(a.Stdout, "    private_repo_id: %s\n", p.Meta.PrivateRepoID)
		}
		fmt.Fprintf(a.Stdout, "    mappings: %d active · %d paused · %d trashed\n",
			p.Counts["active"], p.Counts["paused"], p.Counts["trashed"])
	}
	return nil
}
