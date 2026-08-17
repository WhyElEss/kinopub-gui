package outputlayout

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ZioSHik/kinopub-gui/internal/domain"
	"github.com/ZioSHik/kinopub-gui/internal/lib/fsutil"
)

// Default templates reproduce the historical layout exactly:
//
//	<root>/<Series Title>/Season <NN>/S<NN>E<NN>.<ext>
//
// DefaultDirTemplate names the series folder — the folder that also holds
// .kinopub-state.json and the poster, so it must always resolve to at least one
// path component. DefaultNameTemplate names the file inside it and may contain
// "/" to nest further (the season folder is part of the file template, not the
// series folder, because it varies per episode).
const (
	DefaultDirTemplate  = "{title}"
	DefaultNameTemplate = "Season {season:02}/S{season:02}E{episode:02}"
)

// Movie defaults deliberately differ from the series ones: a film has no
// seasons, and every media server (Plex, Jellyfin, Emby) expects
// "<Name> (<year>)/<Name> (<year>).mkv" rather than a Season 01 folder holding
// an S01E01 file. The original title is used rather than the Russian one because
// that is what those servers match against their metadata sources; {original}
// falls back to the Russian title when a film has no original one.
const (
	DefaultMovieDirTemplate  = "{original} ({year})"
	DefaultMovieNameTemplate = "{original} ({year})"
)

// tokenValues holds the substitution values for one episode.
type tokenValues struct {
	title    string // title as kino.pub gives it, e.g. "Рик и Морти / Rick and Morty"
	ru       string // Russian half of a combined title
	original string // original-language half, or the item's subname
	year     int
	id       string
	season   int
	episode  int
	epTitle  string
	quality  string
}

// stringTokens maps a token name to a plain string value.
func (v tokenValues) stringTokens() map[string]string {
	year := ""
	if v.year > 0 {
		year = strconv.Itoa(v.year)
	}
	return map[string]string{
		"title":    v.title,
		"ru":       firstNonEmpty(v.ru, v.title),
		"original": firstNonEmpty(v.original, v.ru, v.title),
		"year":     year,
		"id":       v.id,
		"eptitle":  v.epTitle,
		"quality":  v.quality,
	}
}

// numberTokens maps a token name to a numeric value, which alone accepts the
// ":0N" zero-padding suffix.
func (v tokenValues) numberTokens() map[string]int {
	return map[string]int{
		"season":  v.season,
		"episode": v.episode,
	}
}

// KnownTokens returns every supported token spelled as it appears in a template,
// sorted, for error messages and UI hints.
func KnownTokens() []string {
	var v tokenValues
	out := make([]string, 0, 8)
	for name := range v.stringTokens() {
		out = append(out, "{"+canonicalToken(name)+"}")
	}
	for name := range v.numberTokens() {
		out = append(out, "{"+name+"}", "{"+name+":02}")
	}
	sort.Strings(out)
	return out
}

// canonicalToken restores the camelCase spelling of a lowercased token name so
// hints read the way users type them.
func canonicalToken(lower string) string {
	if lower == "eptitle" {
		return "epTitle"
	}
	return lower
}

// ValidateTemplate reports whether tmpl is a usable path template: non-empty,
// relative, with balanced braces and only known tokens. It does not check that
// the expansion is non-empty — expansion falls back to a safe default instead.
func ValidateTemplate(tmpl string) error {
	trimmed := strings.TrimSpace(tmpl)
	if trimmed == "" {
		return fmt.Errorf("template is empty")
	}
	if filepath.IsAbs(trimmed) || strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, `\`) {
		return fmt.Errorf("template must be relative, not %q", trimmed)
	}
	var v tokenValues
	strs, nums := v.stringTokens(), v.numberTokens()

	rest := trimmed
	for {
		open := strings.IndexByte(rest, '{')
		if open < 0 {
			if strings.IndexByte(rest, '}') >= 0 {
				return fmt.Errorf("unmatched '}' in template")
			}
			return nil
		}
		if strings.IndexByte(rest[:open], '}') >= 0 {
			return fmt.Errorf("unmatched '}' in template")
		}
		end := strings.IndexByte(rest[open:], '}')
		if end < 0 {
			return fmt.Errorf("unmatched '{' in template")
		}
		expr := rest[open+1 : open+end]
		name, pad, hasPad := strings.Cut(expr, ":")
		name = strings.ToLower(strings.TrimSpace(name))
		if _, ok := strs[name]; ok {
			if hasPad {
				return fmt.Errorf("{%s} does not take a padding suffix", canonicalToken(name))
			}
		} else if _, ok := nums[name]; ok {
			if hasPad {
				if _, err := parsePad(pad); err != nil {
					return fmt.Errorf("bad padding %q in {%s}: %w", pad, name, err)
				}
			}
		} else {
			return fmt.Errorf("unknown token {%s} — available: %s", expr, strings.Join(KnownTokens(), " "))
		}
		rest = rest[open+end+1:]
	}
}

// parsePad parses a ":0N" padding suffix (the leading colon already stripped).
func parsePad(pad string) (int, error) {
	pad = strings.TrimSpace(pad)
	if !strings.HasPrefix(pad, "0") {
		return 0, fmt.Errorf(`padding must look like "02"`)
	}
	n, err := strconv.Atoi(pad)
	if err != nil || n < 1 || n > 6 {
		return 0, fmt.Errorf(`padding must be "01".."06"`)
	}
	return n, nil
}

// expand turns a template into a relative path: the TEMPLATE is split on both
// separators first, then each component is substituted and sanitized, and empty
// components (from a token that resolved to nothing) are dropped. When nothing
// survives, fallback is used as the single component.
//
// Splitting before substitution is what keeps a title like
// "Рик и Морти / Rick and Morty" one folder instead of two: only separators the
// user typed in the template create directories, never separators in the data.
//
// Unknown tokens are left verbatim rather than erroring — callers validate the
// template up front with ValidateTemplate, so this stays a pure string
// operation that can never fail mid-download.
func expand(tmpl string, v tokenValues, fallback string) string {
	parts := splitPath(tmpl)
	clean := make([]string, 0, len(parts))
	for _, p := range parts {
		// A component that sanitizes down to nothing (e.g. "..", or a token that
		// resolved to an empty string) is dropped rather than replaced, so it can
		// neither escape the output root nor leave a blank directory.
		if s := sanitizeOrEmpty(substitute(p, v)); s != "" {
			clean = append(clean, s)
		}
	}
	if len(clean) == 0 {
		return fsutil.SanitizeComponent(fallback, "unnamed")
	}
	return filepath.Join(clean...)
}

// emptyMarker is a fallback fsutil.SanitizeComponent can return verbatim but
// that no real name can sanitize to, which is how sanitizeOrEmpty tells
// "sanitized to nothing" apart from a legitimate result.
const emptyMarker = "\x00"

// sanitizeOrEmpty sanitizes one path component, returning "" when nothing
// usable survives. fsutil.SanitizeComponent cannot express that directly: an
// empty fallback is replaced with "unnamed".
func sanitizeOrEmpty(s string) string {
	if out := fsutil.SanitizeComponent(s, emptyMarker); out != emptyMarker {
		return out
	}
	return ""
}

// substitute replaces the tokens in one path component.
func substitute(tmpl string, v tokenValues) string {
	strs, nums := v.stringTokens(), v.numberTokens()

	var b strings.Builder
	rest := tmpl
	for {
		open := strings.IndexByte(rest, '{')
		if open < 0 {
			b.WriteString(rest)
			break
		}
		end := strings.IndexByte(rest[open:], '}')
		if end < 0 {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:open])
		expr := rest[open+1 : open+end]
		name, pad, hasPad := strings.Cut(expr, ":")
		name = strings.ToLower(strings.TrimSpace(name))
		switch {
		case !hasPad && strs[name] != "":
			b.WriteString(strs[name])
		case !hasPad && hasKey(strs, name):
			// Known token with an empty value: substitute nothing.
		case hasKey(nums, name):
			width := 0
			if hasPad {
				width, _ = parsePad(pad)
			}
			b.WriteString(fmt.Sprintf("%0*d", width, nums[name]))
		default:
			// Unknown token: keep it visible instead of silently dropping it.
			b.WriteString(rest[open : open+end+1])
		}
		rest = rest[open+end+1:]
	}
	return b.String()
}

// splitPath splits a template result on both path separators so a template
// written with "\" on Windows behaves the same as one written with "/".
func splitPath(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == '\\' })
}

func hasKey[V any](m map[string]V, k string) bool {
	_, ok := m[k]
	return ok
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// valuesFor collects the token values for one episode of one series.
func valuesFor(series domain.Series, ep domain.Episode) tokenValues {
	ru, original := series.RussianTitle, series.OriginalTitle
	return tokenValues{
		title:    series.Title,
		ru:       ru,
		original: original,
		year:     series.Year,
		id:       string(series.ID),
		season:   ep.Key.Season,
		episode:  ep.Key.Episode,
		epTitle:  ep.Title,
		quality:  ep.Quality,
	}
}
