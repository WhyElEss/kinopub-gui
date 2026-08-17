package outputlayout

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZioSHik/kinopub-gui/internal/domain"
)

func testSeries() domain.Series {
	return domain.Series{
		ID:            "12345",
		Title:         "Рик и Морти / Rick and Morty",
		RussianTitle:  "Рик и Морти",
		OriginalTitle: "Rick and Morty",
		Year:          2013,
	}
}

func testEpisode(season, episode int) domain.Episode {
	return domain.Episode{
		Key:     domain.EpisodeKey{Series: "12345", Season: season, Episode: episode},
		Title:   "Пиклик",
		Quality: "1080p",
	}
}

func TestEpisodePathDefaultsMatchLegacyLayout(t *testing.T) {
	l := New(domain.ContainerMKV)
	got, err := l.EpisodePath("/out", testSeries(), testEpisode(3, 7))
	if err != nil {
		t.Fatalf("EpisodePath: %v", err)
	}
	want := filepath.Join("/out", "Рик и Морти _ Rick and Morty", "Season 03", "S03E07.mkv")
	if got != want {
		t.Errorf("EpisodePath = %q, want %q", got, want)
	}
}

func TestEpisodePathTemplates(t *testing.T) {
	cases := []struct {
		name     string
		dir      string
		file     string
		season   int
		episode  int
		want     string
		mp4      bool
	}{
		{
			name: "plex movie",
			dir:  DefaultMovieDirTemplate, file: DefaultMovieNameTemplate,
			season: 1, episode: 1,
			want: filepath.Join("/out", "Рик и Морти (2013)", "Рик и Морти (2013).mkv"),
		},
		{
			name: "plex series",
			dir:  "{ru} ({year})", file: "Season {season:02}/{ru} - S{season:02}E{episode:02} - {epTitle}",
			season: 3, episode: 7,
			want: filepath.Join("/out", "Рик и Морти (2013)", "Season 03", "Рик и Морти - S03E07 - Пиклик.mkv"),
		},
		{
			name: "original title and quality, mp4",
			dir:  "{original}", file: "{original} {quality}",
			season: 1, episode: 2, mp4: true,
			want: filepath.Join("/out", "Rick and Morty", "Rick and Morty 1080p.mp4"),
		},
		{
			name: "unpadded numbers",
			dir:  "{title}", file: "{season}x{episode}",
			season: 12, episode: 3,
			want: filepath.Join("/out", "Рик и Морти _ Rick and Morty", "12x3.mkv"),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			container := domain.ContainerMKV
			if c.mp4 {
				container = domain.ContainerMP4
			}
			l := NewWithTemplates(container, c.dir, c.file)
			got, err := l.EpisodePath("/out", testSeries(), testEpisode(c.season, c.episode))
			if err != nil {
				t.Fatalf("EpisodePath: %v", err)
			}
			if got != c.want {
				t.Errorf("EpisodePath = %q, want %q", got, c.want)
			}
		})
	}
}

// A template must never be able to write outside the output root, whatever the
// user (or a title) puts in it.
func TestEpisodePathStaysInsideRoot(t *testing.T) {
	series := testSeries()
	series.Title = "../../etc"
	series.RussianTitle = "../../etc"
	cases := [][2]string{
		{"../../{ru}", "../../{ru}"},
		{"{ru}", "../../../etc/passwd"},
		{"/absolute/{ru}", "{ru}"},
	}
	for _, c := range cases {
		l := NewWithTemplates(domain.ContainerMKV, c[0], c[1])
		got, err := l.EpisodePath("/out", series, testEpisode(1, 1))
		if err != nil {
			t.Fatalf("EpisodePath: %v", err)
		}
		if !strings.HasPrefix(filepath.Clean(got), "/out/") {
			t.Errorf("templates %q/%q escaped the root: %q", c[0], c[1], got)
		}
	}
}

// A template whose tokens all resolve to nothing must still produce a usable
// name rather than an empty path component.
func TestEpisodePathFallsBackWhenEmpty(t *testing.T) {
	l := NewWithTemplates(domain.ContainerMKV, "{ru}", "{epTitle}")
	got, err := l.EpisodePath("/out", domain.Series{ID: "42"}, domain.Episode{
		Key: domain.EpisodeKey{Season: 2, Episode: 5},
	})
	if err != nil {
		t.Fatalf("EpisodePath: %v", err)
	}
	want := filepath.Join("/out", "series_42", "S02E05.mkv")
	if got != want {
		t.Errorf("EpisodePath = %q, want %q", got, want)
	}
}

func TestSeriesDirIsAlwaysBelowRoot(t *testing.T) {
	// Even a file template with no folder in it keeps a per-series directory, so
	// two titles downloaded into the same root cannot share a state file.
	l := NewWithTemplates(domain.ContainerMKV, "{ru} ({year})", "{ru}")
	dir := l.SeriesDir("/out", testSeries())
	if dir != filepath.Join("/out", "Рик и Морти (2013)") {
		t.Errorf("SeriesDir = %q", dir)
	}
	path, _ := l.EpisodePath("/out", testSeries(), testEpisode(1, 1))
	if filepath.Dir(path) != dir {
		t.Errorf("episode %q is not inside series dir %q", path, dir)
	}
}

func TestValidateTemplate(t *testing.T) {
	ok := []string{
		"{title}",
		"Season {season:02}/S{season:02}E{episode:02}",
		"{ru} ({year})",
		"literal only",
		"{original} - {quality} - {epTitle} - {id}",
		"{season}x{episode}",
	}
	for _, tmpl := range ok {
		if err := ValidateTemplate(tmpl); err != nil {
			t.Errorf("ValidateTemplate(%q) = %v, want nil", tmpl, err)
		}
	}

	bad := []string{
		"",
		"   ",
		"/absolute",
		"{titel}",          // typo
		"{title",           // unmatched brace
		"title}",           // unmatched brace
		"{title:02}",       // padding on a string token
		"{season:2}",       // padding must be zero-prefixed
		"{season:099}",     // out of range
	}
	for _, tmpl := range bad {
		if err := ValidateTemplate(tmpl); err == nil {
			t.Errorf("ValidateTemplate(%q) = nil, want an error", tmpl)
		}
	}
}

func TestKnownTokensCoversEveryToken(t *testing.T) {
	for _, tok := range KnownTokens() {
		if err := ValidateTemplate("x" + tok); err != nil {
			t.Errorf("advertised token %s does not validate: %v", tok, err)
		}
	}
}
