// Package config loads ~/.repolink/config.jsonc via tailscale/hujson
// (comment-preserving) and exposes the parsed Config + active-profile
// resolution. Writes are comment-preserving patches.
//
// Spec: docs/PROBLEM.md "Config file: ~/.repolink/config.jsonc" +
// "`repolink config` — machine config management".
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tailscale/hujson"
)

// DefaultPath returns the expected config location, honoring $REPOLINK_CONFIG
// for tests and $HOME for normal runs.
func DefaultPath() (string, error) {
	if p := os.Getenv("REPOLINK_CONFIG"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".repolink", "config.jsonc"), nil
}

// Config mirrors config.jsonc. Raw bytes are kept for comment-preserving
// writebacks via hujson.Patch.
type Config struct {
	DefaultProfile string             `json:"default_profile"`
	Profiles       map[string]Profile `json:"profiles"`

	raw  []byte // original file bytes (comment-preserving round-trip base)
	path string // where it was loaded from
}

// Profile is one private-repo registration on this machine.
type Profile struct {
	Dir       string   `json:"dir"`
	ScanRoots []string `json:"scan_roots,omitempty"`
}

// ErrNotFound is returned by Load when the config file does not exist.
// Callers typically fall through to `setup` bootstrap flow.
var ErrNotFound = errors.New("config.jsonc not found")

// Load reads + parses the config at path. If path is "", DefaultPath() is
// used. Returns ErrNotFound if the file is missing.
func Load(path string) (*Config, error) {
	if path == "" {
		p, err := DefaultPath()
		if err != nil {
			return nil, err
		}
		path = p
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	// hujson.Standardize mutates its input, replacing comment chars with
	// spaces. Pass a copy so raw stays pristine for comment-preserving
	// writeback later.
	stdCopy := append([]byte(nil), raw...)
	std, err := hujson.Standardize(stdCopy)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	var c Config
	dec := json.NewDecoder(bytes.NewReader(std))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	c.raw = raw
	c.path = path

	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("validate %s: %w", path, err)
	}
	return &c, nil
}

// Path returns the file path this Config was loaded from.
func (c *Config) Path() string { return c.path }

// Raw returns the original file bytes (comments preserved).
func (c *Config) Raw() []byte { return append([]byte(nil), c.raw...) }

// Resolve returns the Profile to use for this invocation. If override is
// non-empty it takes precedence over default_profile. Error if the chosen
// name does not exist in Profiles.
func (c *Config) Resolve(override string) (string, Profile, error) {
	name := override
	if name == "" {
		name = c.DefaultProfile
	}
	if name == "" {
		return "", Profile{}, errors.New("no active profile: config has no default_profile and no --profile override")
	}
	p, ok := c.Profiles[name]
	if !ok {
		return "", Profile{}, fmt.Errorf("profile %q does not exist (known: %s)", name, c.profileNames())
	}
	return name, p, nil
}

func (c *Config) profileNames() string {
	if len(c.Profiles) == 0 {
		return "<none>"
	}
	var out string
	first := true
	for k := range c.Profiles {
		if !first {
			out += ", "
		}
		out += k
		first = false
	}
	return out
}

