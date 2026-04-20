package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/khanakia/repolink-go/internal/app"
	"github.com/khanakia/repolink-go/internal/config"
	"github.com/khanakia/repolink-go/internal/gitremote"
	"github.com/khanakia/repolink-go/internal/store"
)

// newUnlinkCmd implements MVP-09:
//
//	repolink unlink <id|link_name>
//
// Soft-delete ONLY. Flips the row to state=trashed. No filesystem change.
// Run `repolink cleanup` after to actually remove the symlink file.
func newUnlinkCmd(a *app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "unlink <id|link_name>",
		Short: "Soft-delete a mapping (state=trashed) — no fs change",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUnlink(cmd.Context(), a, args[0])
		},
	}
}

type unlinkResult struct {
	MappingID string `json:"mapping_id"`
	LinkName  string `json:"link_name"`
	RepoURL   string `json:"repo_url"`
	From      string `json:"from_state"`
	To        string `json:"to_state"`
	DryRun    bool   `json:"dry_run"`
}

func runUnlink(ctx context.Context, a *app.App, ident string) error {
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

	// Scope link_name lookups to current repo if we're inside one; fine to fail silently.
	repoURL := ""
	if cwd, err := os.Getwd(); err == nil {
		if _, url, err := gitremote.ResolveFromCWD(cwd); err == nil {
			repoURL = url
		}
	}

	st, err := store.OpenDB(ctx, prof.Dir)
	if err != nil {
		return err
	}
	defer st.Close()

	m, err := resolveMapping(ctx, st, ident, repoURL)
	if err != nil {
		return err
	}
	if m.State == "trashed" {
		return fmt.Errorf("mapping %s (%s) already trashed", m.ID, m.LinkName)
	}

	res := unlinkResult{
		MappingID: m.ID, LinkName: m.LinkName, RepoURL: m.RepoURL,
		From: m.State, To: "trashed", DryRun: a.DryRun,
	}

	if !a.DryRun {
		if err := st.UpdateMappingState(ctx, m.ID, "trashed"); err != nil {
			return fmt.Errorf("update state: %w", err)
		}
		prof0, _ := st.EnsureProfile(ctx, profName, hostnameOr("unknown"))
		_ = st.LogRun(ctx, store.NewRun{
			ProfileID: prof0.ID,
			MappingID: m.ID,
			Op:        "unlink",
			Result:    "ok",
			Message:   fmt.Sprintf("%s → trashed", m.State),
		})
	}

	return renderUnlink(a, res)
}

func renderUnlink(a *app.App, r unlinkResult) error {
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
	fmt.Fprintf(a.Stdout, "%s unlinked %s (%s → trashed)\n", prefix, r.LinkName, r.From)
	fmt.Fprintf(a.Stdout, "  id: %s\n", r.MappingID)
	fmt.Fprintln(a.Stdout, "  symlink untouched — run `repolink cleanup` to remove from fs")
	return nil
}
