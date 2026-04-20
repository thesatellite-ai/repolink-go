package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/khanakia/repolink-go/internal/app"
	"github.com/khanakia/repolink-go/internal/config"
	"github.com/khanakia/repolink-go/internal/gitremote"
	"github.com/khanakia/repolink-go/internal/store"
)

// MVP-17 verify: read-only drift report. Walks every mapping in the
// active profile and classifies its live filesystem state. Does not
// auto-fix — `sync` / `cleanup` / `pause` are the remediation verbs.
//
// Requires CWD inside the consuming repo so we can derive absolute
// target paths. (Multi-profile / multi-repo verification lands once the
// .repolink.jsonc resolver ships — MVP-08.)

func newVerifyCmd(a *app.App) *cobra.Command {
	var longFlag bool
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Read-only drift report across all mappings in active profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runVerify(cmd.Context(), a, longFlag)
		},
	}
	cmd.Flags().BoolVar(&longFlag, "long", false, "show full UUIDs + fs detail")
	return cmd
}

type verifyIssue struct {
	MappingID string `json:"mapping_id"`
	LinkName  string `json:"link_name"`
	RepoURL   string `json:"repo_url"`
	State     string `json:"state"`
	FSState   string `json:"fs_state"`
	Detail    string `json:"detail,omitempty"`
	TargetAbs string `json:"target_abs"`
}

type verifyResult struct {
	Profile string        `json:"profile"`
	Total   int           `json:"total"`
	Healthy int           `json:"healthy"`
	Issues  []verifyIssue `json:"issues"`
}

func runVerify(ctx context.Context, a *app.App, long bool) error {
	cfgPath, err := config.DefaultPath()
	if err != nil {
		return err
	}
	if a.ConfigPath != "" {
		cfgPath = a.ConfigPath
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	profName, prof, err := cfg.Resolve(a.ProfileOverride)
	if err != nil {
		return err
	}

	st, err := store.OpenDB(ctx, prof.Dir)
	if err != nil {
		return err
	}
	defer st.Close()

	// Look at every non-trashed mapping.
	rows, err := st.ListMappings(ctx, store.MappingFilter{
		States: []string{"active", "paused"},
	})
	if err != nil {
		return err
	}

	result := verifyResult{Profile: profName, Total: len(rows)}

	// Group mappings by RepoURL so we only walk up from a working repo
	// once per group. Without a resolver we can only inspect mappings
	// whose RepoURL matches *a* git repo found on the user's machine via
	// their CWD ancestry — so for MVP we resolve a single repoRoot from
	// the CWD. Mappings for other repos are reported as "unresolved"
	// rather than errored on.
	cwdRoot, cwdURL, _ := walkRepoFromCWD()

	for _, m := range rows {
		var (
			target   string
			resolved = true
		)
		if cwdURL != "" && m.RepoURL == cwdURL {
			target = filepath.Join(cwdRoot, m.TargetRel, m.LinkName)
		} else {
			resolved = false
		}

		if !resolved {
			result.Issues = append(result.Issues, verifyIssue{
				MappingID: m.ID, LinkName: m.LinkName, RepoURL: m.RepoURL,
				State: m.State, FSState: "unresolved",
				Detail: "repo not reachable from CWD — run verify from inside each repo (multi-repo resolver pending)",
			})
			continue
		}

		source := filepath.Join(prof.Dir, m.SourceRel)
		fsState, detail := inspectFS(target, source, m.State)
		if fsState == "ok" {
			result.Healthy++
			continue
		}
		result.Issues = append(result.Issues, verifyIssue{
			MappingID: m.ID, LinkName: m.LinkName, RepoURL: m.RepoURL,
			State: m.State, FSState: fsState, Detail: detail, TargetAbs: target,
		})
	}

	return renderVerify(a, result, long)
}

// walkRepoFromCWD wraps gitremote.ResolveFromCWD. Returns ("", "", err)
// when CWD is not inside any git repo — callers treat that as "unresolved"
// rather than a hard error so verify can still enumerate mappings.
func walkRepoFromCWD() (string, string, error) {
	return gitremote.ResolveFromCWD(mustCWD())
}

func renderVerify(a *app.App, r verifyResult, long bool) error {
	if a.JSON {
		return json.NewEncoder(a.Stdout).Encode(map[string]any{
			"ok":      len(r.Issues) == 0,
			"version": app.Version,
			"data":    r,
		})
	}
	fmt.Fprintf(a.Stdout, "%s — %d mapping(s) · %d healthy · %d issue(s)\n",
		r.Profile, r.Total, r.Healthy, len(r.Issues))
	for _, iss := range r.Issues {
		id := shortID(iss.MappingID, long)
		fmt.Fprintf(a.Stdout, "  ✗ %s %-10s %-14s %s\n",
			id, iss.State, iss.FSState, iss.LinkName)
		if iss.TargetAbs != "" {
			fmt.Fprintf(a.Stdout, "      target: %s\n", iss.TargetAbs)
		}
		if iss.Detail != "" {
			fmt.Fprintf(a.Stdout, "      detail: %s\n", iss.Detail)
		}
	}
	return nil
}
