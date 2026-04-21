// Package gitignore manages a repolink-owned block inside a consumer
// repo's .gitignore file. The block is a projection of the DB — every
// time the set of active mappings for this repo changes, callers call
// UpdateBlock with the new desired lines and the file is rewritten to
// match.
//
// Block shape:
//
//	# BEGIN repolink (managed — do not edit)
//	/research
//	/tools/my-snippet
//	# END repolink
//
// Lines outside the block are preserved verbatim. If the desired list
// is empty, the block is removed along with a single surrounding blank
// line so the file doesn't accumulate cruft.
package gitignore

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	beginMarker = "# BEGIN repolink (managed — do not edit)"
	endMarker   = "# END repolink"
)

// UpdateBlock reconciles the repolink block inside the .gitignore at
// `path` so it contains exactly `desired` (de-duped + sorted). Creates
// the file if missing, creates the block if missing, removes the block
// if desired is empty and no other content exists, leaves everything
// outside the block untouched. Atomic write via tempfile + rename.
func UpdateBlock(path string, desired []string) error {
	want := dedupSorted(desired)

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	rewritten, changed := rewrite(existing, want)
	if !changed {
		return nil
	}

	// If file now empty (no block, no other content), unlink.
	if len(bytes.TrimSpace(rewritten)) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return writeAtomic(path, rewritten, 0o644)
}

// rewrite returns the new file bytes for the given desired-set, plus a
// flag indicating whether the content actually changed.
func rewrite(existing []byte, want []string) ([]byte, bool) {
	before, _, after, hadBlock := splitBlock(existing)

	if len(want) == 0 && !hadBlock {
		return existing, false
	}
	if len(want) == 0 {
		// Drop the block. Trim one blank line preceding it if present,
		// and ditto after, to keep the rest of the file tidy.
		out := bytes.TrimRight(before, "\n")
		tail := bytes.TrimLeft(after, "\n")
		joined := out
		if len(joined) > 0 && len(tail) > 0 {
			joined = append(joined, '\n', '\n')
		}
		joined = append(joined, tail...)
		if !bytes.Equal(joined, existing) {
			return joined, true
		}
		return existing, false
	}

	// Build the new block.
	var buf bytes.Buffer
	buf.WriteString(beginMarker)
	buf.WriteByte('\n')
	for _, ln := range want {
		buf.WriteString(ln)
		buf.WriteByte('\n')
	}
	buf.WriteString(endMarker)
	buf.WriteByte('\n')

	var out bytes.Buffer
	if len(bytes.TrimSpace(before)) > 0 {
		out.Write(before)
		if !bytes.HasSuffix(before, []byte("\n")) {
			out.WriteByte('\n')
		}
		if !hadBlock {
			out.WriteByte('\n') // blank line separating user content from our block
		}
	}
	out.Write(buf.Bytes())
	if len(bytes.TrimSpace(after)) > 0 {
		if !bytes.HasPrefix(after, []byte("\n")) {
			out.WriteByte('\n')
		}
		out.Write(after)
	}

	result := out.Bytes()
	if bytes.Equal(result, existing) {
		return existing, false
	}
	return result, true
}

// splitBlock parses an existing .gitignore into (before-block,
// inside-block-contents, after-block, hadBlock). If the markers aren't
// found, hadBlock=false and before == existing.
func splitBlock(data []byte) (before, inside, after []byte, hadBlock bool) {
	if len(data) == 0 {
		return nil, nil, nil, false
	}
	bIdx := bytes.Index(data, []byte(beginMarker))
	if bIdx < 0 {
		return data, nil, nil, false
	}
	// Trailing search for end marker after begin.
	eIdx := bytes.Index(data[bIdx:], []byte(endMarker))
	if eIdx < 0 {
		// Unclosed block — treat as if no block exists; caller will
		// overwrite anyway.
		return data, nil, nil, false
	}
	eIdx += bIdx
	// Advance past the end marker's line.
	tailStart := eIdx + len(endMarker)
	if tailStart < len(data) && data[tailStart] == '\n' {
		tailStart++
	}

	// Rewind bIdx past any leading \n belonging to the block's leading line.
	for bIdx > 0 && data[bIdx-1] == '\n' {
		bIdx--
		break
	}

	before = data[:bIdx]
	// inside is between the two markers — rarely needed by callers;
	// included for completeness.
	if bodyStart := bytes.IndexByte(data[bIdx:], '\n'); bodyStart >= 0 {
		inside = data[bIdx+bodyStart+1 : eIdx]
	}
	after = data[tailStart:]
	return before, inside, after, true
}

// ReadBlock returns the lines currently inside the managed block, or nil
// if there's no block. Useful for tests + diagnostics.
func ReadBlock(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	_, inside, _, had := splitBlock(data)
	if !had {
		return nil, nil
	}
	sc := bufio.NewScanner(bytes.NewReader(inside))
	var out []string
	for sc.Scan() {
		ln := strings.TrimSpace(sc.Text())
		if ln == "" {
			continue
		}
		out = append(out, ln)
	}
	return out, sc.Err()
}

func dedupSorted(in []string) []string {
	seen := map[string]struct{}{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		seen[s] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".gitignore-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
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
