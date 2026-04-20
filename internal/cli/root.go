// Package cli wires cobra commands. Per hygiene gate G1, this package must
// not import internal/ent directly — it talks to a repo interface instead.
// Per G2, command handlers must not call fmt.Print*; writes go through
// a renderer that honors app.JSON.
package cli

import (
	"github.com/spf13/cobra"

	"github.com/khanakia/repolink-go/internal/app"
)

// NewRoot builds the top-level `repolink` cobra command.
// Bare `repolink` (no args) will eventually alias `sync`; for now it just
// prints help.
func NewRoot(a *app.App) *cobra.Command {
	root := &cobra.Command{
		Use:           "repolink",
		Short:         "Private-repo ↔ GitHub repo symlink manager",
		Long:          "repolink manages symlinks from a central private-repo into consuming GitHub repos. See docs/PROBLEM.md.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVarP(&a.ProfileOverride, "profile", "p", "", "override default_profile for this command")
	root.PersistentFlags().BoolVar(&a.JSON, "json", false, "emit machine-readable JSON (implies --non-interactive)")
	root.PersistentFlags().BoolVar(&a.NonInteractive, "non-interactive", false, "never prompt; fail closed on ambiguous input")
	root.PersistentFlags().BoolVar(&a.DryRun, "dry-run", false, "compute plan, skip apply (mutations only)")

	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if a.JSON {
			a.NonInteractive = true
		}
		return nil
	}

	root.AddCommand(newVersionCmd(a))

	return root
}
