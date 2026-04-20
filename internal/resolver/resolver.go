// Package resolver reads a consumer repo's `.repolink.jsonc` pin file
// and resolves it to a slice of active profile names, opening each
// profile's repo.db to match display_name / private_repo_id as needed.
//
// Pin forms supported (mutually exclusive):
//
//  1. Legacy — profile name(s), local-only:
//       { "profile":  "work" }
//       { "profiles": ["work", "personal"] }
//
//  2. Portable — display_name match via repo_meta:
//       { "sources": ["Work Notes"] }
//
//  3. Bulletproof — UUID match (full or prefix) via repo_meta:
//       { "sources": ["8f3a4b5c-..."] }
//
// Legacy and new forms cannot be mixed in one file.
//
// Precedence is the caller's concern: the typical flow is
//
//	1. --profile / -p CLI flag (overrides)
//	2. resolver.Resolve() with the CWD's .repolink.jsonc (if any)
//	3. fall back to config.DefaultProfile
package resolver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tailscale/hujson"

	"github.com/khanakia/repolink-go/internal/config"
	"github.com/khanakia/repolink-go/internal/store"
)

// ErrPinNotFound is returned by ReadPin when no .repolink.jsonc exists.
var ErrPinNotFound = errors.New("resolver: no .repolink.jsonc in CWD")

// ErrMixedForms is returned when legacy (profile/profiles) and new
// (sources) keys both appear in the same pin file.
var ErrMixedForms = errors.New("resolver: .repolink.jsonc mixes legacy `profile`/`profiles` with `sources` — pick one")

// Pin is the parsed shape of .repolink.jsonc.
type Pin struct {
	Profile  string   `json:"profile,omitempty"`
	Profiles []string `json:"profiles,omitempty"`
	Sources  []string `json:"sources,omitempty"`
}

// Kind reports which form the pin uses. Empty pin returns "none".
func (p Pin) Kind() string {
	hasLegacy := p.Profile != "" || len(p.Profiles) > 0
	hasNew := len(p.Sources) > 0
	switch {
	case hasLegacy && hasNew:
		return "mixed"
	case hasLegacy:
		return "legacy"
	case hasNew:
		return "sources"
	}
	return "none"
}

// ReadPin looks for `.repolink.jsonc` in dir (CWD only — no walk-up, per
// spec decision). Returns ErrPinNotFound when missing.
func ReadPin(dir string) (*Pin, error) {
	path := filepath.Join(dir, ".repolink.jsonc")
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrPinNotFound
		}
		return nil, err
	}
	stdCopy := append([]byte(nil), raw...)
	std, err := hujson.Standardize(stdCopy)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	var p Pin
	dec := json.NewDecoder(bytes.NewReader(std))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	if p.Kind() == "mixed" {
		return nil, ErrMixedForms
	}
	return &p, nil
}

// Resolved is one matched profile.
type Resolved struct {
	ProfileName string
	Profile     config.Profile
	// MatchedBy is "profile", "display_name", or "uuid".
	MatchedBy string
	// Source is the raw pin string that produced this match (for audit).
	Source string
}

// Warning is a non-fatal issue the resolver wants the caller to surface
// (missing profile dir, etc.).
type Warning struct {
	ProfileName string
	Message     string
}

// uuidLike matches anything that's unambiguously "hex with optional dashes"
// — a full UUID, its prefix, or a dashless prefix of 8+ hex chars. We
// require 8+ total hex chars for dashless prefixes so we don't treat a
// short display_name like "cafe" as a UUID.
var uuidLike = regexp.MustCompile(`^[0-9a-fA-F]+(-[0-9a-fA-F]+)*$`)

func isUUIDish(s string) bool {
	if !uuidLike.MatchString(s) {
		return false
	}
	if strings.Contains(s, "-") {
		return true // dashes are a strong signal
	}
	return len(s) >= 8
}

// Resolve turns a pin into concrete profile matches.
//
// Parameters:
//
//	cfg    — parsed ~/.repolink/config.jsonc
//	pin    — parsed .repolink.jsonc; nil is allowed and means "no pin"
//
// Return:
//
//	resolved  — matched profiles (may be >1 for multi-source)
//	warnings  — non-fatal issues (missing profile dir, etc.)
//	err       — fatal (ambiguous match, source not found, mixed forms)
//
// Callers typically layer this between the --profile CLI flag and
// cfg.DefaultProfile — see package doc for precedence.
func Resolve(ctx context.Context, cfg *config.Config, pin *Pin) ([]Resolved, []Warning, error) {
	if pin == nil || pin.Kind() == "none" {
		return nil, nil, nil
	}

	// Legacy: profile / profiles → direct name lookup in cfg.
	if pin.Kind() == "legacy" {
		names := pin.Profiles
		if pin.Profile != "" {
			names = append(names, pin.Profile)
		}
		return resolveByProfileName(cfg, names)
	}

	// New: sources. Each source is either a UUID (prefix) or display_name.
	return resolveBySources(ctx, cfg, pin.Sources)
}

func resolveByProfileName(cfg *config.Config, names []string) ([]Resolved, []Warning, error) {
	var (
		out      []Resolved
		warnings []Warning
	)
	seen := map[string]bool{}
	for _, n := range names {
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		p, ok := cfg.Profiles[n]
		if !ok {
			return nil, warnings, fmt.Errorf("profile %q not configured on this machine (known: %s)", n, knownNames(cfg))
		}
		if _, err := os.Stat(p.Dir); err != nil {
			warnings = append(warnings, Warning{ProfileName: n, Message: "profile dir missing: " + p.Dir})
			continue
		}
		out = append(out, Resolved{
			ProfileName: n, Profile: p, MatchedBy: "profile", Source: n,
		})
	}
	return out, warnings, nil
}

func resolveBySources(ctx context.Context, cfg *config.Config, sources []string) ([]Resolved, []Warning, error) {
	if len(sources) == 0 {
		return nil, nil, nil
	}

	// Gather every configured profile's repo_meta (skipping unreachable DBs).
	type metaRow struct {
		Name    string
		Profile config.Profile
		Meta    store.RepoMeta
	}
	var (
		metas    []metaRow
		warnings []Warning
	)
	for name, p := range cfg.Profiles {
		if _, err := os.Stat(p.Dir); err != nil {
			warnings = append(warnings, Warning{ProfileName: name, Message: "profile dir missing: " + p.Dir})
			continue
		}
		dbPath := filepath.Join(p.Dir, "repo.db")
		if _, err := os.Stat(dbPath); err != nil {
			warnings = append(warnings, Warning{ProfileName: name, Message: "repo.db not initialized"})
			continue
		}
		st, err := store.OpenDB(ctx, p.Dir)
		if err != nil {
			warnings = append(warnings, Warning{ProfileName: name, Message: "open repo.db: " + err.Error()})
			continue
		}
		meta, err := st.GetRepoMeta(ctx)
		_ = st.Close()
		if err != nil {
			warnings = append(warnings, Warning{ProfileName: name, Message: "read repo_meta: " + err.Error()})
			continue
		}
		metas = append(metas, metaRow{Name: name, Profile: p, Meta: meta})
	}

	// For each requested source, match against metas.
	var resolved []Resolved
	for _, src := range sources {
		isUUID := isUUIDish(src)
		var matches []metaRow
		matchedBy := "display_name"
		if isUUID {
			matchedBy = "uuid"
			for _, m := range metas {
				if strings.HasPrefix(strings.ToLower(m.Meta.PrivateRepoID), strings.ToLower(src)) {
					matches = append(matches, m)
				}
			}
		} else {
			for _, m := range metas {
				if m.Meta.DisplayName == src {
					matches = append(matches, m)
				}
			}
		}
		switch len(matches) {
		case 0:
			if matchedBy == "uuid" {
				return nil, warnings, fmt.Errorf("source %q: no configured profile's repo_meta.private_repo_id starts with this prefix", src)
			}
			return nil, warnings, fmt.Errorf("source %q: no configured profile has repo_meta.display_name matching this; use UUID form or run `repolink setup` for the missing private-repo", src)
		case 1:
			m := matches[0]
			resolved = append(resolved, Resolved{
				ProfileName: m.Name, Profile: m.Profile,
				MatchedBy: matchedBy, Source: src,
			})
		default:
			if matchedBy == "uuid" {
				return nil, warnings, fmt.Errorf("source %q: UUID prefix is ambiguous (%d matches) — use a longer prefix", src, len(matches))
			}
			return nil, warnings, fmt.Errorf("source %q: display_name matches %d configured profiles — pin by UUID instead", src, len(matches))
		}
	}
	return resolved, warnings, nil
}

func knownNames(cfg *config.Config) string {
	if len(cfg.Profiles) == 0 {
		return "<none>"
	}
	names := make([]string, 0, len(cfg.Profiles))
	for n := range cfg.Profiles {
		names = append(names, n)
	}
	return strings.Join(names, ", ")
}
