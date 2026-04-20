// Binary repolink — private-repo ↔ GitHub repo symlink manager.
// See docs/PROBLEM.md for the full spec.
package main

import (
	"fmt"
	"os"

	"github.com/khanakia/repolink-go/internal/app"
	"github.com/khanakia/repolink-go/internal/cli"
)

func main() {
	a := app.New()
	root := cli.NewRoot(a)
	if err := root.Execute(); err != nil {
		fmt.Fprintf(a.Stderr, "repolink: %v\n", err)
		os.Exit(1)
	}
}
