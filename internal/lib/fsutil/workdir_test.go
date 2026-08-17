package fsutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkPathWithoutWorkDirIsUnchanged(t *testing.T) {
	got := WorkPath("", "/media/Movies/Film (1999)/Film (1999).mkv", ".ts")
	want := "/media/Movies/Film (1999)/Film (1999).mkv.ts"
	if got != want {
		t.Errorf("WorkPath = %q, want %q", got, want)
	}
}

func TestWorkPathMovesIntoWorkDir(t *testing.T) {
	got := WorkPath("/work", "/media/Movies/Film (1999)/Film (1999).mkv", ".ts")
	if filepath.Dir(got) != "/work" {
		t.Errorf("WorkPath = %q, want it inside /work", got)
	}
	if !strings.HasSuffix(got, ".ts") {
		t.Errorf("WorkPath = %q, want a .ts suffix", got)
	}
	if !strings.Contains(filepath.Base(got), "Film (1999).mkv") {
		t.Errorf("WorkPath = %q, want the output name kept for recognisability", got)
	}
}

// Two titles can share a file name in different folders; in one flat work
// directory they must not collide.
func TestWorkPathDistinguishesSameNamedOutputs(t *testing.T) {
	a := WorkPath("/work", "/media/TV/Show A/Season 01/S01E01.mkv", ".ts")
	b := WorkPath("/work", "/media/TV/Show B/Season 01/S01E01.mkv", ".ts")
	if a == b {
		t.Errorf("different outputs share the work path %q", a)
	}
}

// The name has to be stable, or an interrupted download would not find its own
// partial segments after a restart and would start from zero.
func TestWorkPathIsDeterministic(t *testing.T) {
	out := "/media/TV/Show/Season 01/S01E01.mkv"
	if WorkPath("/work", out, ".ts") != WorkPath("/work", out, ".ts") {
		t.Error("WorkPath is not deterministic")
	}
}

func TestMoveFileWithinOneFilesystem(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.tmp")
	dst := filepath.Join(dir, "final.mkv")
	if err := os.WriteFile(src, []byte("payload"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := MoveFile(src, dst); err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if data, err := os.ReadFile(dst); err != nil || string(data) != "payload" {
		t.Errorf("destination = %q, %v", data, err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source should be gone after a move")
	}
}

// The cross-filesystem path can't be provoked portably, so the copy fallback is
// exercised directly: it must leave a complete destination and no leftovers.
func TestCopyAcrossLeavesNoTempBehind(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.tmp")
	dst := filepath.Join(dir, "final.mkv")
	payload := strings.Repeat("x", 1<<16)
	if err := os.WriteFile(src, []byte(payload), 0644); err != nil {
		t.Fatal(err)
	}
	if err := copyAcross(src, dst); err != nil {
		t.Fatalf("copyAcross: %v", err)
	}
	if data, err := os.ReadFile(dst); err != nil || string(data) != payload {
		t.Errorf("destination is not a faithful copy (%d bytes, %v)", len(data), err)
	}
	if _, err := os.Stat(dst + ".tmp"); !os.IsNotExist(err) {
		t.Error("the sibling temp file should be gone")
	}
}

func TestMoveFileReportsRealFailure(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.tmp")
	if err := os.WriteFile(src, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	// A destination inside a non-existent directory fails both the rename and
	// the copy; the error must surface rather than being swallowed.
	if err := MoveFile(src, filepath.Join(dir, "missing", "final.mkv")); err == nil {
		t.Error("MoveFile into a missing directory should fail")
	}
}

func TestEnsureWorkDir(t *testing.T) {
	if err := EnsureWorkDir(""); err != nil {
		t.Errorf("an empty work dir is not an error: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "nested", "work")
	if err := EnsureWorkDir(dir); err != nil {
		t.Fatalf("EnsureWorkDir: %v", err)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Errorf("work dir was not created: %v", err)
	}
}
