package types

import (
	"strings"
	"testing"
)

// TestMVP_01_TypesConstructorsValidate covers MVP-01:
// constructors reject bad input, accept good input.
func TestMVP_01_TypesConstructorsValidate(t *testing.T) {
	t.Run("ProfileName", func(t *testing.T) {
		cases := map[string]bool{
			"work":                  true,
			"personal-2":            true,
			"AMan_K":                true,
			"":                      false,
			"has space":             false,
			"emoji-💥":               false,
			strings.Repeat("a", 65): false,
		}
		for in, wantOK := range cases {
			_, err := NewProfileName(in)
			if (err == nil) != wantOK {
				t.Errorf("NewProfileName(%q): got err=%v, wantOK=%v", in, err, wantOK)
			}
		}
	})

	t.Run("AbsPath", func(t *testing.T) {
		cases := map[string]bool{
			"/abs/path":     true,
			"/":             true,
			"/a//b/../b":    true,
			"":              false,
			"relative/path": false,
			"~/home":        false,
			"./dot":         false,
		}
		for in, wantOK := range cases {
			_, err := NewAbsPath(in)
			if (err == nil) != wantOK {
				t.Errorf("NewAbsPath(%q): got err=%v, wantOK=%v", in, err, wantOK)
			}
		}
		// cleaning
		p, _ := NewAbsPath("/a//b/../b")
		if p != "/a/b" {
			t.Errorf("NewAbsPath clean: got %q want /a/b", p)
		}
	})

	t.Run("RepoUUID", func(t *testing.T) {
		cases := map[string]bool{
			"550e8400-e29b-41d4-a716-446655440000": true,
			"550E8400-E29B-41D4-A716-446655440000": true,
			"not-a-uuid":                           false,
			"550e8400e29b41d4a716446655440000":     false,
			"":                                     false,
		}
		for in, wantOK := range cases {
			_, err := NewRepoUUID(in)
			if (err == nil) != wantOK {
				t.Errorf("NewRepoUUID(%q): got err=%v, wantOK=%v", in, err, wantOK)
			}
		}
	})

	t.Run("MappingState", func(t *testing.T) {
		for _, s := range []string{"active", "paused", "trashed"} {
			if _, err := NewMappingState(s); err != nil {
				t.Errorf("NewMappingState(%q) rejected: %v", s, err)
			}
		}
		if _, err := NewMappingState("deleted"); err == nil {
			t.Error("NewMappingState(\"deleted\"): want err, got nil")
		}
	})

	t.Run("SymlinkKind", func(t *testing.T) {
		for _, s := range []string{"file", "dir"} {
			if _, err := NewSymlinkKind(s); err != nil {
				t.Errorf("NewSymlinkKind(%q) rejected: %v", s, err)
			}
		}
		if _, err := NewSymlinkKind("symlink"); err == nil {
			t.Error("NewSymlinkKind(\"symlink\"): want err, got nil")
		}
	})
}

// TestMVP_01_TypesZeroValues verifies zero values are *not* silently valid.
func TestMVP_01_TypesZeroValues(t *testing.T) {
	var p ProfileName
	if _, err := NewProfileName(string(p)); err == nil {
		t.Error("zero ProfileName accepted")
	}
	var a AbsPath
	if _, err := NewAbsPath(string(a)); err == nil {
		t.Error("zero AbsPath accepted")
	}
	var u RepoUUID
	if _, err := NewRepoUUID(string(u)); err == nil {
		t.Error("zero RepoUUID accepted")
	}
	if (MappingState("")).Valid() {
		t.Error("zero MappingState reported valid")
	}
}
