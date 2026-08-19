package gui

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ZioSHik/kinopub-gui/internal/domain"
	"github.com/ZioSHik/kinopub-gui/internal/services/kinopubapi"
)

// airingItem is a series whose season 1 has three episodes, the third of which
// kino.pub announces but has not encoded yet, plus a second season.
func airingItem() kinopubapi.Item {
	withFile := []kinopubapi.File{{Quality: "1080p"}}
	return kinopubapi.Item{
		ID:    "121819",
		Title: "Истории далёкого пригорода",
		Seasons: []kinopubapi.Season{
			{Number: 1, Episodes: []kinopubapi.Episode{
				{Number: 1, Title: "Водяной буйвол", Files: withFile},
				{Number: 2, Title: "Сломанные игрушки", Files: withFile},
				{Number: 3, Title: "Дождь вдали"}, // announced, no files yet
			}},
			{Number: 2, Episodes: []kinopubapi.Episode{
				{Number: 1, Title: "Возвращение", Files: withFile},
			}},
		},
	}
}

func keys(ks []domain.EpisodeKey) []string {
	out := make([]string, 0, len(ks))
	for _, k := range ks {
		out = append(out, epKey(k))
	}
	return out
}

func TestMissingEpisodes_SkipsDownloadedAndUnencoded(t *testing.T) {
	w := Watch{ID: "121819"}
	have := map[string]bool{"S1E1": true}

	missing, titles, available := missingEpisodes(airingItem(), w, have, nil)

	if got := keys(missing); len(got) != 2 || got[0] != "S1E2" || got[1] != "S2E1" {
		t.Errorf("missing = %v, want [S1E2 S2E1]", got)
	}
	if available != 3 {
		t.Errorf("available = %d, want 3 (the episode with no files is not offered)", available)
	}
	if titles["S1E2"] != "Сломанные игрушки" {
		t.Errorf("titles = %v, want the episode names carried through", titles)
	}
}

func TestMissingEpisodes_SkipsEpisodesAlreadyOnACard(t *testing.T) {
	missing, _, _ := missingEpisodes(airingItem(), Watch{ID: "121819"}, nil, map[string]bool{"S1E2": true})
	for _, k := range keys(missing) {
		if k == "S1E2" {
			t.Error("an episode a card already covers must not be queued a second time")
		}
	}
}

func TestMissingEpisodes_SeasonScope(t *testing.T) {
	missing, _, available := missingEpisodes(airingItem(), Watch{ID: "121819", Seasons: []int{1}}, nil, nil)
	if got := keys(missing); len(got) != 2 || got[0] != "S1E1" || got[1] != "S1E2" {
		t.Errorf("missing = %v, want season 1 only", got)
	}
	if available != 2 {
		t.Errorf("available = %d, want 2 — season 2 is outside the scope", available)
	}
}

func TestMissingEpisodes_NothingNew(t *testing.T) {
	have := map[string]bool{"S1E1": true, "S1E2": true, "S2E1": true}
	missing, _, _ := missingEpisodes(airingItem(), Watch{ID: "121819"}, have, nil)
	if len(missing) != 0 {
		t.Errorf("missing = %v, want none", keys(missing))
	}
}

func TestWatchFromRequest(t *testing.T) {
	cfg := domain.RunConfig{
		InputURL:         "https://kino.pub/item/view/121819",
		Quality:          "1080p",
		SelectedEpisodes: []domain.EpisodeKey{{Season: 1, Episode: 1}},
		ForceRedownload:  true,
	}
	w, err := watchFromRequest(cfg, []int{2, 1, 2, 0}, "Show", "poster.jpg")
	if err != nil {
		t.Fatalf("watchFromRequest: %v", err)
	}
	if w.ID != "121819" {
		t.Errorf("ID = %q, want the item id from the URL", w.ID)
	}
	if len(w.Seasons) != 2 || w.Seasons[0] != 1 || w.Seasons[1] != 2 {
		t.Errorf("Seasons = %v, want [1 2] deduplicated and sorted", w.Seasons)
	}
	// The frozen episode list is exactly what must NOT survive: each check picks
	// the episodes from what the API offers then.
	if len(w.Cfg.SelectedEpisodes) != 0 || w.Cfg.ForceRedownload {
		t.Errorf("cfg = %+v, want the episode selection dropped", w.Cfg)
	}
	if w.Cfg.Quality != "1080p" {
		t.Error("the download settings must be kept")
	}
}

func TestWatchFromRequest_RejectsNonItemURL(t *testing.T) {
	if _, err := watchFromRequest(domain.RunConfig{InputURL: "https://example.com/"}, nil, "", ""); err == nil {
		t.Error("want an error for a URL with no kino.pub item id")
	}
}

func TestWatchFollowsSeason(t *testing.T) {
	all := Watch{}
	if !all.followsSeason(7) {
		t.Error("an empty season list follows every season")
	}
	one := Watch{Seasons: []int{3}}
	if !one.followsSeason(3) || one.followsSeason(4) {
		t.Error("a season list follows exactly its seasons")
	}
}

func TestWatchListRoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	wl := newWatchList()
	wl.upsert(Watch{ID: "1", URL: "u1", Title: "One", CreatedAt: time.Unix(100, 0)})
	wl.upsert(Watch{ID: "2", URL: "u2", Title: "Two", CreatedAt: time.Unix(200, 0)})

	// Newest first.
	if got := wl.list(); len(got) != 2 || got[0].ID != "2" {
		t.Fatalf("list = %+v, want the newest watch first", got)
	}

	// A re-follow replaces the settings but keeps what earlier checks found.
	checkedAt := time.Unix(500, 0)
	wl.update("1", func(w *Watch) { w.LastCheck = &checkedAt; w.Available = 9 })
	wl.upsert(Watch{ID: "1", URL: "u1", Title: "One again", Cfg: domain.RunConfig{Quality: "720p"}})
	got, ok := wl.get("1")
	if !ok || got.Title != "One again" || got.Cfg.Quality != "720p" {
		t.Fatalf("re-follow did not update the settings: %+v", got)
	}
	if got.Available != 9 || got.LastCheck == nil {
		t.Errorf("re-follow lost the check history: %+v", got)
	}

	// Survives a restart.
	reloaded := newWatchList()
	if len(reloaded.list()) != 2 {
		t.Errorf("watchlist did not survive a reload: %+v", reloaded.list())
	}

	if !wl.remove("1") || wl.remove("nope") {
		t.Error("remove should report whether it removed anything")
	}
	if len(newWatchList().list()) != 1 {
		t.Error("the removal was not persisted")
	}
}

func TestWatchIntervalClamped(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s := &Server{settings: newSettingsStore()}
	cases := map[int]time.Duration{
		0:     defaultWatchIntervalMinutes * time.Minute,
		1:     minWatchIntervalMinutes * time.Minute,
		60:    time.Hour,
		99999: maxWatchIntervalMinutes * time.Minute,
	}
	for in, want := range cases {
		cur := s.settings.get()
		cur.WatchIntervalMinutes = in
		if _, err := s.settings.save(cur); err != nil {
			t.Fatalf("save: %v", err)
		}
		if got := s.watchInterval(); got != want {
			t.Errorf("watchInterval(%d) = %v, want %v", in, got, want)
		}
	}
}

// The watch JSON is what a restart reads back, so its shape is part of the
// contract with the UI.
func TestWatchJSONShape(t *testing.T) {
	data, err := json.Marshal(Watch{ID: "1", URL: "u", Title: "T"})
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"id", "url", "title", "cfg", "createdAt"} {
		if _, ok := back[field]; !ok {
			t.Errorf("missing field %q in %s", field, data)
		}
	}
	// A watch nothing has checked yet must not carry a zero timestamp: the UI
	// would render it as a check that happened two millennia ago.
	for _, field := range []string{"lastCheck", "lastFoundAt"} {
		if _, ok := back[field]; ok {
			t.Errorf("field %q should be absent until a check runs: %s", field, data)
		}
	}
}
