package gui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ZioSHik/kinopub-gui/internal/domain"
)

func testPersistedJob(id, status string) persistedJob {
	return persistedJob{
		ID:        id,
		URL:       "https://kino.pub/item/view/409",
		Status:    status,
		Title:     "Fear and Loathing",
		Quality:   "1080p",
		CreatedAt: time.Date(2026, 7, 12, 3, 0, 0, 0, time.UTC),
		Episodes: []EpisodeView{
			{Key: "S1E1", Season: 1, Episode: 1, State: epRunning, Percent: 18, Bytes: 2_800_000_000, Total: 15_000_000_000, TotalApprox: true, SpeedBps: 5_000_000, ETASeconds: 2278},
			{Key: "S1E2", Season: 1, Episode: 2, State: epCompleted, Percent: 100},
		},
		Titles: map[string]string{"S1E1": "Серия 1"},
		Cfg: domain.RunConfig{
			InputURL:   "https://kino.pub/item/view/409",
			OutputPath: "/tmp/out",
			Quality:    "1080p",
			UseAPI:     true,
			AudioPref:  domain.AudioPreference{Specs: []domain.AudioSpec{{Require: []string{"дубляж"}}}},
		},
	}
}

func TestJobStoreRoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	store := newJobStore()

	in := []persistedJob{testPersistedJob("job-3", statusRunning)}
	if err := store.save(in); err != nil {
		t.Fatalf("save: %v", err)
	}
	out := store.load()
	if len(out) != 1 {
		t.Fatalf("load: got %d jobs, want 1", len(out))
	}
	got := out[0]
	if got.ID != "job-3" || got.URL != in[0].URL || got.Quality != "1080p" {
		t.Errorf("identity fields lost: %+v", got)
	}
	if len(got.Episodes) != 2 || got.Episodes[0].Total != 15_000_000_000 || !got.Episodes[0].TotalApprox {
		t.Errorf("episode progress lost: %+v", got.Episodes)
	}
	if got.Cfg.OutputPath != "/tmp/out" || !got.Cfg.UseAPI {
		t.Errorf("cfg lost: %+v", got.Cfg)
	}
	if len(got.Cfg.AudioPref.Specs) != 1 || got.Cfg.AudioPref.Specs[0].Require[0] != "дубляж" {
		t.Errorf("audio spec lost: %+v", got.Cfg.AudioPref)
	}
}

func TestJobStoreLoadMissingFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if jobs := newJobStore().load(); jobs != nil {
		t.Errorf("expected nil for missing file, got %v", jobs)
	}
}

func TestRestoreJobStatusMapping(t *testing.T) {
	cases := []struct {
		persisted string
		want      string
	}{
		{statusRunning, statusPaused},
		{statusResolving, statusPaused},
		{statusQueued, statusPaused},
		{statusPaused, statusPaused},
		{statusFailed, statusFailed},
		{statusCanceled, statusCanceled},
		{statusCompleted, statusCompleted},
	}
	for _, c := range cases {
		j := restoreJob(testPersistedJob("job-1", c.persisted))
		if j.status != c.want {
			t.Errorf("restore %q: got status %q, want %q", c.persisted, j.status, c.want)
		}
	}
}

func TestRestoreJobSettlesInterruptedEpisodes(t *testing.T) {
	j := restoreJob(testPersistedJob("job-1", statusRunning))

	ep := j.episodes["S1E1"]
	if ep == nil {
		t.Fatal("S1E1 not restored")
	}
	if ep.State != epPaused {
		t.Errorf("interrupted episode state = %q, want %q", ep.State, epPaused)
	}
	if ep.SpeedBps != 0 || ep.ETASeconds != 0 {
		t.Errorf("stale speed/ETA survived restore: %+v", ep)
	}
	if ep.Percent != 18 || ep.Bytes != 2_800_000_000 {
		t.Errorf("progress lost on restore: %+v", ep)
	}
	if done := j.episodes["S1E2"]; done == nil || done.State != epCompleted {
		t.Errorf("completed episode should stay completed: %+v", done)
	}
	if !j.paused.Load() {
		t.Error("restored paused job must have the paused flag set (so a resume run settles correctly)")
	}
	if j.titles["S1E1"] != "Серия 1" {
		t.Errorf("titles lost: %v", j.titles)
	}
}

func TestAttachStoreRestoresAndAdvancesSeq(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	store := newJobStore()
	if err := store.save([]persistedJob{
		testPersistedJob("job-7", statusRunning),
		testPersistedJob("job-2", statusFailed),
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	m := newJobManager(newHub())
	m.attachStore(store)

	if _, ok := m.get("job-7"); !ok {
		t.Fatal("job-7 not restored")
	}
	if j, ok := m.get("job-2"); !ok || j.status != statusFailed {
		t.Fatal("job-2 not restored as failed")
	}
	// New ids must not collide with restored ones.
	if id := m.nextID(); id != "job-8" {
		t.Errorf("nextID = %q, want job-8", id)
	}
}

func TestPersistNowWritesAndSkipsDryRun(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	store := newJobStore()
	m := newJobManager(newHub())
	m.attachStore(store)

	real := newJob("job-1", "https://kino.pub/item/view/1", domain.RunConfig{InputURL: "https://kino.pub/item/view/1"})
	dry := newJob("job-2", "https://kino.pub/item/view/2", domain.RunConfig{InputURL: "https://kino.pub/item/view/2", DryRun: true})
	m.add(real)
	m.add(dry)
	m.markPersistDirty()
	m.persistNow()

	got := store.load()
	if len(got) != 1 || got[0].ID != "job-1" {
		t.Fatalf("persisted %v, want only job-1 (dry-run skipped)", got)
	}

	// Unchanged generation → no rewrite (file mtime/content stays as-is even if
	// the file is deleted, proving the gen gate short-circuits).
	os.Remove(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "kinopub", "jobs.json"))
	m.persistNow()
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "kinopub", "jobs.json")); !os.IsNotExist(err) {
		t.Error("persistNow rewrote without a generation bump")
	}
}

func TestRemovePersistsSynchronously(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	store := newJobStore()
	m := newJobManager(newHub())
	m.attachStore(store)

	j := newJob("job-1", "u", domain.RunConfig{})
	j.status = statusFailed
	m.add(j)
	m.markPersistDirty()
	m.persistNow()
	if got := store.load(); len(got) != 1 {
		t.Fatalf("precondition: expected 1 persisted job, got %d", len(got))
	}

	if ok, _ := m.remove("job-1"); !ok {
		t.Fatal("remove failed")
	}
	if got := store.load(); len(got) != 0 {
		t.Errorf("removed job still persisted: %v", got)
	}
}

// A card saved before the verdict came from the episode rows carries the last
// run's summary — "1 ok" from a per-episode retry — and reads Completed even
// though episodes are still broken. Restoring re-derives both from the rows, so
// the card heals (and gets its Retry back) on the next start.
func TestRestoreJobRecomputesStaleSummary(t *testing.T) {
	p := persistedJob{
		ID:      "job-9",
		URL:     "https://kino.pub/item/view/121819",
		Status:  statusCompleted,
		Summary: &SummaryView{Total: 1, Succeeded: 1},
		Episodes: []EpisodeView{
			{Key: "S1E1", Season: 1, Episode: 1, State: epCompleted, Percent: 100},
			{Key: "S1E2", Season: 1, Episode: 2, State: epCompleted, Percent: 100},
			{Key: "S1E3", Season: 1, Episode: 3, State: epFailed, Error: "boom"},
		},
	}
	j := restoreJob(p)
	if j.status != statusFailed {
		t.Errorf("status = %q, want %q — an episode is still broken", j.status, statusFailed)
	}
	if j.summary.Total != 3 || j.summary.Succeeded != 2 || j.summary.Failed != 1 {
		t.Errorf("summary = %+v, want 2 ok / 1 failed of 3", *j.summary)
	}
}

// A genuinely clean card must survive the same pass untouched.
func TestRestoreJobKeepsCleanCompleted(t *testing.T) {
	j := restoreJob(persistedJob{
		ID:       "job-10",
		Status:   statusCompleted,
		Episodes: []EpisodeView{{Key: "S1E1", Season: 1, Episode: 1, State: epCompleted, Percent: 100}},
	})
	if j.status != statusCompleted || j.summary.Failed != 0 {
		t.Errorf("status = %q, summary = %+v, want a clean completed card", j.status, *j.summary)
	}
}

// A card with no episode rows (a movie, a dry run) keeps the summary it was
// saved with — there are no rows to re-derive it from.
func TestRestoreJobKeepsSummaryWithoutRows(t *testing.T) {
	j := restoreJob(persistedJob{
		ID:      "job-11",
		Status:  statusCompleted,
		Summary: &SummaryView{Total: 1, Succeeded: 1},
	})
	if j.summary == nil || j.summary.Succeeded != 1 || j.status != statusCompleted {
		t.Errorf("status = %q, summary = %+v, want the persisted summary kept", j.status, j.summary)
	}
}

// A restored paused card is not a failure: its unfinished rows are held, not
// counted as failed, and it keeps the status that lets Resume work.
func TestRestoreJobPausedNotFailed(t *testing.T) {
	j := restoreJob(persistedJob{
		ID:     "job-12",
		Status: statusRunning, // died with the old process → restored as paused
		Episodes: []EpisodeView{
			{Key: "S1E1", Season: 1, Episode: 1, State: epCompleted, Percent: 100},
			{Key: "S1E2", Season: 1, Episode: 2, State: epRunning, Percent: 40},
		},
	})
	if j.status != statusPaused {
		t.Errorf("status = %q, want %q", j.status, statusPaused)
	}
	if j.summary.Failed != 0 {
		t.Errorf("summary = %+v, want no failures on a paused card", *j.summary)
	}
}
