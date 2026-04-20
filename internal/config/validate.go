package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// validate runs structural checks on a freshly-loaded Config. Purely in-Go;
// does no filesystem I/O (dir existence is a WARN, surfaced by callers).
func (c *Config) validate() error {
	if len(c.Profiles) == 0 {
		return fmt.Errorf("profiles: at least one profile required")
	}
	for name, p := range c.Profiles {
		if name == "" {
			return fmt.Errorf("profiles: empty profile name")
		}
		if p.Dir == "" {
			return fmt.Errorf("profiles.%s.dir: required", name)
		}
		if !filepath.IsAbs(p.Dir) {
			return fmt.Errorf("profiles.%s.dir: must be absolute, got %q", name, p.Dir)
		}
		for i, r := range p.ScanRoots {
			if !filepath.IsAbs(r) {
				return fmt.Errorf("profiles.%s.scan_roots[%d]: must be absolute, got %q", name, i, r)
			}
		}
	}
	if c.DefaultProfile != "" {
		if _, ok := c.Profiles[c.DefaultProfile]; !ok {
			return fmt.Errorf("default_profile: %q does not exist (known: %s)", c.DefaultProfile, c.profileNames())
		}
	}
	return nil
}

// ValidateKey checks whether a raw dotted path from the CLI (e.g.
// "profiles.work.dir" or "dir") matches the Allowlist. Returns the matched
// KeySpec and a suggestion string ("" if none) if unknown.
func ValidateKey(dotted string) (KeySpec, string, error) {
	for _, k := range Allowlist {
		if keyMatches(k.Path, dotted) {
			return k, "", nil
		}
	}
	return KeySpec{}, nearest(dotted, KnownKeys()), fmt.Errorf("unknown config key %q", dotted)
}

// keyMatches treats "<name>" in a spec path as a single-segment wildcard.
func keyMatches(specPath, dotted string) bool {
	a := strings.Split(specPath, ".")
	b := strings.Split(dotted, ".")
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] == "<name>" {
			if b[i] == "" {
				return false
			}
			continue
		}
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// nearest returns the closest match from candidates by Levenshtein distance,
// or "" if none within a small threshold. Used for "did you mean …?" hints.
func nearest(want string, candidates []string) string {
	best := ""
	bestD := 1 << 30
	for _, c := range candidates {
		d := levenshtein(want, c)
		if d < bestD {
			bestD = d
			best = c
		}
	}
	if bestD > 4 {
		return ""
	}
	return best
}

func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	curr := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		curr[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			curr[j] = min3(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(br)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
