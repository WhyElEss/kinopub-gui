package gui

import (
	"context"
	"net/http"
	"time"
)

// WatchRequest starts following a series. It carries the same download settings
// a normal download does — following a series means "keep downloading it like
// this" — plus the seasons to follow (empty = every season).
type WatchRequest struct {
	RunRequest
	SeedTitle    string `json:"seedTitle"`
	SeedPoster   string `json:"seedPoster"`
	WatchSeasons []int  `json:"watchSeasons"`
}

func (s *Server) handleListWatches(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.watches.list())
}

// handleCreateWatch starts following a series (or updates the settings of one
// already followed) and checks it immediately, so anything already missing is
// queued right away instead of at the next sweep.
func (s *Server) handleCreateWatch(w http.ResponseWriter, r *http.Request) {
	var req WatchRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.WorkDir == "" {
		req.WorkDir = s.settings.get().WorkDir
	}
	cfg, err := buildRunConfig(req.RunRequest)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	watch, err := watchFromRequest(cfg, req.WatchSeasons, req.SeedTitle, req.SeedPoster)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	saved := s.watches.upsert(watch)
	s.publishWatches()

	// The first check runs here rather than at the next sweep, so following a
	// series that is already behind catches up immediately — and the answer says
	// what that check queued, which is the difference between "following, nothing
	// to do" and "following, four episodes are downloading now". A check that
	// fails is not a failure to follow: it is recorded on the watch and retried
	// on the next sweep.
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	s.checkWatch(ctx, saved.ID)
	if cur, ok := s.watches.get(saved.ID); ok {
		saved = cur
	}
	writeJSON(w, http.StatusAccepted, saved)
}

func (s *Server) handleDeleteWatch(w http.ResponseWriter, r *http.Request) {
	if !s.watches.remove(r.PathValue("id")) {
		writeErr(w, http.StatusNotFound, "not following this title")
		return
	}
	s.publishWatches()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handlePauseWatch suspends or resumes the checks for one title without
// forgetting the settings it was followed with.
func (s *Server) handlePauseWatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Paused bool `json:"paused"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, ok := s.watches.update(r.PathValue("id"), func(cur *Watch) { cur.Paused = body.Paused })
	if !ok {
		writeErr(w, http.StatusNotFound, "not following this title")
		return
	}
	s.publishWatches()
	writeJSON(w, http.StatusOK, updated)
}

// handleCheckWatch runs one title's check now and answers with what it queued,
// so the click has a visible result.
func (s *Server) handleCheckWatch(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	queued, err := s.checkWatch(ctx, r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"queued": queued})
}

// handleCheckAllWatches sweeps every followed title in the background.
func (s *Server) handleCheckAllWatches(w http.ResponseWriter, r *http.Request) {
	go s.checkAllWatches(context.Background())
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}
