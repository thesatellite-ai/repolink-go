package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/tailscale/hujson"
)

// Get reads a dotted-path key (resolved against the active profile when
// the path is a shorthand like `dir` or `scan_roots`). Returns the raw
// Go value (string, []string, bool, etc.).
func (c *Config) Get(key string) (any, error) {
	full, err := c.resolveKey(key)
	if err != nil {
		return nil, err
	}
	return c.getByPath(full)
}

// Set writes a dotted-path scalar key. Array keys are rejected (caller
// must use AddScanRoot / RemoveScanRoot instead). Value is auto-coerced:
// "true"/"false" → bool, pure-digit → int64, else string.
func (c *Config) Set(key, value string) error {
	spec, _, err := ValidateKey(resolveShorthand(key, c))
	if err != nil {
		return err
	}
	if spec.Kind == ArrayKey {
		return fmt.Errorf("%q is an array key; use --add-<singular> / --remove-<singular>", key)
	}
	full, err := c.resolveKey(key)
	if err != nil {
		return err
	}

	// Pre-validate specific keys before the patch lands — avoids writing
	// then erroring on reload (ValidateKey isn't per-value).
	if strings.HasSuffix(full, ".dir") || full == "dir" {
		if !filepath.IsAbs(value) {
			return fmt.Errorf("%q: must be absolute path, got %q", key, value)
		}
	}

	if full == "default_profile" {
		if _, ok := c.Profiles[value]; !ok {
			return fmt.Errorf("default_profile: profile %q does not exist", value)
		}
	}

	// Marshal coerced value.
	coerced := coerceValue(value)
	raw, _ := json.Marshal(coerced)

	op := "replace"
	if !c.pathExists(full) {
		op = "add"
	}
	pointer := jsonPointerFromPath(full)
	patch := fmt.Sprintf(`[{"op":%q,"path":%q,"value":%s}]`, op, pointer, raw)

	v, err := hujson.Parse(c.raw)
	if err != nil {
		return fmt.Errorf("parse existing config: %w", err)
	}
	if err := v.Patch([]byte(patch)); err != nil {
		return fmt.Errorf("patch %s: %w", pointer, err)
	}
	c.raw = v.Pack()

	// Re-parse into struct state so Get sees the update.
	return c.reparse()
}

// Unset removes a dotted-path key. Refuses required fields (profile.dir on
// the only profile).
func (c *Config) Unset(key string) error {
	_, _, err := ValidateKey(resolveShorthand(key, c))
	if err != nil {
		return err
	}
	full, err := c.resolveKey(key)
	if err != nil {
		return err
	}
	if !c.pathExists(full) {
		return fmt.Errorf("%q: not set", key)
	}

	if strings.HasSuffix(full, ".dir") && len(c.Profiles) == 1 {
		return errors.New("cannot unset required field `dir` on the only profile")
	}

	pointer := jsonPointerFromPath(full)
	patch := fmt.Sprintf(`[{"op":"remove","path":%q}]`, pointer)
	v, err := hujson.Parse(c.raw)
	if err != nil {
		return err
	}
	if err := v.Patch([]byte(patch)); err != nil {
		return fmt.Errorf("patch remove %s: %w", pointer, err)
	}
	c.raw = v.Pack()
	return c.reparse()
}

// AddScanRoot appends path to the active profile's scan_roots (de-duped +
// normalized to clean absolute form). Empty scan_roots is initialized
// automatically.
func (c *Config) AddScanRoot(path string) error {
	profName := c.DefaultProfile
	if profName == "" {
		return errors.New("no default_profile set")
	}
	return c.AddScanRootTo(profName, path)
}

// AddScanRootTo appends to a named profile's scan_roots.
func (c *Config) AddScanRootTo(profName, path string) error {
	if _, ok := c.Profiles[profName]; !ok {
		return fmt.Errorf("profile %q does not exist", profName)
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("scan_root %q: must be absolute", path)
	}
	clean := filepath.Clean(path)
	p := c.Profiles[profName]
	for _, r := range p.ScanRoots {
		if filepath.Clean(r) == clean {
			return nil // already present — idempotent no-op
		}
	}
	p.ScanRoots = append(p.ScanRoots, clean)
	if err := c.replaceProfile(profName, p); err != nil {
		return err
	}
	return c.reparse()
}

// RemoveScanRoot removes path from the active profile's scan_roots. Idempotent.
func (c *Config) RemoveScanRoot(path string) error {
	profName := c.DefaultProfile
	if profName == "" {
		return errors.New("no default_profile set")
	}
	return c.RemoveScanRootFrom(profName, path)
}

func (c *Config) RemoveScanRootFrom(profName, path string) error {
	if _, ok := c.Profiles[profName]; !ok {
		return fmt.Errorf("profile %q does not exist", profName)
	}
	clean := filepath.Clean(path)
	p := c.Profiles[profName]
	filtered := make([]string, 0, len(p.ScanRoots))
	for _, r := range p.ScanRoots {
		if filepath.Clean(r) != clean {
			filtered = append(filtered, r)
		}
	}
	p.ScanRoots = filtered
	if err := c.replaceProfile(profName, p); err != nil {
		return err
	}
	return c.reparse()
}

// replaceProfile rewrites a single profiles/<name> entry via hujson.Patch.
func (c *Config) replaceProfile(name string, p Profile) error {
	v, err := hujson.Parse(c.raw)
	if err != nil {
		return err
	}
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	pointer := "/profiles/" + jsonPointerEscape(name)
	op := "replace"
	if _, exists := c.Profiles[name]; !exists {
		op = "add"
	}
	patch := fmt.Sprintf(`[{"op":%q,"path":%q,"value":%s}]`, op, pointer, body)
	if err := v.Patch([]byte(patch)); err != nil {
		return fmt.Errorf("patch profiles/%s: %w", name, err)
	}
	c.raw = v.Pack()
	return nil
}

// resolveKey expands shorthand (`dir` → `profiles.<active>.dir`) and
// returns the full dotted path. Non-shorthand keys pass through.
func (c *Config) resolveKey(key string) (string, error) {
	switch key {
	case "dir", "scan_roots":
		if c.DefaultProfile == "" {
			return "", errors.New("shorthand key requires default_profile set")
		}
		return "profiles." + c.DefaultProfile + "." + key, nil
	}
	return key, nil
}

// resolveShorthand is like resolveKey but for ValidateKey lookups — it
// replaces the active profile name with the `<name>` spec placeholder so
// the key matches the allowlist.
func resolveShorthand(key string, c *Config) string {
	if key == "dir" || key == "scan_roots" {
		return key
	}
	if strings.HasPrefix(key, "profiles.") {
		parts := strings.Split(key, ".")
		if len(parts) >= 3 {
			parts[1] = "<name>"
			return strings.Join(parts, ".")
		}
	}
	return key
}

// getByPath reads a dotted path from the parsed struct.
func (c *Config) getByPath(full string) (any, error) {
	switch full {
	case "default_profile":
		return c.DefaultProfile, nil
	}
	if strings.HasPrefix(full, "profiles.") {
		parts := strings.Split(full, ".")
		if len(parts) < 3 {
			return nil, fmt.Errorf("invalid key %q", full)
		}
		name := parts[1]
		p, ok := c.Profiles[name]
		if !ok {
			return nil, fmt.Errorf("profile %q does not exist", name)
		}
		leaf := parts[2]
		switch leaf {
		case "dir":
			return p.Dir, nil
		case "scan_roots":
			return p.ScanRoots, nil
		}
		return nil, fmt.Errorf("unknown field profiles.%s.%s", name, leaf)
	}
	return nil, fmt.Errorf("unknown key %q", full)
}

// pathExists returns true if the path is currently present in the parsed
// config (used to pick patch "add" vs "replace").
func (c *Config) pathExists(full string) bool {
	v, err := c.getByPath(full)
	if err != nil {
		return false
	}
	switch x := v.(type) {
	case string:
		return x != ""
	case []string:
		return x != nil
	}
	return v != nil
}

// jsonPointerFromPath turns "profiles.work.dir" → "/profiles/work/dir".
// Per RFC 6901 segments are escaped (~ → ~0, / → ~1).
func jsonPointerFromPath(full string) string {
	parts := strings.Split(full, ".")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, jsonPointerEscape(p))
	}
	return "/" + strings.Join(out, "/")
}

// coerceValue auto-types a user-supplied string for --set.
func coerceValue(s string) any {
	switch strings.ToLower(s) {
	case "true":
		return true
	case "false":
		return false
	}
	return s
}

// reparse re-decodes c.raw into Config fields. Called after every write
// so in-memory state matches persisted JSONC. Keeps raw as authority.
func (c *Config) reparse() error {
	stdCopy := append([]byte(nil), c.raw...)
	std, err := hujson.Standardize(stdCopy)
	if err != nil {
		return err
	}
	var fresh Config
	if err := json.Unmarshal(std, &fresh); err != nil {
		return err
	}
	c.DefaultProfile = fresh.DefaultProfile
	c.Profiles = fresh.Profiles
	return c.validate()
}
