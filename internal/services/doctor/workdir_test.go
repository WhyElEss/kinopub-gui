package doctor

import (
	"os"
	"path/filepath"
	"testing"
)

// testLogger reuses the package's no-op logger (see doctor_test.go).
func testLogger() nopLogger { return nopLogger{} }

// seedWorkDir builds a work folder with one leftover file and one leftover
// segment directory, as an interrupted download would.
func seedWorkDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "S01E01.mkv-abcd1234.ts"), make([]byte, 2048), 0644); err != nil {
		t.Fatal(err)
	}
	segs := filepath.Join(dir, "S01E02.mkv-beef5678.ts.hls-tmp")
	if err := os.MkdirAll(segs, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(segs, "seg-0001.ts"), make([]byte, 1024), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestScanWorkDirReportsWithoutCleaning(t *testing.T) {
	dir := seedWorkDir(t)
	rep, err := ScanWorkDir(dir, false, false, testLogger())
	if err != nil {
		t.Fatalf("ScanWorkDir: %v", err)
	}
	if len(rep.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(rep.Entries))
	}
	if rep.Bytes != 3072 {
		t.Errorf("bytes = %d, want 3072 (file + segment directory)", rep.Bytes)
	}
	if rep.Removed != 0 {
		t.Errorf("removed = %d, want 0 — a report must not delete anything", rep.Removed)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Errorf("the work folder was modified by a read-only scan")
	}
}

func TestScanWorkDirCleans(t *testing.T) {
	dir := seedWorkDir(t)
	rep, err := ScanWorkDir(dir, false, true, testLogger())
	if err != nil {
		t.Fatalf("ScanWorkDir: %v", err)
	}
	if rep.Removed != 2 {
		t.Errorf("removed = %d, want 2", rep.Removed)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("work folder still holds %d entries", len(entries))
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("the work folder itself must survive: %v", err)
	}
}

// The one rule that protects a paused download: while anything is in flight the
// folder is untouchable, because those files ARE the resume data.
func TestScanWorkDirLeavesBusyFolderAlone(t *testing.T) {
	dir := seedWorkDir(t)
	rep, err := ScanWorkDir(dir, true, true, testLogger())
	if err != nil {
		t.Fatalf("ScanWorkDir: %v", err)
	}
	if !rep.Busy {
		t.Error("report should say it was skipped as busy")
	}
	if len(rep.Entries) != 0 || rep.Removed != 0 {
		t.Errorf("a busy work folder must not be listed or cleaned: %+v", rep)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Fatalf("files disappeared from a busy work folder")
	}
}

func TestScanWorkDirHandlesUnsetAndMissing(t *testing.T) {
	rep, err := ScanWorkDir("", false, true, testLogger())
	if err != nil || len(rep.Entries) != 0 {
		t.Errorf("an unset work folder is not an error: %+v, %v", rep, err)
	}
	rep, err = ScanWorkDir(filepath.Join(t.TempDir(), "never-used"), false, true, testLogger())
	if err != nil || len(rep.Entries) != 0 {
		t.Errorf("a work folder that does not exist yet is not an error: %+v, %v", rep, err)
	}
}
