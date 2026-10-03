package repo

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/parMaster/zoomrs/client"
	"github.com/parMaster/zoomrs/config"
	"github.com/parMaster/zoomrs/storage"
	"github.com/parMaster/zoomrs/storage/model"
	"github.com/parMaster/zoomrs/storage/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errBoom = errors.New("boom")

type deleteCall struct {
	uuid   string
	delete bool
}

// fakeClient implements Client; zero value returns no meetings and a valid token
type fakeClient struct {
	mu          sync.Mutex
	meetings    []model.Meeting
	meetingsErr error
	daysAgo     []int
	token       string // handed out by GetToken; "tok" when empty
	tokenErr    error
	refreshErr  error
	refreshes   atomic.Int32
	deleteErr   error
	deletes     []deleteCall
	onDelete    func()
}

func (f *fakeClient) Authorize() error { return nil }

func (f *fakeClient) GetMeetings(_ context.Context, daysAgo int) ([]model.Meeting, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.daysAgo = append(f.daysAgo, daysAgo)
	return f.meetings, f.meetingsErr
}

func (f *fakeClient) GetToken() (*client.AccessToken, error) {
	if f.tokenErr != nil {
		return nil, f.tokenErr
	}
	return &client.AccessToken{AccessToken: cmp.Or(f.token, "tok")}, nil
}

// RefreshToken hands out "tok2", so a server can tell the new token from the rejected "tok"
func (f *fakeClient) RefreshToken(*client.AccessToken) (*client.AccessToken, error) {
	f.refreshes.Add(1)
	if f.refreshErr != nil {
		return nil, f.refreshErr
	}
	return &client.AccessToken{AccessToken: "tok2"}, nil
}

func (f *fakeClient) DeleteMeetingRecordings(uuid string, del bool) error {
	f.mu.Lock()
	f.deletes = append(f.deletes, deleteCall{uuid, del})
	onDelete := f.onDelete
	f.mu.Unlock()
	if onDelete != nil {
		onDelete()
	}
	return f.deleteErr
}

func (f *fakeClient) deleteCalls() []deleteCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]deleteCall(nil), f.deletes...)
}

// stubStore wraps a real store and lets a test replace single methods, mostly to inject errors
type stubStore struct {
	storage.Storer
	getMeeting         func(ctx context.Context, uuid string) (*model.Meeting, error)
	saveMeeting        func(ctx context.Context, m model.Meeting) error
	getRecords         func(ctx context.Context, uuid string) ([]model.Record, error)
	getRecordsByStatus func(ctx context.Context, rs model.RecordStatus) ([]model.Record, error)
	updateRecord       func(ctx context.Context, id string, status model.RecordStatus, path string) error
	getQueuedRecord    func(ctx context.Context) (*model.Record, error)
	resetFailedRecords func(ctx context.Context) error
}

func (s *stubStore) GetMeeting(ctx context.Context, uuid string) (*model.Meeting, error) {
	if s.getMeeting != nil {
		return s.getMeeting(ctx, uuid)
	}
	return s.Storer.GetMeeting(ctx, uuid)
}

func (s *stubStore) SaveMeeting(ctx context.Context, m model.Meeting) error {
	if s.saveMeeting != nil {
		return s.saveMeeting(ctx, m)
	}
	return s.Storer.SaveMeeting(ctx, m)
}

func (s *stubStore) GetRecords(ctx context.Context, uuid string) ([]model.Record, error) {
	if s.getRecords != nil {
		return s.getRecords(ctx, uuid)
	}
	return s.Storer.GetRecords(ctx, uuid)
}

func (s *stubStore) GetRecordsByStatus(ctx context.Context, rs model.RecordStatus) ([]model.Record, error) {
	if s.getRecordsByStatus != nil {
		return s.getRecordsByStatus(ctx, rs)
	}
	return s.Storer.GetRecordsByStatus(ctx, rs)
}

func (s *stubStore) UpdateRecord(ctx context.Context, id string, status model.RecordStatus, path string) error {
	if s.updateRecord != nil {
		return s.updateRecord(ctx, id, status, path)
	}
	return s.Storer.UpdateRecord(ctx, id, status, path)
}

func (s *stubStore) GetQueuedRecord(ctx context.Context) (*model.Record, error) {
	if s.getQueuedRecord != nil {
		return s.getQueuedRecord(ctx)
	}
	return s.Storer.GetQueuedRecord(ctx)
}

func (s *stubStore) ResetFailedRecords(ctx context.Context) error {
	if s.resetFailedRecords != nil {
		return s.resetFailedRecords(ctx)
	}
	return s.Storer.ResetFailedRecords(ctx)
}

func testConfig(t *testing.T) *config.Parameters {
	t.Helper()
	return &config.Parameters{
		Server: config.Server{AccessKeySalt: "salt"},
		Client: config.Client{
			TrashDownloaded:   true,
			RateLimitingDelay: config.RateLimitingDelay{Light: time.Millisecond, Medium: time.Millisecond, Heavy: time.Millisecond},
		},
		Storage: config.Storage{Type: "sqlite", Repository: t.TempDir()},
		Syncable: config.Syncable{
			Important:   []string{string(model.SharedScreenWithGalleryView)},
			Alternative: []string{string(model.SharedScreenWithSpeakerView)},
			Optional:    []string{string(model.ChatFile)},
			MinDuration: 3,
		},
	}
}

func newStore(t *testing.T) *sqlite.SQLiteStorage {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	store, err := sqlite.NewStorage(ctx, "file:"+filepath.Join(t.TempDir(), "repo_test.db")+"?mode=rwc&_journal_mode=WAL")
	require.NoError(t, err)
	return store
}

// newTestRepo wires a repository over a real sqlite store, a fake client and a disk
// that always has plenty of free space
func newTestRepo(t *testing.T) (*Repository, *sqlite.SQLiteStorage, *fakeClient, *config.Parameters) {
	t.Helper()
	cfg := testConfig(t)
	store := newStore(t)
	fc := &fakeClient{}
	r := NewRepository(store, fc, cfg)
	r.diskFree = func(string) (uint64, error) { return 1 << 40, nil }
	return r, store, fc, cfg
}

func rec(id, meetingID string, typ model.RecordType, start time.Time, status model.RecordStatus, size model.FileSize) model.Record {
	return model.Record{
		Id: id, MeetingId: meetingID, Type: typ, StartTime: start, FileExtension: "MP4",
		FileSize: size, Status: status, DownloadURL: "http://example.invalid/" + id,
	}
}

func mtg(uuid string, start time.Time, duration int, records ...model.Record) model.Meeting {
	return model.Meeting{UUID: uuid, Id: 1, Topic: "topic " + uuid, StartTime: start, Duration: duration, Records: records}
}

func recordIDs(t *testing.T, store storage.Storer, uuid string) []string {
	t.Helper()
	recs, err := store.GetRecords(context.Background(), uuid)
	require.NoError(t, err)
	ids := []string{}
	for _, r := range recs {
		ids = append(ids, r.Id)
	}
	return ids
}

func TestNewRepository_BuildsSyncableSets(t *testing.T) {
	r, _, _, _ := newTestRepo(t)
	assert.Equal(t, map[model.RecordType]bool{model.SharedScreenWithGalleryView: true}, r.Syncable.Important)
	assert.Equal(t, map[model.RecordType]bool{model.SharedScreenWithSpeakerView: true}, r.Syncable.Alternative)
	assert.Equal(t, map[model.RecordType]bool{model.ChatFile: true}, r.Syncable.Optional)
}

func TestSyncMeetings(t *testing.T) {
	now := time.Now()
	ctx := context.Background()

	t.Run("no meetings is a no-op", func(t *testing.T) {
		r, _, _, _ := newTestRepo(t)
		assert.NoError(t, r.SyncMeetings(ctx, &[]model.Meeting{}))
	})

	t.Run("picks record types by priority", func(t *testing.T) {
		r, store, _, _ := newTestRepo(t)
		meetings := []model.Meeting{
			// important present: alternative is dropped, optional kept
			mtg("important", now, 10,
				rec("i-gal", "important", model.SharedScreenWithGalleryView, now, "", 1),
				rec("i-spk", "important", model.SharedScreenWithSpeakerView, now, "", 1),
				rec("i-chat", "important", model.ChatFile, now, "", 1),
				rec("i-audio", "important", model.AudioOnly, now, "", 1)),
			// no important: alternative is used, optional kept
			mtg("alternative", now, 10,
				rec("a-spk", "alternative", model.SharedScreenWithSpeakerView, now, "", 1),
				rec("a-chat", "alternative", model.ChatFile, now, "", 1)),
			// only optional
			mtg("optional", now, 10,
				rec("o-chat", "optional", model.ChatFile, now, "", 1)),
		}
		require.NoError(t, r.SyncMeetings(ctx, &meetings))

		assert.ElementsMatch(t, []string{"i-gal", "i-chat"}, recordIDs(t, store, "important"))
		assert.ElementsMatch(t, []string{"a-spk", "a-chat"}, recordIDs(t, store, "alternative"))
		assert.ElementsMatch(t, []string{"o-chat"}, recordIDs(t, store, "optional"))

		recs, err := store.GetRecords(ctx, "important")
		require.NoError(t, err)
		for _, r := range recs {
			assert.Equal(t, model.StatusQueued, r.Status)
		}
	})

	t.Run("skips short and empty meetings without deleting them", func(t *testing.T) {
		r, store, fc, _ := newTestRepo(t)
		meetings := []model.Meeting{
			mtg("short", now, 2, rec("s1", "short", model.SharedScreenWithGalleryView, now, "", 1)),
			mtg("empty", now, 10, rec("e1", "empty", model.AudioOnly, now, "", 1)),
		}
		require.NoError(t, r.SyncMeetings(ctx, &meetings))

		_, err := store.GetMeeting(ctx, "short")
		assert.ErrorIs(t, err, storage.ErrNoRows)
		_, err = store.GetMeeting(ctx, "empty")
		assert.ErrorIs(t, err, storage.ErrNoRows)
		assert.Empty(t, fc.deleteCalls())
	})

	t.Run("deletes skipped meetings when delete_skipped is on", func(t *testing.T) {
		r, _, fc, cfg := newTestRepo(t)
		cfg.Client.DeleteSkipped = true
		cfg.Client.DeleteDownloaded = true
		fc.deleteErr = errBoom // delete failures are only logged
		meetings := []model.Meeting{
			mtg("short", now, 2),
			mtg("empty", now, 10, rec("e1", "empty", model.AudioOnly, now, "", 1)),
		}
		require.NoError(t, r.SyncMeetings(ctx, &meetings))
		assert.Equal(t, []deleteCall{{"short", true}, {"empty", true}}, fc.deleteCalls())
	})

	t.Run("already saved meetings are left alone", func(t *testing.T) {
		r, store, _, _ := newTestRepo(t)
		m := mtg("m1", now, 10, rec("r1", "m1", model.SharedScreenWithGalleryView, now, "", 1))
		require.NoError(t, store.SaveMeeting(ctx, m))
		require.NoError(t, store.UpdateRecord(ctx, "r1", model.StatusDownloaded, "/some/path"))

		require.NoError(t, r.SyncMeetings(ctx, &[]model.Meeting{m}))
		recs, err := store.GetRecords(ctx, "m1")
		require.NoError(t, err)
		require.Len(t, recs, 1)
		assert.Equal(t, model.StatusDownloaded, recs[0].Status)
	})

	t.Run("lookup error aborts", func(t *testing.T) {
		r, store, _, _ := newTestRepo(t)
		r.store = &stubStore{Storer: store, getMeeting: func(context.Context, string) (*model.Meeting, error) { return nil, errBoom }}
		err := r.SyncMeetings(ctx, &[]model.Meeting{mtg("m1", now, 10)})
		assert.ErrorIs(t, err, errBoom)
	})

	t.Run("save error aborts", func(t *testing.T) {
		r, store, _, _ := newTestRepo(t)
		r.store = &stubStore{Storer: store, saveMeeting: func(context.Context, model.Meeting) error { return errBoom }}
		m := mtg("m1", now, 10, rec("r1", "m1", model.SharedScreenWithGalleryView, now, "", 1))
		err := r.SyncMeetings(ctx, &[]model.Meeting{m})
		assert.ErrorIs(t, err, errBoom)
	})
}

// runJob runs a long-running job in the background and returns a func that cancels it
// and waits for it to return
func runJob(t *testing.T, job func(ctx context.Context)) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		job(ctx)
	}()
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("job did not stop after its context was canceled")
		}
	}
}

func TestSyncJob(t *testing.T) {
	now := time.Now()

	t.Run("does not run without sync types", func(t *testing.T) {
		r, _, fc, _ := newTestRepo(t)
		r.Syncable = syncable{}
		r.SyncJob(context.Background()) // returns immediately, or the test times out
		assert.Empty(t, fc.daysAgo)
	})

	t.Run("syncs yesterday's meetings, then waits for the next tick", func(t *testing.T) {
		r, store, fc, _ := newTestRepo(t)
		fc.meetings = []model.Meeting{mtg("m1", now, 10, rec("r1", "m1", model.SharedScreenWithGalleryView, now, "", 1))}
		stop := runJob(t, r.SyncJob)
		require.Eventually(t, func() bool {
			_, err := store.GetMeeting(context.Background(), "m1")
			return err == nil
		}, 5*time.Second, 10*time.Millisecond)
		stop()
		fc.mu.Lock()
		defer fc.mu.Unlock()
		assert.Equal(t, []int{1}, fc.daysAgo)
	})

	t.Run("waits after a listing error", func(t *testing.T) {
		r, _, fc, _ := newTestRepo(t)
		fc.meetingsErr = errBoom
		stop := runJob(t, r.SyncJob)
		require.Eventually(t, func() bool { fc.mu.Lock(); defer fc.mu.Unlock(); return len(fc.daysAgo) == 1 }, 5*time.Second, 10*time.Millisecond)
		stop()
	})

	t.Run("waits after a sync error", func(t *testing.T) {
		r, store, fc, _ := newTestRepo(t)
		var lookups atomic.Int32
		r.store = &stubStore{Storer: store, getMeeting: func(context.Context, string) (*model.Meeting, error) {
			lookups.Add(1)
			return nil, errBoom
		}}
		fc.meetings = []model.Meeting{mtg("m1", now, 10)}
		stop := runJob(t, r.SyncJob)
		require.Eventually(t, func() bool { return lookups.Load() == 1 }, 5*time.Second, 10*time.Millisecond)
		stop()
	})
}

// fileServer serves body at any path with the given status
func fileServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "tok", r.URL.Query().Get("access_token"))
		w.WriteHeader(status)
		if r.Method != http.MethodHead {
			_, _ = w.Write([]byte(body))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// queue saves one meeting with the given records and returns the first record as read back
// from the store, so DateTime is filled in like it is in production
func queue(t *testing.T, store storage.Storer, uuid string, recs ...model.Record) *model.Record {
	t.Helper()
	require.NoError(t, store.SaveMeeting(context.Background(), mtg(uuid, recs[0].StartTime, 10, recs...)))
	got, err := store.GetRecords(context.Background(), uuid)
	require.NoError(t, err)
	for i := range got {
		if got[i].Id == recs[0].Id {
			return &got[i]
		}
	}
	t.Fatalf("record %s not found", recs[0].Id)
	return nil
}

func recordStatus(t *testing.T, store storage.Storer, uuid, id string) model.Record {
	t.Helper()
	recs, err := store.GetRecords(context.Background(), uuid)
	require.NoError(t, err)
	for _, r := range recs {
		if r.Id == id {
			return r
		}
	}
	t.Fatalf("record %s not found", id)
	return model.Record{}
}

func TestDownloadRecord(t *testing.T) {
	now := time.Now()
	ctx := context.Background()

	t.Run("saves the file and marks the record downloaded", func(t *testing.T) {
		r, store, _, cfg := newTestRepo(t)
		srv := fileServer(t, http.StatusOK, "videodat")
		rc := rec("r1", "m1", model.SharedScreenWithGalleryView, now, "", 8)
		rc.DownloadURL = srv.URL + "/video.mp4"
		queued := queue(t, store, "m1", rc)

		require.NoError(t, r.DownloadRecord(ctx, queued))

		got := recordStatus(t, store, "m1", "r1")
		assert.Equal(t, model.StatusDownloaded, got.Status)
		recFolder, _ := queued.Paths(cfg.Storage.Repository)
		assert.Equal(t, filepath.Join(recFolder, "video.mp4"), got.FilePath)
		data, err := os.ReadFile(got.FilePath)
		require.NoError(t, err)
		assert.Equal(t, "videodat", string(data))
	})

	failures := []struct {
		name    string
		status  int
		body    string
		size    model.FileSize
		file    string
		wantErr string
	}{
		{"error status from server", http.StatusNotFound, "", 8, "/video.mp4", "failed to download"},
		{"2xx other than 200", http.StatusNonAuthoritativeInfo, "videodat", 8, "/video.mp4", "status 203"},
		{"size differs from the record", http.StatusOK, "videodat", 99, "/video.mp4", "size 8"},
		{"empty file", http.StatusOK, "", 0, "/video.mp4", "size 0"},
		{"extension differs from the record", http.StatusOK, "videodat", 8, "/video.m4a", "extension m4a"},
	}
	for _, tc := range failures {
		t.Run(tc.name+" marks the record failed", func(t *testing.T) {
			r, store, _, _ := newTestRepo(t)
			srv := fileServer(t, tc.status, tc.body)
			rc := rec("r1", "m1", model.SharedScreenWithGalleryView, now, "", tc.size)
			rc.DownloadURL = srv.URL + tc.file
			queued := queue(t, store, "m1", rc)

			err := r.DownloadRecord(ctx, queued)
			assert.ErrorContains(t, err, tc.wantErr)
			assert.Equal(t, model.StatusFailed, recordStatus(t, store, "m1", "r1").Status)
		})
	}

	t.Run("token error leaves the record untouched", func(t *testing.T) {
		r, store, fc, _ := newTestRepo(t)
		fc.tokenErr = errBoom
		queued := queue(t, store, "m1", rec("r1", "m1", model.SharedScreenWithGalleryView, now, "", 8))
		assert.ErrorIs(t, r.DownloadRecord(ctx, queued), errBoom)
		assert.Equal(t, model.StatusQueued, recordStatus(t, store, "m1", "r1").Status)
	})

	t.Run("destination that can't be created is an error", func(t *testing.T) {
		r, store, _, cfg := newTestRepo(t)
		readOnly := filepath.Join(t.TempDir(), "ro")
		require.NoError(t, os.Mkdir(readOnly, 0o500))
		t.Cleanup(func() { _ = os.Chmod(readOnly, 0o700) })
		cfg.Storage.Repository = readOnly
		queued := queue(t, store, "m1", rec("r1", "m1", model.SharedScreenWithGalleryView, now, "", 8))
		assert.Error(t, r.DownloadRecord(ctx, queued))
		assert.Equal(t, model.StatusDownloading, recordStatus(t, store, "m1", "r1").Status)
	})

	t.Run("free-space failure does not stop the download", func(t *testing.T) {
		r, store, _, _ := newTestRepo(t)
		r.diskFree = func(string) (uint64, error) { return 0, errBoom }
		srv := fileServer(t, http.StatusOK, "videodat")
		rc := rec("r1", "m1", model.SharedScreenWithGalleryView, now, "", 8)
		rc.DownloadURL = srv.URL + "/video.mp4"
		queued := queue(t, store, "m1", rc)
		require.NoError(t, r.DownloadRecord(ctx, queued))
	})

	t.Run("status update failures", func(t *testing.T) {
		r, store, _, _ := newTestRepo(t)
		srv := fileServer(t, http.StatusOK, "videodat")
		rc := rec("r1", "m1", model.SharedScreenWithGalleryView, now, "", 8)
		rc.DownloadURL = srv.URL + "/video.mp4"
		queued := queue(t, store, "m1", rc)
		r.store = &stubStore{Storer: store, updateRecord: func(context.Context, string, model.RecordStatus, string) error { return errBoom }}

		// the "downloading" update failure is only logged; the final "downloaded" one is returned
		assert.ErrorIs(t, r.DownloadRecord(ctx, queued), errBoom)

		// a failed "failed" update is only logged too
		rc2 := rec("r2", "m2", model.SharedScreenWithGalleryView, now, "", 99)
		rc2.DownloadURL = srv.URL + "/video.mp4"
		queued2 := queue(t, store, "m2", rc2)
		assert.ErrorContains(t, r.DownloadRecord(ctx, queued2), "size 8")
	})

	// rejectingServer answers 401 to the listed tokens and serves the file to any other.
	// It counts GETs only: the download library probes with a HEAD before each one.
	rejectingServer := func(t *testing.T, rejected ...string) (*httptest.Server, *atomic.Int32) {
		t.Helper()
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				calls.Add(1)
			}
			if slices.Contains(rejected, r.URL.Query().Get("access_token")) {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte("videodat"))
		}))
		t.Cleanup(srv.Close)
		return srv, &calls
	}

	t.Run("401 is retried once with a new token and the record never fails", func(t *testing.T) {
		r, store, fc, _ := newTestRepo(t)
		srv, calls := rejectingServer(t, "tok")
		rc := rec("r1", "m1", model.SharedScreenWithGalleryView, now, "", 8)
		rc.DownloadURL = srv.URL + "/video.mp4"
		queued := queue(t, store, "m1", rc)
		var statuses []model.RecordStatus
		r.store = &stubStore{Storer: store, updateRecord: func(ctx context.Context, id string, s model.RecordStatus, path string) error {
			statuses = append(statuses, s)
			return store.UpdateRecord(ctx, id, s, path)
		}}

		require.NoError(t, r.DownloadRecord(ctx, queued))

		assert.Equal(t, []model.RecordStatus{model.StatusDownloading, model.StatusDownloaded}, statuses)
		assert.Equal(t, model.StatusDownloaded, recordStatus(t, store, "m1", "r1").Status)
		assert.Equal(t, int32(2), calls.Load())
		assert.Equal(t, int32(1), fc.refreshes.Load())
	})

	t.Run("a second 401 marks the record failed and there is no third attempt", func(t *testing.T) {
		r, store, fc, _ := newTestRepo(t)
		srv, calls := rejectingServer(t, "tok", "tok2")
		rc := rec("r1", "m1", model.SharedScreenWithGalleryView, now, "", 8)
		rc.DownloadURL = srv.URL + "/video.mp4"
		queued := queue(t, store, "m1", rc)

		err := r.DownloadRecord(ctx, queued)
		assert.ErrorContains(t, err, "401")
		assert.NotContains(t, err.Error(), "tok2")
		assert.Equal(t, model.StatusFailed, recordStatus(t, store, "m1", "r1").Status)
		assert.Equal(t, int32(2), calls.Load())
		assert.Equal(t, int32(1), fc.refreshes.Load())
	})

	t.Run("a failed token refresh marks the record failed", func(t *testing.T) {
		r, store, fc, _ := newTestRepo(t)
		fc.refreshErr = errBoom
		srv, calls := rejectingServer(t, "tok")
		rc := rec("r1", "m1", model.SharedScreenWithGalleryView, now, "", 8)
		rc.DownloadURL = srv.URL + "/video.mp4"
		queued := queue(t, store, "m1", rc)

		assert.ErrorIs(t, r.DownloadRecord(ctx, queued), errBoom)
		assert.Equal(t, model.StatusFailed, recordStatus(t, store, "m1", "r1").Status)
		assert.Equal(t, int32(1), calls.Load())
	})

	t.Run("errors never carry the token", func(t *testing.T) {
		const secret = "very-secret-token"
		closed := httptest.NewServer(http.NotFoundHandler())
		closed.Close()
		serve := func(status int, body string) string {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(srv.Close)
			return srv.URL
		}
		cases := []struct {
			name string
			url  string
			size model.FileSize
		}{
			{"transport error", closed.URL + "/video.mp4", 8},
			{"bad status", serve(http.StatusNotFound, "") + "/video.mp4", 8},
			{"2xx other than 200", serve(http.StatusNonAuthoritativeInfo, "videodat") + "/video.mp4", 8},
			{"wrong size", serve(http.StatusOK, "videodat") + "/video.mp4", 99},
			{"wrong extension", serve(http.StatusOK, "videodat") + "/video.m4a", 8},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				var buf bytes.Buffer
				out := log.Writer()
				log.SetOutput(&buf)
				t.Cleanup(func() { log.SetOutput(out) })

				r, store, fc, _ := newTestRepo(t)
				fc.token = secret
				rc := rec("r1", "m1", model.SharedScreenWithGalleryView, now, "", tc.size)
				rc.DownloadURL = tc.url
				queued := queue(t, store, "m1", rc)

				err := r.DownloadRecord(ctx, queued)
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.url)
				assert.NotContains(t, err.Error(), secret)
				assert.NotContains(t, buf.String(), secret)
			})
		}
	})
}

func TestDownloadOnce(t *testing.T) {
	now := time.Now()
	ctx := context.Background()

	t.Run("nothing queued requeues failed and stuck records", func(t *testing.T) {
		r, store, _, _ := newTestRepo(t)
		require.NoError(t, store.SaveMeeting(ctx, mtg("m1", now, 10,
			rec("failed", "m1", model.SharedScreenWithGalleryView, now, model.StatusFailed, 1),
			rec("stuck", "m1", model.ChatFile, now, model.StatusDownloading, 1),
		)))

		assert.ErrorIs(t, r.DownloadOnce(ctx), ErrNoQueuedRecords)
		assert.Equal(t, model.StatusQueued, recordStatus(t, store, "m1", "failed").Status)
		assert.Equal(t, model.StatusQueued, recordStatus(t, store, "m1", "stuck").Status)
	})

	t.Run("requeue error is returned", func(t *testing.T) {
		r, store, _, _ := newTestRepo(t)
		r.store = &stubStore{Storer: store, resetFailedRecords: func(context.Context) error { return errBoom }}
		assert.ErrorIs(t, r.DownloadOnce(ctx), errBoom)
	})

	t.Run("queue lookup error is returned", func(t *testing.T) {
		r, store, _, _ := newTestRepo(t)
		r.store = &stubStore{Storer: store, getQueuedRecord: func(context.Context) (*model.Record, error) { return nil, errBoom }}
		assert.ErrorIs(t, r.DownloadOnce(ctx), errBoom)
	})

	t.Run("download error is returned", func(t *testing.T) {
		r, store, _, _ := newTestRepo(t)
		srv := fileServer(t, http.StatusNotFound, "")
		rc := rec("r1", "m1", model.SharedScreenWithGalleryView, now, "", 8)
		rc.DownloadURL = srv.URL + "/video.mp4"
		queue(t, store, "m1", rc)
		assert.ErrorContains(t, r.DownloadOnce(ctx), "download returned error r1")
	})

	t.Run("trashes the meeting once its last record is downloaded", func(t *testing.T) {
		r, store, fc, _ := newTestRepo(t)
		srv := fileServer(t, http.StatusOK, "videodat")
		first := rec("r1", "m1", model.SharedScreenWithGalleryView, now, "", 8)
		first.DownloadURL = srv.URL + "/video.mp4"
		second := rec("r2", "m1", model.ChatFile, now.Add(time.Minute), "", 8)
		second.DownloadURL = srv.URL + "/chat.mp4"
		queue(t, store, "m1", first, second)

		require.NoError(t, r.DownloadOnce(ctx))
		assert.Empty(t, fc.deleteCalls(), "must not trash while a record is still queued")

		require.NoError(t, r.DownloadOnce(ctx))
		assert.Equal(t, []deleteCall{{"m1", false}}, fc.deleteCalls())
	})

	t.Run("keeps the meeting in the cloud when neither trash nor delete is on", func(t *testing.T) {
		r, store, fc, cfg := newTestRepo(t)
		cfg.Client.TrashDownloaded = false
		srv := fileServer(t, http.StatusOK, "videodat")
		rc := rec("r1", "m1", model.SharedScreenWithGalleryView, now, "", 8)
		rc.DownloadURL = srv.URL + "/video.mp4"
		queue(t, store, "m1", rc)

		require.NoError(t, r.DownloadOnce(ctx))
		assert.Empty(t, fc.deleteCalls())
	})

	t.Run("cloud delete error is returned", func(t *testing.T) {
		r, store, fc, _ := newTestRepo(t)
		fc.deleteErr = errBoom
		srv := fileServer(t, http.StatusOK, "videodat")
		rc := rec("r1", "m1", model.SharedScreenWithGalleryView, now, "", 8)
		rc.DownloadURL = srv.URL + "/video.mp4"
		queue(t, store, "m1", rc)
		assert.ErrorIs(t, r.DownloadOnce(ctx), errBoom)
	})
}

func TestMeetingRecordsLoaded_StoreErrorMeansNotLoaded(t *testing.T) {
	r, store, _, _ := newTestRepo(t)
	r.store = &stubStore{Storer: store, getRecords: func(context.Context, string) ([]model.Record, error) { return nil, errBoom }}
	assert.False(t, r.meetingRecordsLoaded(context.Background(), "m1"))
}

func TestDownloadJob(t *testing.T) {
	r, store, _, _ := newTestRepo(t)
	var calls atomic.Int32
	r.store = &stubStore{Storer: store, getQueuedRecord: func(context.Context) (*model.Record, error) {
		// an error keeps the 1s tick; "nothing queued" backs off to 1 minute
		if calls.Add(1) == 1 {
			return nil, errBoom
		}
		return nil, storage.ErrNoRows
	}}
	stop := runJob(t, r.DownloadJob)
	require.Eventually(t, func() bool { return calls.Load() == 2 }, 5*time.Second, 10*time.Millisecond)
	stop()
	assert.Equal(t, int32(2), calls.Load())
}

// instance is a fake zoomrs service answering /meetingsLoaded with the given result
func instance(t *testing.T, status int, body string) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/meetingsLoaded/salt", r.URL.Path)
		var req struct {
			Meetings []string `json:"meetings"`
		}
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		mu.Lock()
		asked = append(asked, req.Meetings...)
		mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &asked
}

func TestRequestMeetingsLoaded(t *testing.T) {
	t.Run("no instances configured is an error", func(t *testing.T) {
		r, _, _, _ := newTestRepo(t)
		_, err := r.requestMeetingsLoaded([]string{"m1"})
		assert.EqualError(t, err, "no instances configured")
	})

	t.Run("loaded only when every instance says ok", func(t *testing.T) {
		r, _, _, cfg := newTestRepo(t)
		ok1, asked := instance(t, http.StatusOK, `{"result":"ok"}`)
		ok2, _ := instance(t, http.StatusOK, `{"result":"ok"}`)
		cfg.Commander.Instances = []string{ok1.URL, ok2.URL}
		loaded, err := r.requestMeetingsLoaded([]string{"m1", "m2"})
		require.NoError(t, err)
		assert.True(t, loaded)
		assert.Equal(t, []string{"m1", "m2"}, *asked)

		pending, _ := instance(t, http.StatusOK, `{"result":"pending"}`)
		cfg.Commander.Instances = []string{ok1.URL, pending.URL}
		loaded, err = r.requestMeetingsLoaded([]string{"m1"})
		require.NoError(t, err)
		assert.False(t, loaded)
	})

	t.Run("instance errors", func(t *testing.T) {
		r, _, _, cfg := newTestRepo(t)
		bad, _ := instance(t, http.StatusForbidden, "")
		cfg.Commander.Instances = []string{bad.URL}
		_, err := r.requestMeetingsLoaded([]string{"m1"})
		assert.ErrorContains(t, err, "status 403")

		garbled, _ := instance(t, http.StatusOK, "not json")
		cfg.Commander.Instances = []string{garbled.URL}
		_, err = r.requestMeetingsLoaded([]string{"m1"})
		assert.ErrorContains(t, err, "failed to decode response body")

		down := httptest.NewServer(http.NotFoundHandler())
		down.Close()
		cfg.Commander.Instances = []string{down.URL}
		_, err = r.requestMeetingsLoaded([]string{"m1"})
		assert.ErrorContains(t, err, "failed to post meetingsLoaded")
	})

	t.Run("an instance that never answers is an error, not a hang", func(t *testing.T) {
		r, _, _, cfg := newTestRepo(t)
		release := make(chan struct{})
		stalled := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
		t.Cleanup(stalled.Close)
		t.Cleanup(func() { close(release) }) // runs before Close, which waits for the handler
		cfg.Commander.Instances = []string{stalled.URL}
		r.httpClient = &http.Client{Timeout: 50 * time.Millisecond}

		done := make(chan error, 1)
		go func() {
			_, err := r.requestMeetingsLoaded([]string{"m1"})
			done <- err
		}()
		select {
		case err := <-done:
			assert.ErrorContains(t, err, "failed to post meetingsLoaded to "+stalled.URL)
		case <-time.After(5 * time.Second):
			t.Fatal("requestMeetingsLoaded is still waiting for the instance")
		}
	})

	t.Run("each response body is closed before the next instance is asked", func(t *testing.T) {
		r, _, _, cfg := newTestRepo(t)
		ok1, _ := instance(t, http.StatusOK, `{"result":"ok"}`)
		ok2, _ := instance(t, http.StatusOK, `{"result":"ok"}`)
		ok3, _ := instance(t, http.StatusOK, `{"result":"ok"}`)
		bad, _ := instance(t, http.StatusForbidden, "")
		garbled, _ := instance(t, http.StatusOK, "not json")

		for name, last := range map[string]*httptest.Server{"ok": ok3, "non-200": bad, "garbled body": garbled} {
			tr := &bodyTrackingTransport{}
			r.httpClient = &http.Client{Transport: tr}
			cfg.Commander.Instances = []string{ok1.URL, ok2.URL, last.URL}
			_, _ = r.requestMeetingsLoaded([]string{"m1"})
			assert.Equal(t, int32(3), tr.requests.Load(), name)
			assert.Equal(t, int32(1), tr.maxOpen.Load(), "%s: bodies open at once", name)
			assert.Equal(t, int32(0), tr.open.Load(), "%s: bodies left open", name)
		}
	})
}

// bodyTrackingTransport counts response bodies that are open at the same time
type bodyTrackingTransport struct {
	requests, open, maxOpen atomic.Int32
}

func (tr *bodyTrackingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	tr.requests.Add(1)
	if open := tr.open.Add(1); open > tr.maxOpen.Load() {
		tr.maxOpen.Store(open)
	}
	resp.Body = &trackedBody{ReadCloser: resp.Body, tr: tr}
	return resp, nil
}

type trackedBody struct {
	io.ReadCloser
	tr   *bodyTrackingTransport
	once sync.Once
}

func (b *trackedBody) Close() error {
	b.once.Do(func() { b.tr.open.Add(-1) })
	return b.ReadCloser.Close()
}

func TestCleanupJob(t *testing.T) {
	now := time.Now()
	meetings := []model.Meeting{mtg("m1", now, 10), mtg("m2", now, 10)}

	t.Run("deletes the day's meetings once instances confirm they are loaded", func(t *testing.T) {
		r, _, fc, cfg := newTestRepo(t)
		fc.meetings = meetings
		fc.deleteErr = nil
		cfg.Client.DeleteDownloaded = true
		ok, _ := instance(t, http.StatusOK, `{"result":"ok"}`)
		cfg.Commander.Instances = []string{ok.URL}

		r.CleanupJob(context.Background(), 2, false)
		assert.Equal(t, []int{2}, fc.daysAgo)
		assert.Equal(t, []deleteCall{{"m1", true}, {"m2", true}}, fc.deleteCalls())
	})

	t.Run("keeps meetings an instance is still loading", func(t *testing.T) {
		r, _, fc, cfg := newTestRepo(t)
		fc.meetings = meetings
		pending, _ := instance(t, http.StatusOK, `{"result":"pending"}`)
		cfg.Commander.Instances = []string{pending.URL}

		r.CleanupJob(context.Background(), 2, false)
		assert.Empty(t, fc.deleteCalls())
	})

	t.Run("force deletes without confirmation", func(t *testing.T) {
		r, _, fc, cfg := newTestRepo(t)
		fc.meetings = meetings
		fc.deleteErr = errBoom // failures are counted and logged, the loop goes on
		pending, _ := instance(t, http.StatusOK, `{"result":"pending"}`)
		cfg.Commander.Instances = []string{pending.URL}

		r.CleanupJob(context.Background(), 2, true)
		assert.Len(t, fc.deleteCalls(), 2)
	})

	t.Run("nothing to clean up", func(t *testing.T) {
		r, _, fc, _ := newTestRepo(t)
		r.CleanupJob(context.Background(), 2, false)
		assert.Equal(t, []int{2}, fc.daysAgo)
		assert.Empty(t, fc.deleteCalls())
	})

	t.Run("listing error waits for a retry until canceled", func(t *testing.T) {
		r, _, fc, _ := newTestRepo(t)
		fc.meetingsErr = errBoom
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		r.CleanupJob(ctx, 2, false)
		assert.Empty(t, fc.deleteCalls())
	})

	t.Run("instance error waits for a retry until canceled", func(t *testing.T) {
		r, _, fc, _ := newTestRepo(t)
		fc.meetings = meetings // no instances configured, so the confirmation step fails
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		r.CleanupJob(ctx, 2, false)
		assert.Empty(t, fc.deleteCalls())
	})

	t.Run("instance error with an already canceled context returns at once", func(t *testing.T) {
		r, _, fc, _ := newTestRepo(t)
		fc.meetings = meetings
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		r.CleanupJob(ctx, 2, false)
		assert.Empty(t, fc.deleteCalls())
	})

	t.Run("cancel between deletes stops the loop", func(t *testing.T) {
		r, _, fc, cfg := newTestRepo(t)
		fc.meetings = meetings
		cfg.Client.RateLimitingDelay.Light = time.Hour
		// force still asks the instances, it just ignores a "pending" answer
		pending, _ := instance(t, http.StatusOK, `{"result":"pending"}`)
		cfg.Commander.Instances = []string{pending.URL}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		fc.onDelete = cancel
		r.CleanupJob(ctx, 2, true)
		assert.Len(t, fc.deleteCalls(), 1)
	})
}

func TestCheckConsistency(t *testing.T) {
	now := time.Now()
	ctx := context.Background()

	t.Run("reports missing, empty and wrong-size files", func(t *testing.T) {
		r, store, _, cfg := newTestRepo(t)
		dir := cfg.Storage.Repository
		files := map[string]string{"good": "1234", "empty": "", "wrong": "12"}
		for id, body := range files {
			require.NoError(t, os.WriteFile(filepath.Join(dir, id), []byte(body), 0o600))
		}
		var recs []model.Record
		for _, id := range []string{"good", "empty", "wrong", "missing"} {
			rc := rec(id, "m1", model.SharedScreenWithGalleryView, now, model.StatusDownloaded, 4)
			rc.FilePath = filepath.Join(dir, id)
			recs = append(recs, rc)
		}
		// records in other states are not checked
		recs = append(recs, rec("queued", "m1", model.ChatFile, now, model.StatusQueued, 4))
		require.NoError(t, store.SaveMeeting(ctx, mtg("m1", now, 10, recs...)))

		checked, err := r.CheckConsistency(ctx)
		assert.Equal(t, 4, checked)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "file does not exist: "+filepath.Join(dir, "missing"))
		assert.Contains(t, err.Error(), "file is empty: "+filepath.Join(dir, "empty"))
		assert.Contains(t, err.Error(), "file size does not match: "+filepath.Join(dir, "wrong"))
		assert.NotContains(t, err.Error(), filepath.Join(dir, "good"))
	})

	t.Run("all good", func(t *testing.T) {
		r, store, _, cfg := newTestRepo(t)
		rc := rec("good", "m1", model.SharedScreenWithGalleryView, now, model.StatusDownloaded, 4)
		rc.FilePath = filepath.Join(cfg.Storage.Repository, "good")
		require.NoError(t, os.WriteFile(rc.FilePath, []byte("1234"), 0o600))
		require.NoError(t, store.SaveMeeting(ctx, mtg("m1", now, 10, rc)))

		checked, err := r.CheckConsistency(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 1, checked)
	})

	t.Run("store error", func(t *testing.T) {
		r, store, _, _ := newTestRepo(t)
		r.store = &stubStore{Storer: store, getRecordsByStatus: func(context.Context, model.RecordStatus) ([]model.Record, error) {
			return nil, errBoom
		}}
		_, err := r.CheckConsistency(ctx)
		assert.ErrorIs(t, err, errBoom)
	})
}

// freeSequence returns a diskFree fake that reports the given values in order,
// repeating the last one once they run out
func freeSequence(values ...uint64) (func(string) (uint64, error), *atomic.Int32) {
	var calls atomic.Int32
	return func(string) (uint64, error) {
		i := int(calls.Add(1)) - 1
		if i >= len(values) {
			i = len(values) - 1
		}
		return values[i], nil
	}, &calls
}

// seedDownloaded saves downloaded records a day apart (oldest first) plus one extra record
// on the newest day, and creates their folders the way DownloadRecord lays them out
func seedDownloaded(t *testing.T, store storage.Storer, repoDir string) []model.Record {
	t.Helper()
	ctx := context.Background()
	base := time.Now().Add(-72 * time.Hour)
	recs := []model.Record{
		rec("r1", "m1", model.SharedScreenWithGalleryView, base, model.StatusDownloaded, 4),
		rec("r2", "m1", model.SharedScreenWithGalleryView, base.Add(24*time.Hour), model.StatusDownloaded, 4),
		rec("r3", "m1", model.SharedScreenWithGalleryView, base.Add(48*time.Hour), model.StatusDownloaded, 4),
		rec("r4", "m1", model.ChatFile, base.Add(48*time.Hour+time.Minute), model.StatusDownloaded, 4),
	}
	require.NoError(t, store.SaveMeeting(ctx, mtg("m1", base, 10, recs...)))
	saved, err := store.GetRecordsByStatus(ctx, model.StatusDownloaded)
	require.NoError(t, err)
	for _, rc := range saved {
		recFolder, _ := rc.Paths(repoDir)
		require.NoError(t, os.MkdirAll(recFolder, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(recFolder, rc.Id+".mp4"), []byte("data"), 0o600))
	}
	return saved
}

func TestFreeUpSpace(t *testing.T) {
	ctx := context.Background()

	t.Run("enough free space deletes nothing", func(t *testing.T) {
		r, store, _, cfg := newTestRepo(t)
		cfg.Storage.KeepFreeSpace = 100
		saved := seedDownloaded(t, store, cfg.Storage.Repository)
		var calls *atomic.Int32
		r.diskFree, calls = freeSequence(101)

		deleted, err := r.freeUpSpace(ctx)
		require.NoError(t, err)
		assert.Equal(t, 0, deleted)
		assert.Equal(t, int32(1), calls.Load())
		for _, rc := range saved {
			recFolder, _ := rc.Paths(cfg.Storage.Repository)
			assert.DirExists(t, recFolder)
		}
	})

	t.Run("deletes oldest records until there is enough space", func(t *testing.T) {
		r, store, _, cfg := newTestRepo(t)
		cfg.Storage.KeepFreeSpace = 100
		saved := seedDownloaded(t, store, cfg.Storage.Repository)
		// initial check, then one reading before each record; the third reading is enough
		r.diskFree, _ = freeSequence(100, 100, 100, 101)

		deleted, err := r.freeUpSpace(ctx)
		require.NoError(t, err)
		assert.Equal(t, 2, deleted)

		for i, rc := range saved {
			recFolder, dateFolder := rc.Paths(cfg.Storage.Repository)
			if i < 2 {
				assert.NoDirExists(t, recFolder)
				assert.NoDirExists(t, dateFolder, "an emptied date folder is removed")
				assert.Equal(t, model.StatusDeleted, recordStatus(t, store, "m1", rc.Id).Status)
			} else {
				assert.DirExists(t, recFolder)
				assert.Equal(t, model.StatusDownloaded, recordStatus(t, store, "m1", rc.Id).Status)
			}
		}
	})

	t.Run("keeps a date folder that still has recordings", func(t *testing.T) {
		r, store, _, cfg := newTestRepo(t)
		cfg.Storage.KeepFreeSpace = 100
		saved := seedDownloaded(t, store, cfg.Storage.Repository)
		// r3 and r4 share the newest day: deleting r3 alone must leave that day's folder
		r.diskFree, _ = freeSequence(0, 0, 0, 0, 101)

		deleted, err := r.freeUpSpace(ctx)
		require.NoError(t, err)
		assert.Equal(t, 3, deleted)
		r4Folder, dateFolder := saved[3].Paths(cfg.Storage.Repository)
		assert.DirExists(t, dateFolder)
		assert.DirExists(t, r4Folder)
	})

	t.Run("skips records whose folder is already gone", func(t *testing.T) {
		r, store, _, cfg := newTestRepo(t)
		cfg.Storage.KeepFreeSpace = 100
		saved := seedDownloaded(t, store, cfg.Storage.Repository)
		recFolder, _ := saved[0].Paths(cfg.Storage.Repository)
		require.NoError(t, os.RemoveAll(recFolder))
		r.diskFree, _ = freeSequence(0, 0, 0, 101)

		deleted, err := r.freeUpSpace(ctx)
		require.NoError(t, err)
		assert.Equal(t, 1, deleted)
		assert.Equal(t, model.StatusDownloaded, recordStatus(t, store, "m1", "r1").Status, "skipped record keeps its status")
		assert.Equal(t, model.StatusDeleted, recordStatus(t, store, "m1", "r2").Status)
	})

	t.Run("disk errors", func(t *testing.T) {
		r, store, _, cfg := newTestRepo(t)
		cfg.Storage.KeepFreeSpace = 100
		seedDownloaded(t, store, cfg.Storage.Repository)

		r.diskFree = func(string) (uint64, error) { return 0, errBoom }
		_, err := r.freeUpSpace(ctx)
		assert.ErrorIs(t, err, errBoom)

		var calls atomic.Int32
		r.diskFree = func(string) (uint64, error) {
			if calls.Add(1) > 2 {
				return 0, errBoom
			}
			return 0, nil
		}
		deleted, err := r.freeUpSpace(ctx)
		assert.ErrorIs(t, err, errBoom)
		assert.Equal(t, 1, deleted)
	})

	t.Run("store errors", func(t *testing.T) {
		r, store, _, cfg := newTestRepo(t)
		cfg.Storage.KeepFreeSpace = 100
		seedDownloaded(t, store, cfg.Storage.Repository)
		r.diskFree, _ = freeSequence(0, 0, 101)
		r.store = &stubStore{Storer: store, updateRecord: func(context.Context, string, model.RecordStatus, string) error { return errBoom }}

		// a failed status update is only logged; the folder is still gone
		deleted, err := r.freeUpSpace(ctx)
		require.NoError(t, err)
		assert.Equal(t, 1, deleted)

		r.diskFree, _ = freeSequence(0)
		r.store = &stubStore{Storer: store, getRecordsByStatus: func(context.Context, model.RecordStatus) ([]model.Record, error) {
			return nil, errBoom
		}}
		_, err = r.freeUpSpace(ctx)
		assert.ErrorIs(t, err, errBoom)
	})
}

func TestDiskFree_ReadsTheRealDisk(t *testing.T) {
	free, err := diskFree(t.TempDir())
	require.NoError(t, err)
	assert.Positive(t, free)

	_, err = diskFree(filepath.Join(t.TempDir(), "does-not-exist"))
	assert.Error(t, err)
}

func TestGetStats(t *testing.T) {
	ctx := context.Background()

	t.Run("sums downloaded sizes per day", func(t *testing.T) {
		r, store, _, _ := newTestRepo(t)
		day1 := time.Date(2024, 3, 1, 10, 0, 0, 0, time.Local)
		day2 := time.Date(2024, 3, 2, 10, 0, 0, 0, time.Local)
		require.NoError(t, store.SaveMeeting(ctx, mtg("m1", day1, 10,
			rec("a", "m1", model.SharedScreenWithGalleryView, day1, model.StatusDownloaded, 3<<30),
			rec("b", "m1", model.ChatFile, day1, model.StatusDownloaded, 1<<30),
			rec("c", "m1", model.SharedScreenWithGalleryView, day2, model.StatusDownloaded, 2<<20),
			rec("q", "m1", model.SharedScreenWithGalleryView, day2, model.StatusQueued, 1<<30),
		)))

		cases := map[rune]map[string]int64{
			'G': {"2024-03-01": 4, "2024-03-02": 0},
			'M': {"2024-03-01": 4 << 10, "2024-03-02": 2},
			'K': {"2024-03-01": 4 << 20, "2024-03-02": 2 << 10},
			'X': {"2024-03-01": 4 << 30, "2024-03-02": 2 << 20}, // unknown divider means bytes
		}
		for d, want := range cases {
			got, err := r.GetStats(ctx, d)
			require.NoError(t, err)
			assert.Equal(t, want, got, "divider %c", d)
		}
	})

	t.Run("no downloaded records is an empty map", func(t *testing.T) {
		// sqlite answers an empty result with a nil slice, which must not be read as a failure
		r, _, _, _ := newTestRepo(t)
		stats, err := r.GetStats(ctx, 'K')
		require.NoError(t, err)
		assert.NotNil(t, stats)
		assert.Empty(t, stats)
	})

	t.Run("store error wins over rows returned with it", func(t *testing.T) {
		r, store, _, _ := newTestRepo(t)
		r.store = &stubStore{Storer: store, getRecordsByStatus: func(context.Context, model.RecordStatus) ([]model.Record, error) {
			return []model.Record{{Id: "a", DateTime: "2024-03-01 10:00:00", FileSize: 1}}, errBoom
		}}
		stats, err := r.GetStats(ctx, 'K')
		assert.ErrorIs(t, err, errBoom)
		assert.Nil(t, stats)
	})

	t.Run("store error", func(t *testing.T) {
		r, store, _, _ := newTestRepo(t)
		r.store = &stubStore{Storer: store, getRecordsByStatus: func(context.Context, model.RecordStatus) ([]model.Record, error) {
			return nil, errBoom
		}}
		_, err := r.GetStats(ctx, 'K')
		assert.ErrorIs(t, err, errBoom)
	})
}
