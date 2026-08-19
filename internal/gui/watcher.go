package gui

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"time"

	"github.com/ZioSHik/kinopub-gui/internal/domain"
	"github.com/ZioSHik/kinopub-gui/internal/services/kinopubapi"
)

// How often followed series are re-checked, and the bounds the setting is
// clamped to. Episodes appear on a schedule measured in days, so checking every
// few hours is plenty; anything faster only adds load to an API that has no
// business hearing from us that often.
const (
	defaultWatchIntervalMinutes = 180
	minWatchIntervalMinutes     = 15
	maxWatchIntervalMinutes     = 24 * 60
	// Delay before the first sweep after startup: long enough for a restored
	// queue to settle and for a sign-in to be restored from the credential store.
	watchStartupDelay = 2 * time.Minute
	// Pause between titles within one sweep, so following a dozen series doesn't
	// arrive at kino.pub as a dozen simultaneous requests.
	watchSpacing = 2 * time.Second
)

// StartWatcher launches the background loop that checks followed series for new
// episodes. Called by main; tests construct a Server without it so nothing polls
// in the background.
func (s *Server) StartWatcher() {
	go s.watchLoop(context.Background())
}

func (s *Server) watchLoop(ctx context.Context) {
	timer := time.NewTimer(watchStartupDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		s.checkAllWatches(ctx)
		timer.Reset(s.watchInterval())
	}
}

// watchInterval is the configured check interval, clamped to something sane.
func (s *Server) watchInterval() time.Duration {
	m := s.settings.get().WatchIntervalMinutes
	if m <= 0 {
		m = defaultWatchIntervalMinutes
	}
	if m < minWatchIntervalMinutes {
		m = minWatchIntervalMinutes
	}
	if m > maxWatchIntervalMinutes {
		m = maxWatchIntervalMinutes
	}
	return time.Duration(m) * time.Minute
}

// checkAllWatches checks every active watch in turn, spaced out in time.
func (s *Server) checkAllWatches(ctx context.Context) {
	for i, w := range s.watches.list() {
		if w.Paused {
			continue
		}
		if i > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(watchSpacing):
			}
		}
		s.checkWatch(ctx, w.ID)
	}
}

// checkWatch asks kino.pub what episodes the followed title has now, and queues
// a download for the ones that are neither on disk nor already being downloaded.
// The check's outcome is recorded on the watch either way, so the UI can show
// when it last ran and what it found.
func (s *Server) checkWatch(ctx context.Context, id string) ([]string, error) {
	w, ok := s.watches.get(id)
	if !ok {
		return nil, fmt.Errorf("not following this title")
	}

	queued, available, downloaded, err := s.findNewEpisodes(ctx, w)
	now := time.Now()
	s.watches.update(id, func(cur *Watch) {
		cur.LastCheck = &now
		cur.Available = available
		cur.Downloaded = downloaded
		if err != nil {
			cur.LastError = err.Error()
			return
		}
		cur.LastError = ""
		if len(queued) > 0 {
			cur.LastFoundAt = &now
			cur.LastQueued = queued
		}
	})
	s.publishWatches()
	return queued, err
}

// findNewEpisodes does the work of one check and, when something is missing,
// starts the download. It returns the episode keys it queued plus what it saw
// (episodes offered / already downloaded) for the UI.
func (s *Server) findNewEpisodes(ctx context.Context, w Watch) (queued []string, available, downloaded int, err error) {
	client, cerr := s.kpClient()
	if client == nil {
		if cerr == nil {
			cerr = fmt.Errorf("not signed in to kino.pub")
		}
		return nil, 0, 0, cerr
	}
	item, ierr := client.Item(ctx, w.ID)
	if ierr != nil {
		return nil, 0, 0, ierr
	}

	have := make(map[string]bool)
	for _, d := range downloadedForItem(s.libraryDirs(), w.ID).Episodes {
		have[d.Key] = true
	}
	downloaded = len(have)
	missing, titles, available := missingEpisodes(item, w, have, s.episodesOnCards(w.ID))
	for _, k := range missing {
		queued = append(queued, epKey(k))
	}
	if len(missing) == 0 {
		return nil, available, downloaded, nil
	}

	cfg := w.Cfg
	cfg.SelectedEpisodes = missing
	cfg.RetryOnly = nil
	if cfg.InputURL == "" {
		cfg.InputURL = w.URL
	}
	if !cfg.DryRun {
		if _, lookErr := exec.LookPath(cfg.FFmpegPath); lookErr != nil {
			return nil, available, downloaded, fmt.Errorf("ffmpeg not found on PATH — install ffmpeg to download")
		}
	}
	title := w.Title
	if title == "" {
		title = item.Title
	}
	poster := w.PosterURL
	if poster == "" {
		poster = item.Posters.Best()
	}
	// followDefaults: a download nobody asked for at this moment should run with
	// the concurrency/retry settings that are current when it starts, not the
	// ones that happened to be set the day the series was followed.
	s.launchJob(cfg, title, poster, titles, false, true)
	return queued, available, downloaded, nil
}

// missingEpisodes picks the episodes of a followed title that should be
// downloaded now: everything kino.pub offers within the watch's seasons, minus
// what is already on disk (have) and what a card in the queue already covers
// (onCards). It also returns the episode titles, for the job's
// rows, and how many episodes were offered.
func missingEpisodes(item kinopubapi.Item, w Watch, have, onCards map[string]bool) (missing []domain.EpisodeKey, titles map[string]string, available int) {
	titles = make(map[string]string)
	for _, season := range item.Seasons {
		if !w.followsSeason(season.Number) {
			continue
		}
		for _, e := range season.Episodes {
			// An episode with no files is announced but not yet playable —
			// queueing it would start a download that cannot run.
			if len(e.Files) == 0 {
				continue
			}
			available++
			k := domain.EpisodeKey{Season: season.Number, Episode: e.Number}
			ks := epKey(k)
			if e.Title != "" {
				titles[ks] = e.Title
			}
			if have[ks] || onCards[ks] {
				continue
			}
			missing = append(missing, k)
		}
	}
	sort.Slice(missing, func(i, j int) bool {
		if missing[i].Season != missing[j].Season {
			return missing[i].Season < missing[j].Season
		}
		return missing[i].Episode < missing[j].Episode
	})
	return missing, titles, available
}

// episodesOnCards returns the episode keys any card for this kino.pub item
// already covers, whatever state that card is in. A queued or downloading card
// obviously must not have its episodes queued a second time by the next check.
// A finished one counts too: an episode that failed keeps its card, with its
// Retry — the watcher taking another run at it every few hours would bury the
// queue in identical cards for a title that is simply broken upstream. Clearing
// or removing the card hands the episode back to the watcher.
func (s *Server) episodesOnCards(itemID string) map[string]bool {
	return s.mgr.claimedEpisodes(func(url string) bool {
		return kinopubapi.ItemIDFromURL(url) == itemID
	})
}

// watchFromRequest builds a Watch from a download request, so following a series
// captures exactly the settings its download was started with.
func watchFromRequest(cfg domain.RunConfig, seasons []int, title, poster string) (Watch, error) {
	id := kinopubapi.ItemIDFromURL(cfg.InputURL)
	if id == "" {
		return Watch{}, fmt.Errorf("a kino.pub title URL is required")
	}
	// The episode selection is what goes stale — each check picks the episodes
	// itself, from what the API offers at that moment.
	cfg.SelectedEpisodes = nil
	cfg.RetryOnly = nil
	cfg.ForceRedownload = false
	seen := make(map[int]bool)
	var uniq []int
	for _, n := range seasons {
		if n > 0 && !seen[n] {
			seen[n] = true
			uniq = append(uniq, n)
		}
	}
	sort.Ints(uniq)
	return Watch{
		ID:        id,
		URL:       cfg.InputURL,
		Title:     title,
		PosterURL: poster,
		Seasons:   uniq,
		Cfg:       cfg,
		CreatedAt: time.Now(),
	}, nil
}

func (s *Server) publishWatches() {
	s.hub.broadcast(Event{Type: "watches", Data: s.watches.list()})
}
