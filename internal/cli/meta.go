package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/khanakia/repolink-go/internal/app"
	"github.com/khanakia/repolink-go/internal/config"
	"github.com/khanakia/repolink-go/internal/store"
)

// MVP-16 meta commands — read/edit the per-DB repo_meta singleton.
//
//	repolink meta                     show private_repo_id + display_name + created_at
//	repolink meta rename "<new name>" update display_name (committed to repo.db)

func newMetaCmd(a *app.App) *cobra.Command {
	m := &cobra.Command{
		Use:   "meta",
		Short: "Show or rename the active profile's repo_meta",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMetaShow(cmd.Context(), a)
		},
	}
	m.AddCommand(newMetaRenameCmd(a))
	return m
}

func newMetaRenameCmd(a *app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "rename <new-name>",
		Short: "Set display_name (committed to repo.db — travels via git)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMetaRename(cmd.Context(), a, args[0])
		},
	}
}

type metaResult struct {
	Profile       string `json:"profile"`
	ID            string `json:"id"`
	PrivateRepoID string `json:"private_repo_id"`
	DisplayName   string `json:"display_name"`
	CreatedAt     string `json:"created_at"`
	Renamed       bool   `json:"renamed,omitempty"`
	PreviousName  string `json:"previous_name,omitempty"`
}

func openActiveStore(ctx context.Context, a *app.App) (string, store.Store, func(), error) {
	cfgPath, err := config.DefaultPath()
	if err != nil {
		return "", nil, nil, err
	}
	if a.ConfigPath != "" {
		cfgPath = a.ConfigPath
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			return "", nil, nil, fmt.Errorf("no ~/.repolink/config.jsonc — run `repolink setup` first")
		}
		return "", nil, nil, err
	}
	profName, prof, err := cfg.Resolve(a.ProfileOverride)
	if err != nil {
		return "", nil, nil, err
	}
	st, err := store.OpenDB(ctx, prof.Dir)
	if err != nil {
		return "", nil, nil, err
	}
	return profName, st, func() { _ = st.Close() }, nil
}

func runMetaShow(ctx context.Context, a *app.App) error {
	profName, st, closeFn, err := openActiveStore(ctx, a)
	if err != nil {
		return err
	}
	defer closeFn()

	meta, err := st.GetRepoMeta(ctx)
	if err != nil {
		return fmt.Errorf("get repo_meta: %w", err)
	}
	r := metaResult{
		Profile: profName,
		ID:      meta.ID, PrivateRepoID: meta.PrivateRepoID,
		DisplayName: meta.DisplayName,
		CreatedAt:   meta.CreatedAt.Format("2006-01-02 15:04:05"),
	}
	return renderMeta(a, r)
}

func runMetaRename(ctx context.Context, a *app.App, newName string) error {
	if newName == "" {
		return errors.New("meta rename: empty name")
	}
	profName, st, closeFn, err := openActiveStore(ctx, a)
	if err != nil {
		return err
	}
	defer closeFn()

	prev, err := st.GetRepoMeta(ctx)
	if err != nil {
		return fmt.Errorf("read current repo_meta: %w", err)
	}
	if a.DryRun {
		r := metaResult{
			Profile: profName,
			ID:      prev.ID, PrivateRepoID: prev.PrivateRepoID,
			DisplayName: newName,
			CreatedAt:   prev.CreatedAt.Format("2006-01-02 15:04:05"),
			Renamed:     false,
			PreviousName: prev.DisplayName,
		}
		return renderMeta(a, r)
	}

	updated, err := st.RenameRepoMeta(ctx, newName)
	if err != nil {
		return fmt.Errorf("rename repo_meta: %w", err)
	}
	prof0, _ := st.EnsureProfile(ctx, profName, hostnameOr("unknown"))
	_ = st.LogRun(ctx, store.NewRun{
		ProfileID: prof0.ID,
		Op:        "meta",
		Result:    "ok",
		Message:   fmt.Sprintf("display_name: %q → %q", prev.DisplayName, newName),
	})

	return renderMeta(a, metaResult{
		Profile: profName,
		ID:      updated.ID, PrivateRepoID: updated.PrivateRepoID,
		DisplayName: updated.DisplayName,
		CreatedAt:   updated.CreatedAt.Format("2006-01-02 15:04:05"),
		Renamed:     true, PreviousName: prev.DisplayName,
	})
}

func renderMeta(a *app.App, r metaResult) error {
	if a.JSON {
		return json.NewEncoder(a.Stdout).Encode(map[string]any{
			"ok":      true,
			"version": app.Version,
			"data":    r,
		})
	}
	if r.Renamed {
		fmt.Fprintf(a.Stdout, "✓ renamed %q → %q\n", r.PreviousName, r.DisplayName)
		fmt.Fprintln(a.Stdout)
	}
	fmt.Fprintf(a.Stdout, "profile:        %s\n", r.Profile)
	fmt.Fprintf(a.Stdout, "private_repo_id: %s\n", r.PrivateRepoID)
	fmt.Fprintf(a.Stdout, "display_name:    %s\n", r.DisplayName)
	fmt.Fprintf(a.Stdout, "created_at:      %s\n", r.CreatedAt)
	return nil
}
