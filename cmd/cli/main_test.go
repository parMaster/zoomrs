package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jessevdk/go-flags"
	"github.com/parMaster/zoomrs/client"
	"github.com/parMaster/zoomrs/config"
	"github.com/parMaster/zoomrs/storage"
	"github.com/parMaster/zoomrs/storage/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeZoom serves the Zoom API and the recording files. recordings is called for every
// listing request; the default returns no meetings.
type fakeZoom struct {
	srv        *httptest.Server
	recordings func(w http.ResponseWriter, r *http.Request)
	file       func(w http.ResponseWriter, r *http.Request)
	deletes    atomic.Int32
	mu         sync.Mutex
	listed     []string // "from..to" of every listing request
}

func (f *fakeZoom) listings() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.listed...)
}

// day formats a date the way the listing request carries it
func day(daysAgo int) string {
	return time.Now().AddDate(0, 0, -daysAgo).Format(time.DateOnly)
}

// captureLog collects what the test logs
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	out := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(out) })
	return &buf
}

func newFakeZoom(t *testing.T) *fakeZoom {
	t.Helper()
	f := &fakeZoom{}
	f.recordings = func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"meetings":[]}`)) }
	f.file = func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("videodat")) }
	mux := http.NewServeMux()
	mux.HandleFunc("POST /oauth/token", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
	})
	mux.HandleFunc("GET /users/me/recordings", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.listed = append(f.listed, r.URL.Query().Get("from")+".."+r.URL.Query().Get("to"))
		f.mu.Unlock()
		f.recordings(w, r)
	})
	mux.HandleFunc("DELETE /meetings/{id}/recordings", func(w http.ResponseWriter, _ *http.Request) {
		f.deletes.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /files/", func(w http.ResponseWriter, r *http.Request) { f.file(w, r) })
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// serveMeeting makes the listing return one meeting from yesterday with a single downloadable video
func (f *fakeZoom) serveMeeting(t *testing.T) {
	t.Helper()
	f.serveMeetingAt(t, time.Now().Add(-24*time.Hour))
}

func (f *fakeZoom) serveMeetingAt(t *testing.T, start time.Time) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"meetings": []map[string]any{{
		"uuid": "m1", "id": 1, "topic": "standup", "duration": 30,
		"start_time": start.Format(time.RFC3339),
		"recording_files": []map[string]any{{
			"id": "r1", "meeting_id": "m1", "recording_type": "shared_screen_with_gallery_view",
			"recording_start": start.Format(time.RFC3339),
			"file_extension":  "MP4", "file_size": 8, "download_url": f.srv.URL + "/files/video.mp4",
		}},
	}}})
	require.NoError(t, err)
	f.recordings = func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }
}

func newTestCommander(t *testing.T) (*Commander, *fakeZoom) {
	t.Helper()
	cfg, err := config.NewConfig("../../config/config_example.yml")
	require.NoError(t, err)
	cfg.Storage.Repository = t.TempDir()
	cfg.Storage.Path = "file:" + filepath.Join(t.TempDir(), "cli.db") + "?mode=rwc&_journal_mode=WAL"
	cfg.Storage.KeepFreeSpace = 0
	cfg.Commander.Instances = nil
	cfg.Client.RateLimitingDelay = config.RateLimitingDelay{Light: time.Millisecond, Medium: time.Millisecond, Heavy: time.Millisecond}

	f := newFakeZoom(t)
	c := NewCommander(cfg)
	c.client = client.NewZoomClient(cfg.Client, client.WithBaseURLs(f.srv.URL, f.srv.URL))
	return c, f
}

func TestRun_StorageError(t *testing.T) {
	c, _ := newTestCommander(t)
	c.cfg.Storage.Type = ""
	assert.ErrorContains(t, c.Run(context.Background(), Options{Cmd: "check"}), "failed to init storage")
}

// openStore opens the commander's database separately, since Run closes its own connection on return
func openStore(t *testing.T, c *Commander) storage.Storer {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var s storage.Storer
	require.NoError(t, LoadStorage(ctx, c.cfg.Storage, &s))
	return s
}

func TestRun_Check(t *testing.T) {
	c, _ := newTestCommander(t)
	require.NoError(t, c.Run(context.Background(), Options{Cmd: "check"}))

	now := time.Now()
	require.NoError(t, openStore(t, c).SaveMeeting(context.Background(), model.Meeting{UUID: "m1", StartTime: now, Records: []model.Record{{
		Id: "r1", MeetingId: "m1", StartTime: now, FileSize: 4, Status: model.StatusDownloaded,
		FilePath: filepath.Join(c.cfg.Storage.Repository, "missing.mp4"),
	}}}))
	assert.ErrorContains(t, c.Run(context.Background(), Options{Cmd: "check"}), "file does not exist")
}

// loadedInstance is a fake zoomrs service that confirms the given meetings as loaded
func loadedInstance(t *testing.T, loaded ...string) (url string, calls *atomic.Int32) {
	t.Helper()
	calls = &atomic.Int32{}
	body, err := json.Marshal(map[string]any{"result": "pending", "loaded": loaded})
	require.NoError(t, err)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, calls
}

func TestRun_Trash(t *testing.T) {
	ctx := context.Background()

	t.Run("one day with --days, the last 30 days without it", func(t *testing.T) {
		c, f := newTestCommander(t)
		// no meetings in the feed, so there is nothing to confirm or delete
		require.NoError(t, c.Run(ctx, Options{Cmd: "trash", Days: 2}))
		require.NoError(t, c.Run(ctx, Options{Cmd: "trash", Days: 0}))
		require.NoError(t, c.Run(ctx, Options{Cmd: "trash", Days: daysUnset}))
		assert.Equal(t, []string{day(2) + ".." + day(2), day(0) + ".." + day(0), day(30) + ".." + day(0)}, f.listings())
		assert.Zero(t, f.deletes.Load())
	})

	t.Run("trashes a meeting the instances confirm, keeps one they don't", func(t *testing.T) {
		c, f := newTestCommander(t)
		f.serveMeeting(t)
		has, _ := loadedInstance(t, "m1")
		lacks, _ := loadedInstance(t)

		c.cfg.Commander.Instances = []string{has, lacks}
		require.NoError(t, c.Run(ctx, Options{Cmd: "trash", Days: daysUnset}))
		assert.Zero(t, f.deletes.Load())

		c.cfg.Commander.Instances = []string{has, has}
		require.NoError(t, c.Run(ctx, Options{Cmd: "trash", Days: daysUnset}))
		assert.Equal(t, int32(1), f.deletes.Load())
	})

	t.Run("no instances configured trashes nothing", func(t *testing.T) {
		c, f := newTestCommander(t)
		f.serveMeeting(t)
		require.NoError(t, c.Run(ctx, Options{Cmd: "trash", Days: 1}))
		assert.Zero(t, f.deletes.Load())
	})

	t.Run("force trashes the day without asking, and only with an explicit day", func(t *testing.T) {
		c, f := newTestCommander(t)
		f.serveMeeting(t)
		lacks, calls := loadedInstance(t)
		c.cfg.Commander.Instances = []string{lacks}

		err := c.Run(ctx, Options{Cmd: "trash", Days: daysUnset, Force: true})
		assert.ErrorContains(t, err, "'--force' needs '--days' to be set")
		assert.Empty(t, f.listings(), "rejected before Zoom is asked")
		assert.Zero(t, f.deletes.Load())

		require.NoError(t, c.Run(ctx, Options{Cmd: "trash", Days: 1, Force: true}))
		assert.Equal(t, []string{day(1) + ".." + day(1)}, f.listings())
		assert.Equal(t, int32(1), f.deletes.Load())
		assert.Zero(t, calls.Load())
	})
}

func TestParseOptions(t *testing.T) {
	t.Run("days is unset by default and never read from DEBUG", func(t *testing.T) {
		t.Setenv("DEBUG", "true")
		opts, err := parseOptions([]string{"--cmd", "sync"})
		require.NoError(t, err)
		assert.Equal(t, daysUnset, opts.Days)
		assert.True(t, opts.Dbg)
		assert.Equal(t, "sync", opts.Cmd)
	})

	t.Run("--days N, 0 included", func(t *testing.T) {
		for arg, want := range map[string]int{"0": 0, "1": 1, "2": 2} {
			opts, err := parseOptions([]string{"--cmd", "sync", "--days", arg})
			require.NoError(t, err)
			assert.Equal(t, want, opts.Days)
		}
	})

	t.Run("--trash N is --days N with a deprecation warning", func(t *testing.T) {
		logged := captureLog(t)
		opts, err := parseOptions([]string{"--cmd", "trash", "--trash", "2"})
		require.NoError(t, err)
		assert.Equal(t, 2, opts.Days)
		assert.Contains(t, logged.String(), "[WARN] '--trash' is deprecated, use '--days 2'")

		opts, err = parseOptions([]string{"--cmd", "trash", "--trash", "0", "--days", "0"})
		require.NoError(t, err)
		assert.Equal(t, 0, opts.Days)
	})

	t.Run("no warning without --trash", func(t *testing.T) {
		logged := captureLog(t)
		_, err := parseOptions([]string{"--cmd", "trash", "--days", "2"})
		require.NoError(t, err)
		assert.Empty(t, logged.String())
	})

	t.Run("--trash and --days that disagree are rejected", func(t *testing.T) {
		_, err := parseOptions([]string{"--cmd", "trash", "--trash", "2", "--days", "3"})
		assert.EqualError(t, err, "'--trash 2' and '--days 3' disagree, use '--days' alone")
	})

	t.Run("negative days are rejected", func(t *testing.T) {
		for _, flag := range []string{"--days", "--trash"} {
			_, err := parseOptions([]string{"--cmd", "trash", flag + "=-2"})
			assert.EqualError(t, err, "'--days' and '--trash' can't be negative")
		}
	})

	t.Run("unknown flags and help are go-flags errors", func(t *testing.T) {
		_, err := parseOptions([]string{"--nope"})
		assert.Error(t, err)
		_, err = parseOptions([]string{"--help"})
		var flagsErr *flags.Error
		require.ErrorAs(t, err, &flagsErr)
		assert.Equal(t, flags.ErrHelp, flagsErr.Type)
	})
}

func TestOptionsInterval(t *testing.T) {
	now := time.Date(2026, 3, 31, 10, 0, 0, 0, time.Local)
	from, to := Options{Days: daysUnset}.interval(now)
	assert.Equal(t, "2026-03-01", from.Format(time.DateOnly))
	assert.Equal(t, now, to)

	for days, want := range map[int]string{0: "2026-03-31", 2: "2026-03-29"} {
		from, to = Options{Days: days}.interval(now)
		assert.Equal(t, want, from.Format(time.DateOnly))
		assert.Equal(t, from, to)
	}
}

func TestRun_CloudCap(t *testing.T) {
	c, f := newTestCommander(t)
	f.serveMeeting(t)
	var calls atomic.Int32
	serveOnce := f.recordings
	f.recordings = func(w http.ResponseWriter, r *http.Request) {
		// the meeting shows up in the first 30-day window only
		if calls.Add(1) == 1 {
			serveOnce(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"meetings":[]}`))
	}

	c.cfg.Client.CloudCapacityHardLimit = 4 // the 8-byte meeting is over it
	require.NoError(t, c.Run(context.Background(), Options{Cmd: "cloudcap"}))
	assert.Equal(t, int32(1), f.deletes.Load())

	c.cfg.Client.CloudCapacityHardLimit = 0
	assert.ErrorContains(t, c.Run(context.Background(), Options{Cmd: "cloudcap"}), "cloud storage capacity is not configured")
}

func recordState(t *testing.T, s storage.Storer, id string) model.Record {
	t.Helper()
	return recordOf(t, s, "m1", id)
}

func recordOf(t *testing.T, s storage.Storer, uuid, id string) model.Record {
	t.Helper()
	recs, err := s.GetRecords(context.Background(), uuid)
	require.NoError(t, err)
	for _, r := range recs {
		if r.Id == id {
			return r
		}
	}
	t.Fatalf("record %s not found", id)
	return model.Record{}
}

func TestRun_Sync(t *testing.T) {
	t.Run("lists the day, downloads everything and leaves it in the cloud", func(t *testing.T) {
		c, f := newTestCommander(t)
		c.cfg.Client.DeleteDownloaded = true
		f.serveMeeting(t)
		logged := captureLog(t)
		require.NoError(t, c.Run(context.Background(), Options{Cmd: "sync", Days: 1}))

		r := recordState(t, openStore(t, c), "r1")
		assert.Equal(t, model.StatusDownloaded, r.Status)
		data, err := os.ReadFile(r.FilePath)
		require.NoError(t, err)
		assert.Equal(t, "videodat", string(data))
		assert.Zero(t, f.deletes.Load())
		assert.Equal(t, []string{day(1) + ".." + day(1)}, f.listings())
		assert.NotContains(t, logged.String(), "left failed", "no summary when nothing failed")
	})

	t.Run("--days 0 and --days 2 list exactly that day", func(t *testing.T) {
		c, f := newTestCommander(t)
		require.NoError(t, c.Run(context.Background(), Options{Cmd: "sync", Days: 0}))
		require.NoError(t, c.Run(context.Background(), Options{Cmd: "sync", Days: 2}))
		assert.Equal(t, []string{day(0) + ".." + day(0), day(2) + ".." + day(2)}, f.listings())
	})

	t.Run("without --days lists the last 30 days in one call and downloads only what is listed", func(t *testing.T) {
		c, f := newTestCommander(t)
		f.serveMeetingAt(t, time.Now().Add(-20*24*time.Hour))
		store := openStore(t, c)
		// in the window by date, but Zoom no longer lists it
		seed(t, store, f, "unlisted", time.Now().Add(-10*24*time.Hour), map[string]model.RecordStatus{
			"u-queued": model.StatusQueued, "u-failed": model.StatusFailed, "u-stuck": model.StatusDownloading,
		})

		require.NoError(t, c.Run(context.Background(), Options{Cmd: "sync", Days: daysUnset}))

		assert.Equal(t, []string{day(30) + ".." + day(0)}, f.listings())
		assert.Equal(t, model.StatusDownloaded, recordState(t, store, "r1").Status)
		assert.Equal(t, model.StatusQueued, recordOf(t, store, "unlisted", "u-queued").Status)
		assert.Equal(t, model.StatusFailed, recordOf(t, store, "unlisted", "u-failed").Status)
		assert.Equal(t, model.StatusDownloading, recordOf(t, store, "unlisted", "u-stuck").Status)
		assert.Zero(t, f.deletes.Load())
	})

	t.Run("no sync types configured", func(t *testing.T) {
		c, _ := newTestCommander(t)
		c.cfg.Syncable = config.Syncable{}
		assert.ErrorContains(t, c.Run(context.Background(), Options{Cmd: "sync"}), "no sync types configured")
	})

	t.Run("listing error waits for a retry until canceled", func(t *testing.T) {
		c, f := newTestCommander(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		f.recordings = func(w http.ResponseWriter, _ *http.Request) {
			cancel()
			w.WriteHeader(http.StatusInternalServerError)
		}
		assert.ErrorIs(t, c.Run(ctx, Options{Cmd: "sync"}), context.Canceled)
	})

	t.Run("download error waits for a retry until canceled", func(t *testing.T) {
		c, f := newTestCommander(t)
		f.serveMeeting(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		f.file = func(w http.ResponseWriter, _ *http.Request) {
			cancel()
			w.WriteHeader(http.StatusNotFound)
		}
		assert.ErrorContains(t, c.Run(ctx, Options{Cmd: "sync"}), "downloading terminated")
	})

	t.Run("a run stopped by a signal still names the failed records", func(t *testing.T) {
		c, f := newTestCommander(t)
		f.serveMeeting(t)
		// too old to be requeued, so it is failed for good when the run is stopped
		seed(t, openStore(t, c), f, "m1", time.Now().Add(-5*24*time.Hour), map[string]model.RecordStatus{
			"gone": model.StatusFailed, "next": model.StatusQueued,
		})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		f.file = func(w http.ResponseWriter, _ *http.Request) {
			cancel()
			w.WriteHeader(http.StatusNotFound)
		}
		logged := captureLog(t)
		assert.ErrorIs(t, c.Run(ctx, Options{Cmd: "sync", Days: daysUnset}), context.Canceled)
		assert.Contains(t, logged.String(), "[WARN] failed: m1 | record gone | ")
	})
}

func TestRun_Sync_FailedRecords(t *testing.T) {
	// alwaysFails makes every download fail and counts the attempts. The download library
	// probes with a HEAD before each GET, so only GETs are counted.
	alwaysFails := func(f *fakeZoom) *atomic.Int32 {
		var attempts atomic.Int32
		f.file = func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				attempts.Add(1)
			}
			w.WriteHeader(http.StatusNotFound)
		}
		return &attempts
	}

	t.Run("a record that keeps failing is requeued once, then the run ends and names it", func(t *testing.T) {
		c, f := newTestCommander(t)
		c.retryWait = time.Millisecond
		f.serveMeeting(t)
		attempts := alwaysFails(f)
		logged := captureLog(t)

		require.NoError(t, c.Run(context.Background(), Options{Cmd: "sync", Days: daysUnset}))

		assert.Equal(t, int32(2), attempts.Load())
		r := recordState(t, openStore(t, c), "r1")
		assert.Equal(t, model.StatusFailed, r.Status)
		assert.Contains(t, logged.String(), "[WARN] 1 records are left failed:")
		assert.Contains(t, logged.String(), "[WARN] failed: standup | record r1 | "+r.DateTime)
	})

	t.Run("a failed record older than 3 days is not tried again, a stuck one is", func(t *testing.T) {
		c, f := newTestCommander(t)
		c.retryWait = time.Millisecond
		fiveDaysAgo := time.Now().Add(-5 * 24 * time.Hour)
		f.serveMeetingAt(t, fiveDaysAgo)
		store := openStore(t, c)
		seed(t, store, f, "m1", fiveDaysAgo, map[string]model.RecordStatus{
			"oldFailed": model.StatusFailed, "oldStuck": model.StatusDownloading,
		})
		var paths []string
		var mu sync.Mutex
		f.file = func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				mu.Lock()
				paths = append(paths, r.URL.Path)
				mu.Unlock()
			}
			_, _ = w.Write([]byte("videodat"))
		}
		logged := captureLog(t)

		require.NoError(t, c.Run(context.Background(), Options{Cmd: "sync", Days: daysUnset}))

		assert.Equal(t, []string{"/files/oldStuck.mp4"}, paths)
		assert.Equal(t, model.StatusDownloaded, recordOf(t, store, "m1", "oldStuck").Status)
		assert.Equal(t, model.StatusFailed, recordOf(t, store, "m1", "oldFailed").Status)
		assert.Contains(t, logged.String(), "[WARN] failed: m1 | record oldFailed | ")
	})

	t.Run("the timeout ends the run and the failed record is still named", func(t *testing.T) {
		c, f := newTestCommander(t)
		c.retryWait = time.Hour
		c.syncTimeout = 100 * time.Millisecond
		f.serveMeeting(t)
		attempts := alwaysFails(f)
		logged := captureLog(t)

		err := c.Run(context.Background(), Options{Cmd: "sync", Days: 1})
		assert.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Equal(t, int32(1), attempts.Load())
		assert.Contains(t, logged.String(), "[WARN] failed: standup | record r1 | ")
	})

	t.Run("a listing that never works has no summary", func(t *testing.T) {
		c, f := newTestCommander(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		f.recordings = func(w http.ResponseWriter, _ *http.Request) {
			cancel()
			w.WriteHeader(http.StatusInternalServerError)
		}
		logged := captureLog(t)
		assert.Error(t, c.Run(ctx, Options{Cmd: "sync", Days: daysUnset}))
		assert.NotContains(t, logged.String(), "left failed")
	})
}

// seed saves a meeting that an earlier run left in the database; each record is an
// 8-byte video the fake file server can serve
func seed(t *testing.T, s storage.Storer, f *fakeZoom, uuid string, start time.Time, statuses map[string]model.RecordStatus) {
	t.Helper()
	m := model.Meeting{UUID: uuid, Id: 7, Topic: uuid, StartTime: start}
	for id, status := range statuses {
		m.Records = append(m.Records, model.Record{
			Id: id, MeetingId: uuid, Type: model.SharedScreenWithGalleryView, StartTime: start,
			FileExtension: "MP4", FileSize: 8, Status: status, DownloadURL: f.srv.URL + "/files/" + id + ".mp4",
		})
	}
	require.NoError(t, s.SaveMeeting(context.Background(), m))
}

func TestRun_Sync_DownloadsOnlyTheRequestedDay(t *testing.T) {
	tenDaysAgo := time.Now().Add(-10 * 24 * time.Hour)
	yesterday := time.Now().Add(-24 * time.Hour)

	t.Run("a queued record of an older meeting stays queued", func(t *testing.T) {
		c, f := newTestCommander(t)
		f.serveMeeting(t)
		store := openStore(t, c)
		seed(t, store, f, "old", tenDaysAgo, map[string]model.RecordStatus{"old1": model.StatusQueued})

		require.NoError(t, c.Run(context.Background(), Options{Cmd: "sync", Days: 1}))

		assert.Equal(t, model.StatusQueued, recordOf(t, store, "old", "old1").Status)
		assert.Equal(t, model.StatusDownloaded, recordState(t, store, "r1").Status)
	})

	t.Run("failed and stuck records of an older meeting are not requeued", func(t *testing.T) {
		c, f := newTestCommander(t)
		f.serveMeeting(t)
		store := openStore(t, c)
		seed(t, store, f, "old", tenDaysAgo, map[string]model.RecordStatus{
			"oldFailed": model.StatusFailed, "oldStuck": model.StatusDownloading,
		})

		require.NoError(t, c.Run(context.Background(), Options{Cmd: "sync", Days: 1}))

		assert.Equal(t, model.StatusFailed, recordOf(t, store, "old", "oldFailed").Status)
		assert.Equal(t, model.StatusDownloading, recordOf(t, store, "old", "oldStuck").Status)
		assert.Equal(t, model.StatusDownloaded, recordState(t, store, "r1").Status)
	})

	t.Run("a failed record of the day is requeued and retried", func(t *testing.T) {
		c, f := newTestCommander(t)
		c.retryWait = time.Millisecond
		f.serveMeeting(t)
		var attempts atomic.Int32
		f.file = func(w http.ResponseWriter, _ *http.Request) {
			if attempts.Add(1) == 1 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte("videodat"))
		}

		require.NoError(t, c.Run(context.Background(), Options{Cmd: "sync", Days: 1}))

		assert.Equal(t, int32(2), attempts.Load())
		assert.Equal(t, model.StatusDownloaded, recordState(t, openStore(t, c), "r1").Status)
	})

	t.Run("a meeting of the day saved by an earlier run gets its records downloaded", func(t *testing.T) {
		c, f := newTestCommander(t)
		f.serveMeeting(t)
		store := openStore(t, c)
		seed(t, store, f, "m1", yesterday, map[string]model.RecordStatus{
			"early1": model.StatusQueued, "early2": model.StatusFailed,
		})

		require.NoError(t, c.Run(context.Background(), Options{Cmd: "sync", Days: 1}))

		assert.Equal(t, model.StatusDownloaded, recordOf(t, store, "m1", "early1").Status)
		assert.Equal(t, model.StatusDownloaded, recordOf(t, store, "m1", "early2").Status)
	})

	t.Run("a day with no meetings leaves the queue untouched", func(t *testing.T) {
		c, f := newTestCommander(t)
		store := openStore(t, c)
		seed(t, store, f, "old", tenDaysAgo, map[string]model.RecordStatus{
			"old1": model.StatusQueued, "oldFailed": model.StatusFailed,
		})

		require.NoError(t, c.Run(context.Background(), Options{Cmd: "sync", Days: 1}))

		assert.Equal(t, model.StatusQueued, recordOf(t, store, "old", "old1").Status)
		assert.Equal(t, model.StatusFailed, recordOf(t, store, "old", "oldFailed").Status)
		assert.Zero(t, f.deletes.Load())
	})
}

func TestLoadStorage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var s storage.Storer

	require.NoError(t, LoadStorage(ctx, config.Storage{Type: "sqlite", Path: "file:" + filepath.Join(t.TempDir(), "x.db") + "?mode=rwc"}, &s))
	assert.NotNil(t, s)
	assert.EqualError(t, LoadStorage(ctx, config.Storage{}, &s), "storage is not configured")
	assert.EqualError(t, LoadStorage(ctx, config.Storage{Type: "mongo"}, &s), "storage type mongo is not supported")
	err := LoadStorage(ctx, config.Storage{Type: "sqlite", Path: "file:" + filepath.Join(t.TempDir(), "no", "x.db") + "?mode=rwc"}, &s)
	assert.EqualError(t, err, "failed to init SQLite storage: unable to open database file: no such file or directory")

	canceled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	err = LoadStorage(canceled, config.Storage{Type: "sqlite", Path: "file:" + filepath.Join(t.TempDir(), "y.db") + "?mode=rwc"}, &s)
	assert.ErrorIs(t, err, context.Canceled)
}
