package cli

import (
	"context"
	"path/filepath"

	"github.com/khanakia/repolink-go/internal/gitignore"
	"github.com/khanakia/repolink-go/internal/store"
)

// syncConsumerGitignore reconciles a consumer repo's .gitignore with the
// DB: the repolink-managed block is rewritten to list every active
// mapping's target path (repo-relative, with leading "/").
//
// Safe no-op when repoURL/repoRoot is empty (command ran outside a
// consumer repo). Errors are returned but callers typically log-and-
// continue since a gitignore update failure shouldn't fail a link.
func syncConsumerGitignore(ctx context.Context, st store.Store, repoURL, repoRoot string) error {
	if repoURL == "" || repoRoot == "" {
		return nil
	}
	rows, err := st.ListMappings(ctx, store.MappingFilter{
		RepoURL: repoURL,
		States:  []string{"active"},
	})
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(rows))
	for _, m := range rows {
		// Gitignore convention: leading "/" anchors the pattern to the
		// repo root, which is exactly what we want for these targets.
		paths = append(paths, "/"+filepath.ToSlash(filepath.Join(m.TargetRel, m.LinkName)))
	}
	return gitignore.UpdateBlock(filepath.Join(repoRoot, ".gitignore"), paths)
}
