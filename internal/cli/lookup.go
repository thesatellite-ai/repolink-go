package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/khanakia/repolink-go/internal/store"
)

// ErrAmbiguous is returned when a user-provided identifier matches more
// than one mapping row. Callers render this as JSON code UUID_AMBIGUOUS.
var ErrAmbiguous = errors.New("lookup: ambiguous identifier")

// ErrNoMatch is returned when no row matches the identifier at all.
var ErrNoMatch = errors.New("lookup: no match")

// resolveMapping matches `ident` to exactly one mapping in the active
// profile's DB. Resolution order:
//  1. Exact id match.
//  2. UUID prefix (id begins with ident, 4+ chars required).
//  3. link_name match, scoped to repoURL if provided.
//
// If multiple rows match any stage, ErrAmbiguous is returned immediately
// (no fall-through to later stages).
func resolveMapping(ctx context.Context, st store.Store, ident, repoURL string) (store.Mapping, error) {
	if ident == "" {
		return store.Mapping{}, errors.New("lookup: empty identifier")
	}

	all, err := st.ListMappings(ctx, store.MappingFilter{})
	if err != nil {
		return store.Mapping{}, err
	}

	// 1) Exact id.
	for _, m := range all {
		if m.ID == ident {
			return m, nil
		}
	}

	// 2) UUID prefix (require 4+ chars to avoid accidental single-char hits).
	if isHexPrefixLike(ident) && len(ident) >= 4 {
		var hits []store.Mapping
		for _, m := range all {
			if strings.HasPrefix(m.ID, ident) {
				hits = append(hits, m)
			}
		}
		if len(hits) == 1 {
			return hits[0], nil
		}
		if len(hits) > 1 {
			return store.Mapping{}, fmt.Errorf("%w: prefix %q matches %d rows", ErrAmbiguous, ident, len(hits))
		}
	}

	// 3) link_name match. Scope to repoURL if available so "plan" in repo A
	//    doesn't accidentally match a "plan" link_name in repo B.
	var hits []store.Mapping
	for _, m := range all {
		if m.LinkName != ident {
			continue
		}
		if repoURL != "" && m.RepoURL != repoURL {
			continue
		}
		hits = append(hits, m)
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return store.Mapping{}, fmt.Errorf("%w: %q", ErrNoMatch, ident)
	default:
		var lines []string
		for _, h := range hits {
			tgt := h.RepoURL + "/" + h.TargetRel + "/" + h.LinkName
			if h.TargetRel == "" {
				tgt = h.RepoURL + "/" + h.LinkName
			}
			lines = append(lines, fmt.Sprintf("    %s  %-10s  %s", h.ID[:18]+"…", h.State, tgt))
		}
		return store.Mapping{}, fmt.Errorf("%w: link_name %q matches %d rows — re-run with one of these UUID prefixes:\n%s",
			ErrAmbiguous, ident, len(hits), strings.Join(lines, "\n"))
	}
}

// isHexPrefixLike returns true if the string could plausibly be a UUID
// prefix (hex chars + dashes only). Cheap check — avoids reading arbitrary
// link_names as prefixes.
func isHexPrefixLike(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		case r == '-':
		default:
			return false
		}
	}
	return true
}
