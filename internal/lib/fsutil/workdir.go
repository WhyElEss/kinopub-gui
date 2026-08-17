package fsutil

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// WorkPath returns where a download's intermediate file (segments, the joined
// stream, ffmpeg's output) should live.
//
// With an empty workDir it is outPath+suffix — the historical behaviour of
// working right next to the final file. With a work directory set, the file
// moves there under a name derived from the output: the final file's base name
// keeps it recognisable, and a hash of the FULL output path keeps two titles
// that happen to share a file name (say the same episode in two libraries) from
// colliding in one flat folder.
//
// The name is a pure function of outPath, so an interrupted download still
// finds its own partial data after a restart and resumes instead of starting
// over.
func WorkPath(workDir, outPath, suffix string) string {
	if workDir == "" {
		return outPath + suffix
	}
	sum := sha256.Sum256([]byte(filepath.Clean(outPath)))
	base := SanitizeComponent(filepath.Base(outPath), "download")
	return filepath.Join(workDir, fmt.Sprintf("%s-%s%s", base, hex.EncodeToString(sum[:4]), suffix))
}

// MoveFile puts src at dst, replacing whatever is there.
//
// The fast path is a rename, which is atomic: readers of the destination
// directory — a media server scanning the library, say — never observe a
// half-written file under the final name. A rename cannot cross filesystems,
// though, and a work directory on another disk is a perfectly reasonable setup,
// so the fallback copies into a temporary file NEXT TO the destination and
// renames that. The final appearance stays atomic either way; only the copy
// costs an extra pass over the data.
func MoveFile(src, dst string) error {
	renameErr := os.Rename(src, dst)
	if renameErr == nil {
		return nil
	}
	// Any rename failure falls through to the copy rather than only EXDEV: the
	// error differs per platform (EXDEV, ERROR_NOT_SAME_DEVICE), and a copy that
	// cannot work either reports the real problem below.
	if err := copyAcross(src, dst); err != nil {
		return errors.Join(renameErr, err)
	}
	return os.Remove(src)
}

// copyAcross copies src to dst via a sibling temp file, fsyncing before the
// rename so a power cut cannot leave a truncated file under the final name.
func copyAcross(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// EnsureWorkDir creates the work directory when one is configured. An empty
// workDir is not an error: it means "work next to the output file", which needs
// no directory of its own.
func EnsureWorkDir(workDir string) error {
	if workDir == "" {
		return nil
	}
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return fmt.Errorf("work folder %s: %w", workDir, err)
	}
	return nil
}
