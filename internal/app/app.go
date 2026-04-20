// Package app holds the process-wide dependency-injection struct.
// Hygiene gate G4: no package-level mutable globals — every component
// that needs config, db, stderr/stdout writers, clock, etc. receives
// them via this struct.
package app

import (
	"io"
	"os"
	"time"
)

// Version is stamped at build time via -ldflags.
var Version = "dev"

// Clock abstracts time so tests can control deterministic timestamps.
type Clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// App is the DI container passed through cobra PreRun hooks.
// Fields are populated in cmd/repolink/main.go before subcommands run.
type App struct {
	// Writers — every command writes through these, never fmt.Print*.
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader

	// Clock — time source (swappable in tests).
	Clock Clock

	// ConfigPath — resolved path to ~/.repolink/config.jsonc.
	// Populated lazily by the config loader; empty before first load.
	ConfigPath string

	// Profile — the --profile / -p override, or "" for default_profile.
	ProfileOverride string

	// JSON — global --json flag. When true, renderers emit machine-
	// readable envelopes instead of human prose (G2).
	JSON bool

	// NonInteractive — global --non-interactive flag. Implied by JSON.
	NonInteractive bool

	// DryRun — global --dry-run flag. Mutations compute plans, skip apply.
	DryRun bool
}

// New returns an App wired with real stdio + clock.
func New() *App {
	return &App{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Stdin:  os.Stdin,
		Clock:  realClock{},
	}
}
