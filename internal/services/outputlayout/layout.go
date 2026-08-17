// Package outputlayout derives filesystem paths for episode output and ensures
// the required directory structure exists.
package outputlayout

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ZioSHik/kinopub-gui/internal/domain"
)

// Layout implements domain.OutputLayout.
type Layout struct {
	ext      string // file extension including the leading dot, e.g. ".mkv"
	dirTmpl  string // series-folder template, e.g. "{title} ({year})"
	nameTmpl string // file template, may contain "/" to nest deeper
}

// New creates a Layout for the given container format with the default
// templates, i.e. the historical <Title>/Season NN/SNNENN layout.
// The container determines the output file extension (.mkv or .mp4).
func New(container domain.Container) *Layout {
	return NewWithTemplates(container, "", "")
}

// NewWithTemplates creates a Layout with user-chosen path templates. An empty
// template falls back to its default, so callers can pass through unset config
// without special-casing. Templates should be validated with ValidateTemplate
// before reaching here; anything invalid that slips through degrades to a
// verbatim, sanitized path component rather than failing the download.
func NewWithTemplates(container domain.Container, dirTmpl, nameTmpl string) *Layout {
	ext := ".mkv"
	if container == domain.ContainerMP4 {
		ext = ".mp4"
	}
	if dirTmpl == "" {
		dirTmpl = DefaultDirTemplate
	}
	if nameTmpl == "" {
		nameTmpl = DefaultNameTemplate
	}
	return &Layout{ext: ext, dirTmpl: dirTmpl, nameTmpl: nameTmpl}
}

// SeriesDir returns the directory that holds one series' downloads — the folder
// the state file and poster live in. It is always at least one component below
// root, so a flat file template can never turn the output root itself into a
// series folder (which would collide between titles).
func (l *Layout) SeriesDir(root string, series domain.Series) string {
	vals := valuesFor(series, domain.Episode{})
	return filepath.Join(root, expand(l.dirTmpl, vals, seriesFallback(series)))
}

// EpisodePath builds the full output path for an episode:
//
//	root/<dir template>/<name template><ext>
//
// With the default templates that is the historical
// root/<sanitized series title>/Season <NN>/S<NN>E<NN>.<ext>. Every component
// is sanitized via fsutil.SanitizeComponent, and components that sanitize to
// nothing are dropped, so the result always stays inside root.
func (l *Layout) EpisodePath(root string, series domain.Series, ep domain.Episode) (string, error) {
	vals := valuesFor(series, ep)
	name := expand(l.nameTmpl, vals, episodeFallback(ep))
	return filepath.Join(l.SeriesDir(root, series), name+l.ext), nil
}

// seriesFallback is the series-folder name used when the template expands to
// nothing (e.g. an untitled series and a template of only "{title}").
func seriesFallback(series domain.Series) string {
	return fmt.Sprintf("series_%s", string(series.ID))
}

// episodeFallback is the file name used when the name template expands to
// nothing.
func episodeFallback(ep domain.Episode) string {
	return fmt.Sprintf("S%02dE%02d", ep.Key.Season, ep.Key.Episode)
}

// EnsureDirs creates all directories in the path up to and including the
// directory containing the file at path. It is idempotent: existing directories
// are not an error. Returns domain.ErrOutputDirUnwritable if the directories
// cannot be created.
func (l *Layout) EnsureDirs(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("%w: %s", domain.ErrOutputDirUnwritable, err.Error())
	}
	return nil
}
