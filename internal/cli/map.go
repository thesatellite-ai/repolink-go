package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/khanakia/repolink-go/internal/app"
	"github.com/khanakia/repolink-go/internal/config"
	"github.com/khanakia/repolink-go/internal/store"
)

// newMapCmd is the `repolink map` parent. MVP-07 ships `list` only;
// `add`/`remove`/`restore`/`purge`/`mv` land in later MVP slices.
func newMapCmd(a *app.App) *cobra.Command {
	m := &cobra.Command{
		Use:   "map",
		Short: "Global mapping management (list / add / remove / restore / purge / mv)",
	}
	m.AddCommand(newMapListCmd(a))
	m.AddCommand(newMapPurgeCmd(a))
	m.AddCommand(newMapMvCmd(a))
	return m
}

func newMapListCmd(a *app.App) *cobra.Command {
	var (
		repoFlag   string
		sourceFlag string
		stateFlag  string
		longFlag   bool
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List mappings in the active profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMapList(cmd.Context(), a, mapListOpts{
				Repo:   repoFlag,
				Source: sourceFlag,
				State:  stateFlag,
				Long:   longFlag,
			})
		},
	}
	cmd.Flags().StringVar(&repoFlag, "repo", "", "filter by repo_url (exact)")
	cmd.Flags().StringVar(&sourceFlag, "source", "", "filter by source_rel (exact)")
	cmd.Flags().StringVar(&stateFlag, "state", "", "filter by state: active|paused|trashed|all (default: active)")
	cmd.Flags().BoolVar(&longFlag, "long", false, "show full UUIDs + notes + timestamps")
	return cmd
}

type mapListOpts struct {
	Repo   string
	Source string
	State  string
	Long   bool
}

type mapListRow struct {
	ID             string `json:"id"`
	RepoURL        string `json:"repo_url"`
	SourceRel      string `json:"source_rel"`
	TargetRel      string `json:"target_rel"`
	LinkName       string `json:"link_name"`
	Kind           string `json:"kind"`
	State          string `json:"state"`
	Notes          string `json:"notes,omitempty"`
	CreatedByEmail string `json:"created_by_email,omitempty"`
	CreatedByName  string `json:"created_by_name,omitempty"`
}

type mapListResult struct {
	Profile string       `json:"profile"`
	Rows    []mapListRow `json:"rows"`
}

func runMapList(ctx context.Context, a *app.App, opts mapListOpts) error {
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

	states, err := resolveStateFilter(opts.State)
	if err != nil {
		return err
	}

	st, err := store.OpenDB(ctx, prof.Dir)
	if err != nil {
		return err
	}
	defer st.Close()

	all, err := st.ListMappings(ctx, store.MappingFilter{
		RepoURL: opts.Repo,
		States:  states,
	})
	if err != nil {
		return err
	}

	rows := make([]mapListRow, 0, len(all))
	for _, m := range all {
		if opts.Source != "" && m.SourceRel != opts.Source {
			continue
		}
		rows = append(rows, mapListRow{
			ID: m.ID, RepoURL: m.RepoURL, SourceRel: m.SourceRel,
			TargetRel: m.TargetRel, LinkName: m.LinkName, Kind: m.Kind,
			State: m.State, Notes: m.Notes,
			CreatedByEmail: m.CreatedByEmail,
			CreatedByName:  m.CreatedByName,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].RepoURL != rows[j].RepoURL {
			return rows[i].RepoURL < rows[j].RepoURL
		}
		return rows[i].LinkName < rows[j].LinkName
	})

	result := mapListResult{Profile: profName, Rows: rows}
	return renderMapList(a, result, opts.Long)
}

// resolveStateFilter returns the list of states to pass to the Store.
// "" defaults to "active". "all" returns nil (no filter).
func resolveStateFilter(s string) ([]string, error) {
	switch s {
	case "":
		return []string{"active"}, nil
	case "active", "paused", "trashed":
		return []string{s}, nil
	case "all":
		return nil, nil
	}
	return nil, fmt.Errorf("--state %q: must be active|paused|trashed|all", s)
}

func renderMapList(a *app.App, r mapListResult, long bool) error {
	if a.JSON {
		return json.NewEncoder(a.Stdout).Encode(map[string]any{
			"ok":      true,
			"version": app.Version,
			"data":    r,
		})
	}
	if len(r.Rows) == 0 {
		fmt.Fprintf(a.Stdout, "%s: no mappings\n", r.Profile)
		return nil
	}
	fmt.Fprintf(a.Stdout, "%s — %d mapping(s)\n", r.Profile, len(r.Rows))
	for _, row := range r.Rows {
		id := shortID(row.ID, long)
		fmt.Fprintf(a.Stdout, "  %-8s %-10s %-35s %s/%s/%s\n",
			id, row.State, row.SourceRel, row.RepoURL, row.TargetRel, row.LinkName)
		if long && row.Notes != "" {
			fmt.Fprintf(a.Stdout, "           note: %s\n", row.Notes)
		}
	}
	return nil
}
