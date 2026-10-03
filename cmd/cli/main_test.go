package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

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
	mux.HandleFunc("GET /users/me/recordings", func(w http.ResponseWriter, r *http.Request) { f.recordings(w, r) })
	mux.HandleFunc("DELETE /meetings/{id}/recordings", func(w http.ResponseWriter, _ *http.Request) {
		f.deletes.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /files/", func(w http.ResponseWriter, r *http.Request) { f.file(w, r) })
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// serveMeeting makes the listing return one meeting with a single downloadable video
func (f *fakeZoom) serveMeeting(t *testing.T) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"meetings": []map[string]any{{
		"uuid": "m1", "id": 1, "topic": "standup", "duration": 30,
		"start_time": time.Now().Add(-24 * time.Hour).Format(time.RFC3339),
		"recording_files": []map[string]any{{
			"id": "r1", "meeting_id": "m1", "recording_type": "shared_screen_with_gallery_view",
			"recording_start": time.Now().Add(-24 * time.Hour).Format(time.RFC3339),
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
	cfg.Client.TrashDownloaded = true
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

func TestRun_Trash(t *testing.T) {
	c, _ := newTestCommander(t)
	assert.ErrorContains(t, c.Run(context.Background(), Options{Cmd: "trash", Trash: -1}), "'--trash' option (days) is not set")
	// no meetings that day, so there is nothing to confirm or delete
	assert.NoError(t, c.Run(context.Background(), Options{Cmd: "trash", Trash: 2}))
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
	recs, err := s.GetRecords(context.Background(), "m1")
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
	t.Run("lists the day, downloads everything and trashes it in the cloud", func(t *testing.T) {
		c, f := newTestCommander(t)
		f.serveMeeting(t)
		require.NoError(t, c.Run(context.Background(), Options{Cmd: "sync", Days: 1}))

		r := recordState(t, openStore(t, c), "r1")
		assert.Equal(t, model.StatusDownloaded, r.Status)
		data, err := os.ReadFile(r.FilePath)
		require.NoError(t, err)
		assert.Equal(t, "videodat", string(data))
		assert.Equal(t, int32(1), f.deletes.Load())
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
