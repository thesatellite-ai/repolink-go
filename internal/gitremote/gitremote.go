// Package gitremote reads a consumer repo's git identity:
//   - FindRepoRoot  — walk up CWD to locate .git (dir or gitlink file)
//   - ReadOriginURL — parse .git/config for [remote "origin"] url
//   - NormalizeURL  — canonical "github.com/owner/repo" form (no scheme, no .git)
//
// No external git binary required. Parses the ini-shaped .git/config directly.
package gitremote

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrNoGitRepo is returned when no .git is found between start and filesystem root.
var ErrNoGitRepo = errors.New("gitremote: no .git found walking up from path")

// ErrNoOriginRemote is returned when .git/config has no [remote "origin"] url.
var ErrNoOriginRemote = errors.New("gitremote: no [remote \"origin\"] url in .git/config")

// FindRepoRoot walks up from start looking for a `.git` entry. Returns
// the directory containing `.git`. Handles both `.git` as a directory
// (normal repo) and `.git` as a gitlink file (worktrees / submodules —
// contents like `gitdir: /path/to/actual/git`).
func FindRepoRoot(start string) (string, error) {
	cur, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		dotgit := filepath.Join(cur, ".git")
		if info, err := os.Lstat(dotgit); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return cur, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", ErrNoGitRepo
		}
		cur = parent
	}
}

// gitDirOf returns the absolute path to the .git *directory* for the repo
// whose working tree root is repoRoot. Follows the `gitdir:` indirection
// if .git is a regular file (worktree / submodule).
func gitDirOf(repoRoot string) (string, error) {
	dotgit := filepath.Join(repoRoot, ".git")
	info, err := os.Lstat(dotgit)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return dotgit, nil
	}
	// Regular file — read "gitdir: <path>" and resolve relative to repoRoot.
	data, err := os.ReadFile(dotgit)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(data))
	const prefix = "gitdir:"
	if !strings.HasPrefix(line, prefix) {
		return "", fmt.Errorf("unrecognized .git gitlink file: %q", line)
	}
	p := strings.TrimSpace(line[len(prefix):])
	if !filepath.IsAbs(p) {
		p = filepath.Join(repoRoot, p)
	}
	return filepath.Clean(p), nil
}

// ReadOriginURL reads `[remote "origin"] url = ...` from the repo's
// .git/config. Multiple remotes: spec says "always match against origin"
// on ambiguous configs (PROBLEM.md Decisions).
func ReadOriginURL(repoRoot string) (string, error) {
	gitDir, err := gitDirOf(repoRoot)
	if err != nil {
		return "", err
	}
	f, err := os.Open(filepath.Join(gitDir, "config"))
	if err != nil {
		return "", err
	}
	defer f.Close()

	var (
		inOrigin bool
		url      string
	)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inOrigin = isOriginHeader(line)
			continue
		}
		if !inOrigin {
			continue
		}
		if k, v, ok := splitKV(line); ok && strings.EqualFold(k, "url") {
			url = v
			// Don't break — later definitions override earlier ones per git semantics.
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	if url == "" {
		return "", ErrNoOriginRemote
	}
	return url, nil
}

// isOriginHeader recognizes [remote "origin"] with various whitespace.
func isOriginHeader(header string) bool {
	body := strings.TrimSpace(header[1 : len(header)-1])
	if !strings.HasPrefix(body, "remote") {
		return false
	}
	body = strings.TrimSpace(body[len("remote"):])
	// Expect `"origin"` — strip quotes, compare.
	body = strings.Trim(body, `"`)
	return body == "origin"
}

func splitKV(line string) (k, v string, ok bool) {
	i := strings.Index(line, "=")
	if i < 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:]), true
}

// NormalizeURL returns the canonical `host/owner/repo` form used as
// repo_url in the DB. Handles:
//
//	https://github.com/khanakia/abc.git
//	git@github.com:khanakia/abc.git
//	ssh://git@github.com/khanakia/abc
//	github.com/khanakia/abc    (already-canonical passes through)
//
// Returns an error for obviously malformed input.
func NormalizeURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("empty url")
	}

	// Strip scheme.
	for _, prefix := range []string{"https://", "http://", "ssh://", "git://"} {
		if strings.HasPrefix(s, prefix) {
			s = s[len(prefix):]
			break
		}
	}
	// SCP-style: git@host:owner/repo(.git)
	if strings.HasPrefix(s, "git@") {
		s = s[len("git@"):]
		if i := strings.Index(s, ":"); i > 0 {
			s = s[:i] + "/" + s[i+1:]
		}
	}
	// user@host/path — strip userinfo.
	if at := strings.Index(s, "@"); at >= 0 && !strings.Contains(s[:at], "/") {
		s = s[at+1:]
	}

	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimSuffix(s, "/")

	// Basic shape: host/owner/repo (or host/path with >= 2 segments).
	parts := strings.Split(s, "/")
	if len(parts) < 3 {
		return "", fmt.Errorf("unrecognized remote URL shape: %q", raw)
	}
	// Lowercase the host — GitHub URLs are case-insensitive there.
	parts[0] = strings.ToLower(parts[0])
	return strings.Join(parts, "/"), nil
}

// ResolveFromCWD is the one-call helper most callers want: walk up from
// start, read origin, normalize — returns (repoRoot, canonicalURL, err).
func ResolveFromCWD(start string) (string, string, error) {
	root, err := FindRepoRoot(start)
	if err != nil {
		return "", "", err
	}
	raw, err := ReadOriginURL(root)
	if err != nil {
		return "", "", err
	}
	url, err := NormalizeURL(raw)
	if err != nil {
		return "", "", err
	}
	return root, url, nil
}

// Identity is the git `user.email` / `user.name` pair stamped onto
// mapping audit fields (created_by_*, updated_by_*) and run_logs.
type Identity struct {
	Email string
	Name  string
}

// ReadIdentity resolves the git identity using git's own precedence:
//  1. <repoRoot>/.git/config  [user] section
//  2. $XDG_CONFIG_HOME/git/config
//  3. ~/.gitconfig
//
// Returns the first section that supplies both email and name, falling
// back gracefully when only one is set. If neither source defines a
// value the returned Identity has empty fields — callers can choose
// whether an empty stamp is fatal (link/sync usually aren't, meta
// rename can tolerate it).
func ReadIdentity(repoRoot string) (Identity, error) {
	var id Identity

	// Per-repo config (highest precedence).
	if repoRoot != "" {
		if gitDir, err := gitDirOf(repoRoot); err == nil {
			id = mergeIdentity(id, readUserSection(filepath.Join(gitDir, "config")))
		}
	}
	if id.Email != "" && id.Name != "" {
		return id, nil
	}

	// $XDG_CONFIG_HOME/git/config (git's modern location).
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		id = mergeIdentity(id, readUserSection(filepath.Join(xdg, "git", "config")))
	}
	if id.Email != "" && id.Name != "" {
		return id, nil
	}

	// ~/.gitconfig (traditional location).
	if home, err := os.UserHomeDir(); err == nil {
		id = mergeIdentity(id, readUserSection(filepath.Join(home, ".gitconfig")))
	}
	return id, nil
}

// readUserSection parses one git config file and extracts the `user`
// section's email + name. Returns a zero Identity if the file is missing
// or the section is absent — never an error (caller falls through).
func readUserSection(path string) Identity {
	f, err := os.Open(path)
	if err != nil {
		return Identity{}
	}
	defer f.Close()

	var (
		inUser bool
		id     Identity
	)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			body := strings.TrimSpace(line[1 : len(line)-1])
			inUser = strings.EqualFold(body, "user")
			continue
		}
		if !inUser {
			continue
		}
		if k, v, ok := splitKV(line); ok {
			switch strings.ToLower(k) {
			case "email":
				id.Email = v
			case "name":
				id.Name = v
			}
		}
	}
	return id
}

// mergeIdentity fills empty fields in dst from src. Higher-precedence
// calls happen first, so dst wins when both set a field.
func mergeIdentity(dst, src Identity) Identity {
	if dst.Email == "" {
		dst.Email = src.Email
	}
	if dst.Name == "" {
		dst.Name = src.Name
	}
	return dst
}
