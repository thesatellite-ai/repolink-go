package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/khanakia/repolink-go/internal/app"
	"github.com/khanakia/repolink-go/internal/config"
	"github.com/khanakia/repolink-go/internal/gitremote"
	"github.com/khanakia/repolink-go/internal/store"
)

// newStatusCmd implements the current-repo half of MVP-07. Read-only —
// never mutates DB or fs. Walks up for the consumer repo, lists every
// mapping with repo_url match, and reports each one's live fs state.
func newStatusCmd(a *app.App) *cobra.Command {
	var longFlag bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show mappings + live fs state for the current repo (read-only)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd.Context(), a, longFlag)
		},
	}
	cmd.Flags().BoolVar(&longFlag, "long", false, "show full UUIDs + audit fields")
	return cmd
}

// statusRow is the renderer-friendly per-mapping snapshot.
type statusRow struct {
	ID        string `json:"id"`
	LinkName  string `json:"link_name"`
	SourceRel string `json:"source_rel"`
	TargetRel string `json:"target_rel"`
	Kind      string `json:"kind"`
	State     string `json:"state"`
	FSState   string `json:"fs_state"` // ok | missing | wrong_target | collision
	FSDetail  string `json:"fs_detail,omitempty"`
	TargetAbs string `json:"target_abs"`
}

type statusResult struct {
	Profile  string      `json:"profile"`
	RepoURL  string      `json:"repo_url"`
	RepoRoot string      `json:"repo_root"`
	Rows     []statusRow `json:"rows"`
}

func runStatus(ctx context.Context, a *app.App, long bool) error {
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

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	repoRoot, repoURL, err := gitremote.ResolveFromCWD(cwd)
	if err != nil {
		return fmt.Errorf("detect consumer repo: %w", err)
	}

	st, err := store.OpenDB(ctx, prof.Dir)
	if err != nil {
		return err
	}
	defer st.Close()

	rows, err := st.ListMappings(ctx, store.MappingFilter{
		RepoURL: repoURL,
		// include all states so users see trashed/paused too
	})
	if err != nil {
		return err
	}

	out := statusResult{
		Profile:  profName,
		RepoURL:  repoURL,
		RepoRoot: repoRoot,
		Rows:     make([]statusRow, 0, len(rows)),
	}
	for _, m := range rows {
		targetAbs := filepath.Join(repoRoot, m.TargetRel, m.LinkName)
		sourceAbs := filepath.Join(prof.Dir, m.SourceRel)
		fsState, detail := inspectFS(targetAbs, sourceAbs, m.State)
		out.Rows = append(out.Rows, statusRow{
			ID:        m.ID,
			LinkName:  m.LinkName,
			SourceRel: m.SourceRel,
			TargetRel: m.TargetRel,
			Kind:      m.Kind,
			State:     m.State,
			FSState:   fsState,
			FSDetail:  detail,
			TargetAbs: targetAbs,
		})
	}

	// Stable order: active first, then paused, then trashed; tie-break by link_name.
	sort.SliceStable(out.Rows, func(i, j int) bool {
		if out.Rows[i].State != out.Rows[j].State {
			return stateRank(out.Rows[i].State) < stateRank(out.Rows[j].State)
		}
		return out.Rows[i].LinkName < out.Rows[j].LinkName
	})

	return renderStatus(a, out, long)
}

func stateRank(s string) int {
	switch s {
	case "active":
		return 0
	case "paused":
		return 1
	case "trashed":
		return 2
	}
	return 3
}

// inspectFS classifies a mapping's current filesystem state.
// Mapping state + fs state together paint the full picture; sync-layer
// interpretation lives in symlinker, not here.
func inspectFS(targetAbs, sourceAbs, mappingState string) (string, string) {
	info, err := os.Lstat(targetAbs)
	if err != nil {
		if os.IsNotExist(err) {
			if mappingState == "paused" || mappingState == "trashed" {
				return "ok", "" // expected to be gone
			}
			return "missing", "no symlink at target"
		}
		return "error", err.Error()
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return "collision", "non-symlink at target"
	}
	dest, err := os.Readlink(targetAbs)
	if err != nil {
		return "error", err.Error()
	}
	if !filepath.IsAbs(dest) {
		dest = filepath.Clean(filepath.Join(filepath.Dir(targetAbs), dest))
	}
	if filepath.Clean(dest) != filepath.Clean(sourceAbs) {
		return "wrong_target", "points at " + dest
	}
	if mappingState != "active" {
		return "stale", "symlink present but state=" + mappingState
	}
	return "ok", ""
}

func renderStatus(a *app.App, r statusResult, long bool) error {
	if a.JSON {
		return json.NewEncoder(a.Stdout).Encode(map[string]any{
			"ok":      true,
			"version": app.Version,
			"data":    r,
		})
	}
	fmt.Fprintf(a.Stdout, "%s  %s\n", r.Profile, r.RepoURL)
	if len(r.Rows) == 0 {
		fmt.Fprintln(a.Stdout, "  no mappings")
		return nil
	}
	idWidth := 19
	if long {
		idWidth = 36
	}
	fmt.Fprintf(a.Stdout, "     %-*s  %-10s  %-20s  %s\n",
		idWidth, "ID", "STATE", "LINK", "TARGET")
	for _, row := range r.Rows {
		id := shortID(row.ID, long)
		marker := fsMarker(row.FSState)
		fmt.Fprintf(a.Stdout, "  %s  %-*s  %-10s  %-20s  %s\n",
			marker, idWidth, id, row.State, row.LinkName, row.TargetAbs)
		if long && row.FSDetail != "" {
			fmt.Fprintf(a.Stdout, "     %*s    %s\n", idWidth, "", row.FSDetail)
		}
	}
	return nil
}

func fsMarker(s string) string {
	switch s {
	case "ok":
		return "✓"
	case "missing":
		return "!"
	case "wrong_target", "collision", "stale", "error":
		return "✗"
	}
	return "?"
}

// shortID truncates a UUID v7 for human display. Default keeps 18 hex
// characters (including dashes) so UUIDs generated inside the same
// millisecond — which share the 48-bit timestamp prefix — still show a
// distinguishing random suffix. --long shows the full UUID.
func shortID(id string, long bool) string {
	if long || len(id) < 18 {
		return id
	}
	return id[:18] + "…"
}
