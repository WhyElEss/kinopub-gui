package kinopub

import (
	"context"
	"testing"
	"time"

	"github.com/ZioSHik/kinopub-gui/internal/domain"
)

// A per-episode retry narrows the run to the episode the user clicked. A live
// retry for a DIFFERENT failed episode must still be accepted into that run —
// otherwise a card with several broken episodes can only be repaired one
// episode per run, no matter what the concurrency setting says.
func TestRunHLS_LiveRetryOutsideNarrowedScope(t *testing.T) {
	k1 := domain.EpisodeKey{Series: "42", Season: 1, Episode: 1}
	k2 := domain.EpisodeKey{Series: "42", Season: 1, Episode: 2}
	g := newGatedHLS(k1, k2)

	e, _, _ := newRetryTestEngine(g, &fakePageScraper{playlist: makePlaylist(3)})
	retry := make(chan domain.EpisodeKey, 4)
	e.deps.RetryRequests = retry

	cfg := retryTestConfig()
	cfg.MaxConcurrency = 2
	// This run starts with E01 only — as a per-episode retry does.
	cfg.RetryOnly = []domain.EpisodeKey{{Season: 1, Episode: 1}}

	done := make(chan domain.RunResult, 1)
	go func() {
		res, _ := e.runHLS(context.Background(), cfg)
		done <- res
	}()

	select {
	case <-g.started[k1]:
	case <-time.After(2 * time.Second):
		t.Fatal("the narrowed episode never started")
	}

	// The user hits Retry on a second failed episode while the first is running.
	retry <- k2
	select {
	case <-g.started[k2]:
	case <-time.After(2 * time.Second):
		t.Fatal("a live retry outside the narrowed scope was dropped")
	}

	close(g.release[k1])
	close(g.release[k2])

	select {
	case res := <-done:
		if res.Succeeded != 2 {
			t.Errorf("Succeeded = %d, want 2 (both the narrowed and the retried episode)", res.Succeeded)
		}
		if res.Total < 2 {
			t.Errorf("Total = %d, want at least 2 — the tally must not undercount a live retry", res.Total)
		}
		if res.Failed != 0 {
			t.Errorf("Failed = %d, want 0", res.Failed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("run did not finish")
	}
}

// The narrowing itself must still hold: an episode that was neither selected
// nor retried must not be downloaded by a scoped run.
func TestRunHLS_NarrowedScopeStartsWithOneEpisode(t *testing.T) {
	k1 := domain.EpisodeKey{Series: "42", Season: 1, Episode: 1}
	g := newGatedHLS(k1)
	close(g.release[k1])

	e, _, _ := newRetryTestEngine(g, &fakePageScraper{playlist: makePlaylist(3)})
	cfg := retryTestConfig()
	cfg.RetryOnly = []domain.EpisodeKey{{Season: 1, Episode: 1}}

	res, err := e.runHLS(context.Background(), cfg)
	if err != nil {
		t.Fatalf("runHLS error: %v", err)
	}
	if res.Total != 1 || res.Succeeded != 1 {
		t.Errorf("result = %+v, want exactly one episode downloaded", res)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for k, n := range g.calls {
		if k != k1 && n > 0 {
			t.Errorf("episode %v was downloaded %d times by a run scoped to S1E1", k, n)
		}
	}
}

// preCompletedStore reports a fixed set of episodes as already downloaded.
type preCompletedStore struct {
	mockStateStore
	done map[string]bool
}

func (s *preCompletedStore) IsCompleted(_ domain.DownloadState, key domain.EpisodeKey) bool {
	return s.done[episodeKeyStr(key)]
}

// planCapture records the plan the engine announces at the start of a run.
type planCapture struct {
	recordingReporter
	plan domain.SeriesPlan
}

func (p *planCapture) Start(plan domain.SeriesPlan) { p.plan = plan }

// The plan must name the episodes this run skips because they are already on
// disk — that is what lets the GUI keep their rows green instead of leaving a
// stale "failed" on an episode that is in fact downloaded.
func TestRunHLS_PlanNamesAlreadyDownloadedEpisodes(t *testing.T) {
	k3 := domain.EpisodeKey{Series: "42", Season: 1, Episode: 3}
	g := newGatedHLS(k3)
	close(g.release[k3])

	e, _, _ := newRetryTestEngine(g, &fakePageScraper{playlist: makePlaylist(3)})
	cap := &planCapture{}
	e.deps.ProgressReporter = cap
	e.deps.StateStore = &preCompletedStore{done: map[string]bool{"S1E1": true, "S1E2": true}}

	res, err := e.runHLS(context.Background(), retryTestConfig())
	if err != nil {
		t.Fatalf("runHLS error: %v", err)
	}
	if res.Succeeded != 1 {
		t.Errorf("Succeeded = %d, want 1 (only the missing episode)", res.Succeeded)
	}
	if got := len(cap.plan.Completed); got != 2 {
		t.Fatalf("plan.Completed has %d episodes, want 2", got)
	}
	if cap.plan.AlreadyCompleted != 2 {
		t.Errorf("AlreadyCompleted = %d, want 2", cap.plan.AlreadyCompleted)
	}
	for _, ce := range cap.plan.Completed {
		if ce.Key.Episode == 3 {
			t.Error("the episode this run downloads must not be reported as already completed")
		}
	}
}

// A run scoped to one episode must not claim its untouched siblings are done.
func TestRunHLS_NarrowedRunDoesNotClaimSiblingsCompleted(t *testing.T) {
	k1 := domain.EpisodeKey{Series: "42", Season: 1, Episode: 1}
	g := newGatedHLS(k1)
	close(g.release[k1])

	e, _, _ := newRetryTestEngine(g, &fakePageScraper{playlist: makePlaylist(3)})
	cap := &planCapture{}
	e.deps.ProgressReporter = cap

	cfg := retryTestConfig()
	cfg.RetryOnly = []domain.EpisodeKey{{Season: 1, Episode: 1}}
	if _, err := e.runHLS(context.Background(), cfg); err != nil {
		t.Fatalf("runHLS error: %v", err)
	}
	if len(cap.plan.Completed) != 0 {
		t.Errorf("plan.Completed = %+v, want empty — nothing was downloaded before", cap.plan.Completed)
	}
	if cap.plan.AlreadyCompleted != 0 {
		t.Errorf("AlreadyCompleted = %d, want 0", cap.plan.AlreadyCompleted)
	}
}

// ForceRedownload re-downloads completed episodes, so none of them may be
// reported as already done.
func TestRunHLS_ForceRedownloadReportsNothingCompleted(t *testing.T) {
	e, _, _ := newRetryTestEngine(newFakeHLS(nil), &fakePageScraper{playlist: makePlaylist(2)})
	cap := &planCapture{}
	e.deps.ProgressReporter = cap
	e.deps.StateStore = &preCompletedStore{done: map[string]bool{"S1E1": true, "S1E2": true}}

	cfg := retryTestConfig()
	cfg.ForceRedownload = true
	if _, err := e.runHLS(context.Background(), cfg); err != nil {
		t.Fatalf("runHLS error: %v", err)
	}
	if len(cap.plan.Completed) != 0 {
		t.Errorf("plan.Completed = %+v, want empty under ForceRedownload", cap.plan.Completed)
	}
	if len(cap.plan.Planned) != 2 {
		t.Errorf("plan.Planned has %d episodes, want both re-downloaded", len(cap.plan.Planned))
	}
}
