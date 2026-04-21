package config

// Key kinds allow the CLI validator to pick the right verb:
//
//	scalar → --set / --get / --unset
//	array  → --add-<singular> / --remove-<singular>
type KeyKind int

const (
	ScalarKey KeyKind = iota
	ArrayKey
)

// KeySpec describes one allowed config key.
type KeySpec struct {
	Path    string  // dotted path, with "<name>" token for per-profile keys (e.g. "profiles.<name>.dir")
	Kind    KeyKind // scalar or array
	Type    string  // "string" | "[]string" | "bool" | "int" — for error messages
	AbsPath bool    // if true, values must be absolute filesystem paths
}

// Allowlist is the single source of truth for what `repolink config` will
// accept. Adding a new knob = append here. CLI verb generation (e.g.
// --add-scan-root) is driven off this list.
var Allowlist = []KeySpec{
	{Path: "default_profile", Kind: ScalarKey, Type: "string"},
	{Path: "dir", Kind: ScalarKey, Type: "string", AbsPath: true},         // shorthand → active profile
	{Path: "scan_roots", Kind: ArrayKey, Type: "[]string", AbsPath: true}, // shorthand → active profile
	{Path: "profiles.<name>.dir", Kind: ScalarKey, Type: "string", AbsPath: true},
	{Path: "profiles.<name>.scan_roots", Kind: ArrayKey, Type: "[]string", AbsPath: true},
}

// KnownKeys returns just the dotted path strings, useful for error hints.
func KnownKeys() []string {
	out := make([]string, 0, len(Allowlist))
	for _, k := range Allowlist {
		out = append(out, k.Path)
	}
	return out
}
