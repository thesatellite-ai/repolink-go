package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tailscale/hujson"
)

// InitialJSONC is the body written when `repolink setup` creates
// config.jsonc from scratch. Keep the commentary short — it's the first
// thing the user sees if they open the file.
const InitialJSONC = `{
  // Active profile by default. Override per-command with --profile/-p.
  "default_profile": "",

  "profiles": {
  }
}
`

// AddProfile inserts a new profile into config.jsonc, preserving comments
// on the rest of the document. If name already exists, it is replaced.
// If default_profile is empty, it is set to this profile.
func (c *Config) AddProfile(name string, p Profile) error {
	if name == "" {
		return fmt.Errorf("add profile: empty name")
	}
	if !filepath.IsAbs(p.Dir) {
		return fmt.Errorf("add profile %q: dir must be absolute, got %q", name, p.Dir)
	}

	// Decode raw into a hujson Value for comment-preserving edits.
	v, err := hujson.Parse(c.raw)
	if err != nil {
		return fmt.Errorf("parse existing config: %w", err)
	}

	profValue, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal profile: %w", err)
	}

	op := "add"
	if _, exists := c.Profiles[name]; exists {
		op = "replace"
	}
	patch := fmt.Sprintf(`[{"op":%q,"path":"/profiles/%s","value":%s}]`, op, jsonPointerEscape(name), profValue)
	if err := v.Patch([]byte(patch)); err != nil {
		return fmt.Errorf("patch profiles/%s: %w", name, err)
	}

	// If no default_profile yet, set it to this one.
	if c.DefaultProfile == "" {
		dpPatch := fmt.Sprintf(`[{"op":"replace","path":"/default_profile","value":%q}]`, name)
		if err := v.Patch([]byte(dpPatch)); err != nil {
			return fmt.Errorf("patch default_profile: %w", err)
		}
		c.DefaultProfile = name
	}

	v.Format()
	c.raw = v.Pack()
	if c.Profiles == nil {
		c.Profiles = map[string]Profile{}
	}
	c.Profiles[name] = p
	return nil
}

// SetDefaultProfile updates default_profile. Name must exist in Profiles.
func (c *Config) SetDefaultProfile(name string) error {
	if _, ok := c.Profiles[name]; !ok {
		return fmt.Errorf("profile %q does not exist", name)
	}
	v, err := hujson.Parse(c.raw)
	if err != nil {
		return fmt.Errorf("parse existing config: %w", err)
	}
	patch := fmt.Sprintf(`[{"op":"replace","path":"/default_profile","value":%q}]`, name)
	if err := v.Patch([]byte(patch)); err != nil {
		return fmt.Errorf("patch default_profile: %w", err)
	}
	v.Format()
	c.raw = v.Pack()
	c.DefaultProfile = name
	return nil
}

// WriteFile atomically persists the current c.raw bytes to c.path.
// Uses a same-dir temp file + rename for crash safety.
func (c *Config) WriteFile() error {
	if c.path == "" {
		return fmt.Errorf("config has no path — call Load first or set via LoadBytes")
	}
	return writeAtomic(c.path, c.raw, 0o600)
}

// writeAtomic writes data to path via a temp file + rename.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".config-*.jsonc")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // best-effort cleanup if rename fails
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// BootstrapEmpty returns a *Config backed by InitialJSONC — used by setup
// when no config exists yet. Caller must set path before WriteFile.
func BootstrapEmpty(path string) *Config {
	c := &Config{
		Profiles: map[string]Profile{},
		raw:      []byte(InitialJSONC),
		path:     path,
	}
	return c
}

// jsonPointerEscape escapes a single key segment per RFC 6901: ~ → ~0, / → ~1.
func jsonPointerEscape(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '~':
			out = append(out, '~', '0')
		case '/':
			out = append(out, '~', '1')
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}
