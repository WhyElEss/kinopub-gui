package gui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/ZioSHik/kinopub-gui/internal/domain"
)

// Watch is a series the app follows. An airing series gets new episodes days or
// weeks after it was first queued, and the download that queued it captured the
// episode list as it stood that day — so following one means re-asking kino.pub
// what exists now and downloading whatever is missing, with the settings the
// user picked when they started following.
type Watch struct {
	ID        string `json:"id"` // kino.pub item id — one watch per title
	URL       string `json:"url"`
	Title     string `json:"title,omitempty"`
	PosterURL string `json:"posterUrl,omitempty"`
	// Seasons narrows what is followed. Empty means every season, which is what
	// a still-airing show needs once it rolls over into the next one.
	Seasons []int `json:"seasons,omitempty"`
	// Cfg carries the download settings captured when the series was followed
	// (quality, container, output templates, voiceover). Its episode selection is
	// not used: each check picks the episodes itself.
	Cfg domain.RunConfig `json:"cfg"`
	// Paused stops the checks without forgetting the settings.
	Paused    bool      `json:"paused,omitempty"`
	CreatedAt time.Time `json:"createdAt"`

	// Result of the last check, for the UI. The timestamps are pointers because a
	// zero time.Time still marshals (omitempty does not skip a struct), and the UI
	// would render "checked 2025 years ago" for a watch nothing has checked yet.
	LastCheck   *time.Time `json:"lastCheck,omitempty"`
	LastError   string     `json:"lastError,omitempty"`
	LastFoundAt *time.Time `json:"lastFoundAt,omitempty"`
	LastQueued  []string   `json:"lastQueued,omitempty"` // episode keys queued by the last check that found any
	Available   int        `json:"available,omitempty"`  // episodes kino.pub offered at the last check
	Downloaded  int        `json:"downloaded,omitempty"` // of those, how many are already on disk
}

// watchList is the persisted set of followed series. It is small (one entry per
// followed title) and read on every check, so it is kept in memory and written
// out whole on each change, like the job queue.
type watchList struct {
	mu    sync.Mutex
	path  string
	items []Watch
}

func newWatchList() *watchList {
	w := &watchList{}
	if dir, err := configDir(); err == nil {
		w.path = filepath.Join(dir, "watchlist.json")
	}
	w.items = w.load()
	return w
}

func (w *watchList) load() []Watch {
	if w.path == "" {
		return nil
	}
	data, err := os.ReadFile(w.path)
	if err != nil {
		return nil
	}
	var items []Watch
	if err := json.Unmarshal(data, &items); err != nil {
		return nil
	}
	return items
}

// saveLocked atomically replaces the file (write temp + rename) so a crash
// mid-write cannot corrupt the previous good list. Caller must hold w.mu.
func (w *watchList) saveLocked() error {
	if w.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(w.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(w.items, "", "  ")
	if err != nil {
		return err
	}
	tmp := w.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, w.path)
}

// list returns a copy of the watches, newest first.
func (w *watchList) list() []Watch {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]Watch, len(w.items))
	copy(out, w.items)
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (w *watchList) get(id string) (Watch, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, it := range w.items {
		if it.ID == id {
			return it, true
		}
	}
	return Watch{}, false
}

// upsert adds a watch or replaces the settings of an existing one for the same
// title, keeping the record of earlier checks.
func (w *watchList) upsert(nw Watch) Watch {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i, it := range w.items {
		if it.ID == nw.ID {
			nw.CreatedAt = it.CreatedAt
			nw.LastCheck = it.LastCheck
			nw.LastError = it.LastError
			nw.LastFoundAt = it.LastFoundAt
			nw.LastQueued = it.LastQueued
			nw.Available = it.Available
			nw.Downloaded = it.Downloaded
			w.items[i] = nw
			_ = w.saveLocked()
			return nw
		}
	}
	if nw.CreatedAt.IsZero() {
		nw.CreatedAt = time.Now()
	}
	w.items = append(w.items, nw)
	_ = w.saveLocked()
	return nw
}

// update applies fn to the stored watch and persists the result.
func (w *watchList) update(id string, fn func(*Watch)) (Watch, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i := range w.items {
		if w.items[i].ID == id {
			fn(&w.items[i])
			_ = w.saveLocked()
			return w.items[i], true
		}
	}
	return Watch{}, false
}

func (w *watchList) remove(id string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i, it := range w.items {
		if it.ID == id {
			w.items = append(w.items[:i], w.items[i+1:]...)
			_ = w.saveLocked()
			return true
		}
	}
	return false
}

// followsSeason reports whether this watch covers the given season number.
func (w Watch) followsSeason(n int) bool {
	if len(w.Seasons) == 0 {
		return true
	}
	for _, s := range w.Seasons {
		if s == n {
			return true
		}
	}
	return false
}
