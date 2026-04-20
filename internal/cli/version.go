package cli

import (
	"encoding/json"
	"fmt"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/khanakia/repolink-go/internal/app"
)

func newVersionCmd(a *app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print repolink version",
		RunE: func(cmd *cobra.Command, args []string) error {
			if a.JSON {
				return json.NewEncoder(a.Stdout).Encode(map[string]any{
					"ok":      true,
					"version": app.Version,
					"data": map[string]any{
						"go":   runtime.Version(),
						"os":   runtime.GOOS,
						"arch": runtime.GOARCH,
					},
				})
			}
			fmt.Fprintf(a.Stdout, "repolink %s (%s %s/%s)\n", app.Version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
			return nil
		},
	}
}
