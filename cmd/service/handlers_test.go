package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/parMaster/zoomrs/client"
	"github.com/parMaster/zoomrs/config"
	"github.com/parMaster/zoomrs/storage"
	"github.com/parMaster/zoomrs/storage/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errBoom = errors.New("boom")

// stubStore wraps a real store and lets a test make single methods fail
type stubStore struct {
	storage.Storer
	stats        func(ctx context.Context) (map[model.RecordStatus]any, error)
	listMeetings func(ctx context.Context) ([]model.Meeting, error)
	getMeeting   func(ctx context.Context, uuid string) (*model.Meeting, error)
	getRecords   func(ctx context.Context, uuid string) ([]model.Record, error)
}

func (s *stubStore) Stats(ctx context.Context) (map[model.RecordStatus]any, error) {
	if s.stats != nil {
		return s.stats(ctx)
	}
	return s.Storer.Stats(ctx)
}

func (s *stubStore) ListMeetings(ctx context.Context) ([]model.Meeting, error) {
	if s.listMeetings != nil {
		return s.listMeetings(ctx)
	}
	return s.Storer.ListMeetings(ctx)
}

func (s *stubStore) GetMeeting(ctx context.Context, uuid string) (*model.Meeting, error) {
	if s.getMeeting != nil {
		return s.getMeeting(ctx, uuid)
	}
	return s.Storer.GetMeeting(ctx, uuid)
}

func (s *stubStore) GetRecords(ctx context.Context, uuid string) ([]model.Record, error) {
	if s.getRecords != nil {
		return s.getRecords(ctx, uuid)
	}
	return s.Storer.GetRecords(ctx, uuid)
}

func serve(t *testing.T, h http.Handler, method, path string, body []byte, header ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	return rw
}

func accessKey(uuid, salt string) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(uuid+salt)))
}

func TestStatusHandler(t *testing.T) {
	type statusResp struct {
		Status         string         `json:"status"`
		Stats          map[string]any `json:"stats"`
		LastDownloaded string         `json:"last_downloaded"`
		Cloud          *struct {
			UsagePercent int `json:"usage_percent"`
		} `json:"cloud"`
		Storage map[string]any `json:"storage"`
	}
	get := func(t *testing.T, s *Server, ctx context.Context) (*httptest.ResponseRecorder, statusResp) {
		t.Helper()
		rw := serve(t, s.router(ctx), http.MethodGet, "/status", nil)
		var resp statusResp
		if rw.Code == http.StatusOK {
			require.NoError(t, json.Unmarshal(rw.Body.Bytes(), &resp))
		}
		return rw, resp
	}

	t.Run("reports downloads, the latest cloud usage and local disk", func(t *testing.T) {
		s, ctx, _ := newTestServerWithZoom(t)
		rw, resp := get(t, s, ctx)
		require.Equal(t, http.StatusOK, rw.Code)
		assert.Equal(t, "OK", resp.Status)
		assert.Contains(t, resp.Stats, "downloaded")
		assert.NotEmpty(t, resp.LastDownloaded)
		require.NotNil(t, resp.Cloud)
		assert.Equal(t, 20, resp.Cloud.UsagePercent) // last day of the report: 2 GB of 10 GB
		assert.Contains(t, resp.Storage, "free")
	})

	t.Run("caches the last meeting and the cloud report", func(t *testing.T) {
		s, ctx, zoom := newTestServerWithZoom(t)
		for range 2 {
			rw, _ := get(t, s, ctx)
			require.Equal(t, http.StatusOK, rw.Code)
		}
		assert.Equal(t, int32(1), zoom.reportCalls.Load())
	})

	t.Run("LOADING while anything is queued or downloading", func(t *testing.T) {
		s, ctx, _ := newTestServerWithZoom(t)
		require.NoError(t, s.store.SaveMeeting(ctx, model.Meeting{UUID: "m2", StartTime: time.Now(),
			Records: []model.Record{{Id: "q", MeetingId: "m2", StartTime: time.Now(), Status: model.StatusQueued}}}))
		_, resp := get(t, s, ctx)
		assert.Equal(t, "LOADING", resp.Status)
	})

	t.Run("FAILED when something failed and nothing is loading", func(t *testing.T) {
		s, ctx, _ := newTestServerWithZoom(t)
		require.NoError(t, s.store.SaveMeeting(ctx, model.Meeting{UUID: "m2", StartTime: time.Now(),
			Records: []model.Record{{Id: "f", MeetingId: "m2", StartTime: time.Now(), Status: model.StatusFailed}}}))
		_, resp := get(t, s, ctx)
		assert.Equal(t, "FAILED", resp.Status)
	})

	t.Run("no cloud section when the report is empty; zero quota means 0%", func(t *testing.T) {
		s, ctx, zoom := newTestServerWithZoom(t)
		zoom.report.Store(`{"cloud_recording_storage":[]}`)
		_, resp := get(t, s, ctx)
		assert.Nil(t, resp.Cloud)

		s, ctx, zoom = newTestServerWithZoom(t)
		zoom.report.Store(`{"cloud_recording_storage":[{"usage":"1 GB","plan_usage":"0","free_usage":"0"}]}`)
		_, resp = get(t, s, ctx)
		require.NotNil(t, resp.Cloud)
		assert.Equal(t, 0, resp.Cloud.UsagePercent)
	})

	t.Run("no stats is 204", func(t *testing.T) {
		s, ctx, _ := newTestServerWithZoom(t)
		s.store = &stubStore{Storer: s.store, stats: func(context.Context) (map[model.RecordStatus]any, error) { return nil, errBoom }}
		rw, _ := get(t, s, ctx)
		assert.Equal(t, http.StatusNoContent, rw.Code)
	})

	t.Run("failures are 500", func(t *testing.T) {
		s, ctx, _ := newTestServerWithZoom(t)
		s.store = &stubStore{Storer: s.store, listMeetings: func(context.Context) ([]model.Meeting, error) { return nil, errBoom }}
		rw, _ := get(t, s, ctx)
		assert.Equal(t, http.StatusInternalServerError, rw.Code, "listing meetings fails")

		s, ctx, zoom := newTestServerWithZoom(t)
		zoom.reportStatus.Store(http.StatusBadGateway)
		rw, _ = get(t, s, ctx)
		assert.Equal(t, http.StatusInternalServerError, rw.Code, "cloud report fails")

		s, ctx, _ = newTestServerWithZoom(t)
		s.cfg.Storage.Repository = filepath.Join(t.TempDir(), "gone")
		rw, _ = get(t, s, ctx)
		assert.Equal(t, http.StatusInternalServerError, rw.Code, "disk usage fails")
	})

	t.Run("panics when nothing is downloaded yet", func(t *testing.T) {
		// BUG: with records but no downloaded video, ListMeetings is empty and the handler
		// indexes meetingsLoaded[0]; net/http recovers it, so the client just sees the connection drop
		s, ctx, _ := newTestServerWithZoom(t)
		s.store = &stubStore{Storer: s.store, listMeetings: func(context.Context) ([]model.Meeting, error) { return nil, nil }}
		h := s.statusHandler(ctx)
		assert.Panics(t, func() { h(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/status", nil)) })
	})
}

func TestListMeetings(t *testing.T) {
	t.Run("adds a salted access key to each meeting", func(t *testing.T) {
		s, ctx := newTestServer(t)
		rw := serve(t, s.router(ctx), http.MethodGet, "/listMeetings", nil, "X-JWT", authHeader(t, s))
		require.Equal(t, http.StatusOK, rw.Code)
		var resp struct {
			Data []model.Meeting `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rw.Body.Bytes(), &resp))
		require.Len(t, resp.Data, 1)
		assert.Equal(t, accessKey("testUUID", s.cfg.Server.AccessKeySalt), resp.Data[0].AccessKey)
	})

	t.Run("store error is 500", func(t *testing.T) {
		s, ctx := newTestServer(t)
		s.store = &stubStore{Storer: s.store, listMeetings: func(context.Context) ([]model.Meeting, error) { return nil, errBoom }}
		rw := serve(t, s.router(ctx), http.MethodGet, "/listMeetings", nil, "X-JWT", authHeader(t, s))
		assert.Equal(t, http.StatusInternalServerError, rw.Code)
	})

	t.Run("no user in the request is 500", func(t *testing.T) {
		// only reachable when the handler is mounted without the auth middleware
		s, ctx := newTestServer(t)
		rw := httptest.NewRecorder()
		s.listMeetings(ctx)(rw, httptest.NewRequest(http.MethodGet, "/listMeetings", nil))
		assert.Equal(t, http.StatusInternalServerError, rw.Code)
	})
}

func TestWatchMeetingHandler(t *testing.T) {
	path := func(s *Server, uuid string) string {
		return "/watchMeeting/" + accessKey(uuid, s.cfg.Server.AccessKeySalt) + "?uuid=" + uuid
	}

	t.Run("valid key returns the meeting without download links", func(t *testing.T) {
		s, ctx := newTestServer(t)
		rw := serve(t, s.router(ctx), http.MethodGet, path(s, "testUUID"), nil)
		require.Equal(t, http.StatusOK, rw.Code)
		var resp struct {
			Meeting model.Meeting  `json:"meeting"`
			Records []model.Record `json:"records"`
		}
		require.NoError(t, json.Unmarshal(rw.Body.Bytes(), &resp))
		assert.Equal(t, "testUUID", resp.Meeting.UUID)
		require.Len(t, resp.Records, 2)
		for _, r := range resp.Records {
			assert.Empty(t, r.DownloadURL)
			assert.Empty(t, r.PlayURL)
			assert.Empty(t, r.FileExtension)
			assert.NotEmpty(t, r.FilePath)
		}
	})

	t.Run("key for another meeting is forbidden", func(t *testing.T) {
		s, ctx := newTestServer(t)
		p := "/watchMeeting/" + accessKey("other", s.cfg.Server.AccessKeySalt) + "?uuid=testUUID"
		assert.Equal(t, http.StatusForbidden, serve(t, s.router(ctx), http.MethodGet, p, nil).Code)
	})

	t.Run("unknown meeting is 404", func(t *testing.T) {
		s, ctx := newTestServer(t)
		assert.Equal(t, http.StatusNotFound, serve(t, s.router(ctx), http.MethodGet, path(s, "nope"), nil).Code)
	})

	t.Run("store errors are 500", func(t *testing.T) {
		s, ctx := newTestServer(t)
		real := s.store
		s.store = &stubStore{Storer: real, getMeeting: func(context.Context, string) (*model.Meeting, error) { return nil, errBoom }}
		assert.Equal(t, http.StatusInternalServerError, serve(t, s.router(ctx), http.MethodGet, path(s, "testUUID"), nil).Code)

		s.store = &stubStore{Storer: real, getRecords: func(context.Context, string) ([]model.Record, error) { return nil, errBoom }}
		assert.Equal(t, http.StatusInternalServerError, serve(t, s.router(ctx), http.MethodGet, path(s, "testUUID"), nil).Code)
	})
}

func TestMeetingsLoadedHandler(t *testing.T) {
	ask := func(t *testing.T, s *Server, ctx context.Context, body string) (int, string) {
		t.Helper()
		rw := serve(t, s.router(ctx), http.MethodPost, "/meetingsLoaded/"+s.cfg.Server.AccessKeySalt, []byte(body))
		if rw.Code != http.StatusOK {
			return rw.Code, ""
		}
		var resp struct {
			Result string `json:"result"`
		}
		require.NoError(t, json.Unmarshal(rw.Body.Bytes(), &resp))
		return rw.Code, resp.Result
	}

	t.Run("ok when every record is downloaded with the right size", func(t *testing.T) {
		s, ctx := newTestServer(t)
		_, result := ask(t, s, ctx, `{"meetings":["testUUID"]}`)
		assert.Equal(t, "ok", result)
	})

	t.Run("ok for an empty list", func(t *testing.T) {
		s, ctx := newTestServer(t)
		_, result := ask(t, s, ctx, `{"meetings":[]}`)
		assert.Equal(t, "ok", result)
	})

	t.Run("pending for an unknown meeting", func(t *testing.T) {
		s, ctx := newTestServer(t)
		_, result := ask(t, s, ctx, `{"meetings":["testUUID","unknown"]}`)
		assert.Equal(t, "pending", result)
	})

	t.Run("pending while a record is not downloaded", func(t *testing.T) {
		s, ctx := newTestServer(t)
		require.NoError(t, s.store.UpdateRecord(ctx, "recM4A", model.StatusQueued, ""))
		_, result := ask(t, s, ctx, `{"meetings":["testUUID"]}`)
		assert.Equal(t, "pending", result)
	})

	t.Run("pending when a file has the wrong size", func(t *testing.T) {
		s, ctx := newTestServer(t)
		require.NoError(t, os.WriteFile(filepath.Join(s.cfg.Storage.Repository, "recM4A.m4a"), []byte("x"), 0o600))
		_, result := ask(t, s, ctx, `{"meetings":["testUUID"]}`)
		assert.Equal(t, "pending", result)
	})

	t.Run("ok even when a downloaded file is missing", func(t *testing.T) {
		// BUG: a missing file is not checked, so the meeting counts as loaded and CleanupJob
		// may delete the only remaining copy from Zoom
		s, ctx := newTestServer(t)
		require.NoError(t, os.Remove(filepath.Join(s.cfg.Storage.Repository, "recM4A.m4a")))
		_, result := ask(t, s, ctx, `{"meetings":["testUUID"]}`)
		assert.Equal(t, "ok", result)
	})

	t.Run("bad request bodies are 500", func(t *testing.T) {
		s, ctx := newTestServer(t)
		code, _ := ask(t, s, ctx, `{"meetings":`)
		assert.Equal(t, http.StatusInternalServerError, code)
		code, _ = ask(t, s, ctx, `{"meetings":[],"extra":1}`)
		assert.Equal(t, http.StatusInternalServerError, code, "unknown fields are rejected")
	})

	t.Run("store error is 500", func(t *testing.T) {
		s, ctx := newTestServer(t)
		s.store = &stubStore{Storer: s.store, getRecords: func(context.Context, string) ([]model.Record, error) { return nil, errBoom }}
		code, _ := ask(t, s, ctx, `{"meetings":["testUUID"]}`)
		assert.Equal(t, http.StatusInternalServerError, code)
	})
}

func TestStatsHandler_NoDownloadsIs500(t *testing.T) {
	// BUG: repo.GetStats errors on an empty result, so a fresh install gets a 500 here
	s, ctx := newTestServer(t)
	require.NoError(t, s.store.DeleteMeeting(ctx, "testUUID"))
	rw := serve(t, s.router(ctx), http.MethodGet, "/stats/K", nil, "X-JWT", authHeader(t, s))
	assert.Equal(t, http.StatusInternalServerError, rw.Code)
}

func TestIndexPage_LoggedInUserGetsTheApp(t *testing.T) {
	s, ctx := newTestServer(t)
	rw := serve(t, s.router(ctx), http.MethodGet, "/", nil, "X-JWT", authHeader(t, s))
	require.Equal(t, http.StatusOK, rw.Code)
	assert.Contains(t, rw.Body.String(), "<html")
}

func TestWatchHandler_EmptyKeyIs400(t *testing.T) {
	// the router never matches an empty {accessKey}, so call the handler directly
	s, _ := newTestServer(t)
	rw := httptest.NewRecorder()
	s.watchHandler(rw, httptest.NewRequest(http.MethodGet, "/watch/", nil))
	assert.Equal(t, http.StatusBadRequest, rw.Code)
}

func TestRespondWithFile_DebugModeReadsFromDisk(t *testing.T) {
	s, ctx := newTestServer(t)
	s.cfg.Server.Dbg = true

	// paths are relative to the working directory, which is cmd/service under go test
	assert.Equal(t, http.StatusInternalServerError, serve(t, s.router(ctx), http.MethodGet, "/login", nil).Code)

	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir("../.."))
	t.Cleanup(func() { _ = os.Chdir(wd) })
	rw := serve(t, s.router(ctx), http.MethodGet, "/login", nil)
	assert.Equal(t, http.StatusOK, rw.Code)
	assert.Contains(t, rw.Body.String(), "<html")
}

func TestLoadStorage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var s storage.Storer

	require.NoError(t, LoadStorage(ctx, config.Storage{Type: "sqlite", Path: "file:" + filepath.Join(t.TempDir(), "x.db") + "?mode=rwc"}, &s))
	assert.NotNil(t, s)

	assert.EqualError(t, LoadStorage(ctx, config.Storage{}, &s), "storage is not configured")
	assert.EqualError(t, LoadStorage(ctx, config.Storage{Type: "mongo"}, &s), "storage type mongo is not supported")
	// the sqlite error is formatted with %e, so only the prefix is reliable
	err := LoadStorage(ctx, config.Storage{Type: "sqlite", Path: "file:" + filepath.Join(t.TempDir(), "no", "x.db") + "?mode=rwc"}, &s)
	assert.ErrorContains(t, err, "failed to init SQLite storage")
}

func newZoomClientFor(cfg *config.Parameters, zoom *fakeZoomAPI) *client.ZoomClient {
	return client.NewZoomClient(cfg.Client, client.WithBaseURLs(zoom.srv.URL, zoom.srv.URL))
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

func TestRun_ServesUntilCanceled(t *testing.T) {
	cfg, err := config.NewConfig("../../config/config_example.yml")
	require.NoError(t, err)
	cfg.Server.Listen = freePort(t)
	cfg.Server.Dbg = false
	cfg.Storage.Repository = t.TempDir()
	cfg.Storage.Path = "file:" + filepath.Join(t.TempDir(), "run.db") + "?mode=rwc&_journal_mode=WAL"
	zoom := newFakeZoomAPI(t)

	s := NewServer(cfg)
	s.client = newZoomClientFor(cfg, zoom)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Run(ctx)
	}()

	require.Eventually(t, func() bool {
		resp, err := http.Get("http://" + cfg.Server.Listen + "/favicon.ico")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 20*time.Millisecond)

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestStartServer_BadAddressReturnsAfterCancel(t *testing.T) {
	s, _ := newTestServer(t)
	s.cfg.Server.Listen = "127.0.0.1:-1"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.startServer(ctx) // returns: listen fails, ctx is done, shutdown of an unstarted server is a no-op
}
