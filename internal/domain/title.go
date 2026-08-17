package domain

import (
	"strconv"
	"strings"
)

// SplitTitle separates a kino.pub combined "Русское / Original" title into its
// two halves. When the title has no " / " separator the whole string is the
// Russian title and the original is empty.
//
// It lives in domain because both the API layer (building a Series) and the GUI
// (rendering catalog cards) need the exact same split, and the output-path
// templates expose the halves as {ru} / {original}.
func SplitTitle(s string) (title, original string) {
	if i := strings.Index(s, " / "); i > 0 {
		return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+3:])
	}
	return s, ""
}

// SeriesFromPlaylist creates the Series shell (identity and naming metadata, no
// seasons) for an extracted page playlist. The engine and the GUI preview both
// build a Series from the same playlist and must agree on every field the
// output-path templates read, or a preview would look for finished downloads in
// a different folder than the one the engine writes to.
func SeriesFromPlaylist(pl *PagePlaylist) Series {
	ru, original := SplitTitle(pl.Title)
	if pl.Subname != "" {
		original = pl.Subname
	}
	return Series{
		ID:            SeriesID(strconv.Itoa(pl.ItemID)),
		Title:         pl.Title,
		RussianTitle:  ru,
		OriginalTitle: original,
		Year:          pl.Year,
		PosterURL:     pl.Poster,
	}
}
