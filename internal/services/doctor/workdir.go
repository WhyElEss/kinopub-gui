package doctor

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ZioSHik/kinopub-gui/internal/domain"
)

// WorkDirEntry is one leftover in the work folder.
type WorkDirEntry struct {
	Path  string
	Bytes int64
	IsDir bool
}

// WorkDirReport summarizes what the work folder holds.
type WorkDirReport struct {
	Dir     string
	Entries []WorkDirEntry
	Bytes   int64
	Removed int
	// Busy is set when the caller said a download is in progress. Nothing is
	// listed or removed then — see ScanWorkDir.
	Busy bool
}

// ScanWorkDir reports (and optionally removes) what a download left behind in
// the work folder.
//
// Every entry there belongs to a download in flight, so the only safe moment to
// judge them is when nothing is downloading: then, by construction, whatever
// remains is debris from a run that died — a hard kill, a power cut, a crash —
// and no resume will ever look for it again. That is why the caller passes
// busy: with a job queued, running or PAUSED, the folder is left strictly
// alone, because a paused download's segments are exactly what its resume needs.
//
// Only the top level is enumerated: entries are per-episode files and segment
// directories, and a directory is reported (and removed) as one item.
func ScanWorkDir(workDir string, busy, clean bool, log domain.Logger) (*WorkDirReport, error) {
	rep := &WorkDirReport{Dir: workDir, Busy: busy}
	if workDir == "" {
		return rep, nil
	}
	if busy {
		return rep, nil
	}

	entries, err := os.ReadDir(workDir)
	if err != nil {
		if os.IsNotExist(err) {
			return rep, nil // never used yet — nothing to clean
		}
		return rep, fmt.Errorf("read work folder: %w", err)
	}

	for _, e := range entries {
		path := filepath.Join(workDir, e.Name())
		size := entrySize(path, e.IsDir())
		rep.Entries = append(rep.Entries, WorkDirEntry{Path: path, Bytes: size, IsDir: e.IsDir()})
		rep.Bytes += size
		if !clean {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			log.Warn("could not remove work folder leftover",
				domain.F("path", path),
				domain.F("error", err.Error()),
			)
			continue
		}
		rep.Removed++
		log.Info("removed work folder leftover",
			domain.F("path", path),
			domain.F("bytes", size),
		)
	}
	return rep, nil
}

// entrySize is the size of a file, or the total of everything under a
// directory. Unreadable parts count as zero rather than failing the scan: the
// number is there to tell the user how much space they get back.
func entrySize(path string, isDir bool) int64 {
	if !isDir {
		if info, err := os.Stat(path); err == nil {
			return info.Size()
		}
		return 0
	}
	var total int64
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, statErr := d.Info(); statErr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
