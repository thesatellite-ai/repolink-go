package symlinker

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func setup(t *testing.T) (srcDir, dstDir string) {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "consumer")
	if err := os.MkdirAll(src, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dst, 0o700); err != nil {
		t.Fatal(err)
	}
	return src, dst
}

func TestCompute_Create(t *testing.T) {
	src, dst := setup(t)
	in := Intent{SourceAbs: src, TargetAbs: filepath.Join(dst, "link"), Kind: "dir"}
	p := Compute([]Intent{in})
	if len(p.Actions) != 1 || p.Actions[0].Kind != ActCreate {
		t.Errorf("got %+v, want ActCreate", p.Actions)
	}
}

func TestCompute_Skip_AlreadyCorrect(t *testing.T) {
	src, dst := setup(t)
	target := filepath.Join(dst, "link")
	_ = os.Symlink(src, target)

	p := Compute([]Intent{{SourceAbs: src, TargetAbs: target, Kind: "dir"}})
	if p.Actions[0].Kind != ActSkip {
		t.Errorf("got %v, want ActSkip (%q)", p.Actions[0].Kind, p.Actions[0].Reason)
	}
}

func TestCompute_Replace_PointsElsewhere(t *testing.T) {
	src, dst := setup(t)
	other := filepath.Join(t.TempDir(), "other")
	_ = os.MkdirAll(other, 0o700)
	target := filepath.Join(dst, "link")
	_ = os.Symlink(other, target)

	p := Compute([]Intent{{SourceAbs: src, TargetAbs: target, Kind: "dir"}})
	if p.Actions[0].Kind != ActReplace {
		t.Errorf("got %v, want ActReplace", p.Actions[0].Kind)
	}
}

func TestSafety_S07_Compute_CollisionOnNonSymlink(t *testing.T) {
	src, dst := setup(t)
	target := filepath.Join(dst, "link")
	// Real file at target — must not be clobbered.
	if err := os.WriteFile(target, []byte("REAL"), 0o600); err != nil {
		t.Fatal(err)
	}

	p := Compute([]Intent{{SourceAbs: src, TargetAbs: target, Kind: "dir"}})
	if p.Actions[0].Kind != ActCollision {
		t.Errorf("got %v, want ActCollision", p.Actions[0].Kind)
	}
}

func TestCompute_SourceMissing(t *testing.T) {
	_, dst := setup(t)
	p := Compute([]Intent{{
		SourceAbs: "/nonexistent/xyz-abc",
		TargetAbs: filepath.Join(dst, "link"),
	}})
	if p.Actions[0].Kind != ActSourceMissing {
		t.Errorf("got %v, want ActSourceMissing", p.Actions[0].Kind)
	}
}

func TestApply_CreateThenSkip(t *testing.T) {
	src, dst := setup(t)
	target := filepath.Join(dst, "link")

	p := Compute([]Intent{{SourceAbs: src, TargetAbs: target, Kind: "dir"}})
	r, err := Apply(context.Background(), p, ApplyOpts{})
	if err != nil || len(r.Applied) != 1 {
		t.Fatalf("first apply: %v / %+v", err, r)
	}

	// Symlink exists and points at src.
	got, err := os.Readlink(target)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if got != src {
		t.Errorf("symlink: got %q want %q", got, src)
	}

	// Re-compute + re-apply → skip.
	p2 := Compute([]Intent{{SourceAbs: src, TargetAbs: target, Kind: "dir"}})
	r2, _ := Apply(context.Background(), p2, ApplyOpts{})
	if len(r2.Skipped) != 1 || len(r2.Applied) != 0 {
		t.Errorf("second apply: want 1 skip 0 applied, got %+v", r2)
	}
}

func TestSafety_S00_RemoveSymlinkRefusesNonSymlink(t *testing.T) {
	_, dst := setup(t)
	real := filepath.Join(dst, "real-file")
	_ = os.WriteFile(real, []byte("hi"), 0o600)

	if err := RemoveSymlink(real); err == nil {
		t.Error("RemoveSymlink on real file: expected err, got nil")
	}
	// Real file must still be there.
	if _, err := os.Stat(real); err != nil {
		t.Errorf("real file gone after failed RemoveSymlink: %v", err)
	}
}

func TestSafety_S00_RemovePreservesSourceDir(t *testing.T) {
	src, dst := setup(t)
	// Put real content inside source.
	inside := filepath.Join(src, "content.txt")
	_ = os.WriteFile(inside, []byte("precious"), 0o600)

	target := filepath.Join(dst, "link")
	_ = os.Symlink(src, target)

	if err := RemoveSymlink(target); err != nil {
		t.Fatalf("RemoveSymlink: %v", err)
	}
	// Source dir + content still intact.
	if _, err := os.Stat(inside); err != nil {
		t.Errorf("source content deleted via symlink removal: %v", err)
	}
}

func TestApply_DryRun(t *testing.T) {
	src, dst := setup(t)
	target := filepath.Join(dst, "link")

	p := Compute([]Intent{{SourceAbs: src, TargetAbs: target, Kind: "dir"}})
	r, err := Apply(context.Background(), p, ApplyOpts{DryRun: true})
	if err != nil {
		t.Fatalf("Apply(dry-run): %v", err)
	}
	if len(r.Applied) != 1 {
		t.Errorf("dry-run should mark applied, got %+v", r)
	}
	// Nothing on disk.
	if _, err := os.Lstat(target); err == nil {
		t.Error("dry-run created the symlink on disk")
	}
}

func TestApply_RemoveIntent(t *testing.T) {
	src, dst := setup(t)
	target := filepath.Join(dst, "link")
	_ = os.Symlink(src, target)

	p := Compute([]Intent{{TargetAbs: target, Remove: true}})
	r, err := Apply(context.Background(), p, ApplyOpts{})
	if err != nil {
		t.Fatalf("Apply(remove): %v", err)
	}
	if len(r.Applied) != 1 {
		t.Errorf("remove: want 1 applied, got %+v", r)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Errorf("symlink still on disk: %v", err)
	}
	// Source dir untouched.
	if _, err := os.Stat(src); err != nil {
		t.Errorf("source removed: %v", err)
	}
}
