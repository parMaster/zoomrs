package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/parMaster/zoomrs/config"
	"github.com/parMaster/zoomrs/storage/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeZoom is an in-process stand-in for the Zoom OAuth and REST APIs.
// Each test sets only the handlers it needs; unset routes answer 404.
type fakeZoom struct {
	t          *testing.T
	srv        *httptest.Server
	tokenCalls atomic.Int32

	token      http.HandlerFunc
	recordings http.HandlerFunc
	report     http.HandlerFunc
	delete     http.HandlerFunc
}

func newFakeZoom(t *testing.T) *fakeZoom {
	t.Helper()
	f := &fakeZoom{t: t}
	f.token = func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{"access_token": "tok", "expires_in": 3600, "token_type": "bearer"})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /oauth/token", func(w http.ResponseWriter, r *http.Request) {
		f.tokenCalls.Add(1)
		f.token(w, r)
	})
	mux.HandleFunc("GET /users/me/recordings", func(w http.ResponseWriter, r *http.Request) { serveOr404(w, r, f.recordings) })
	mux.HandleFunc("GET /report/cloud_recording", func(w http.ResponseWriter, r *http.Request) { serveOr404(w, r, f.report) })
	mux.HandleFunc("DELETE /meetings/{id}/recordings", func(w http.ResponseWriter, r *http.Request) { serveOr404(w, r, f.delete) })
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func serveOr404(w http.ResponseWriter, r *http.Request, h http.HandlerFunc) {
	if h == nil {
		http.NotFound(w, r)
		return
	}
	h(w, r)
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	assert.NoError(t, json.NewEncoder(w).Encode(v))
}

func testClientConfig() config.Client {
	return config.Client{
		AccountId:       "acc",
		Id:              "id",
		Secret:          "secret",
		TrashDownloaded: true,
		RateLimitingDelay: config.RateLimitingDelay{
			Light:  time.Millisecond,
			Medium: time.Millisecond,
			Heavy:  time.Millisecond,
		},
	}
}

func (f *fakeZoom) client(cfg config.Client) *ZoomClient {
	return NewZoomClient(cfg, WithBaseURLs(f.srv.URL, f.srv.URL))
}

func meeting(uuid string, start time.Time, sizes ...model.FileSize) model.Meeting {
	m := model.Meeting{UUID: uuid, Topic: "topic " + uuid, StartTime: start}
	for i, s := range sizes {
		m.Records = append(m.Records, model.Record{Id: uuid + "-" + string(rune('a'+i)), MeetingId: uuid, FileSize: s})
	}
	return m
}

func TestNewZoomClient_DefaultURLs(t *testing.T) {
	z := NewZoomClient(testClientConfig())
	assert.Equal(t, "https://zoom.us", z.authURL)
	assert.Equal(t, "https://api.zoom.us/v2", z.apiURL)
}

func TestAuthorize(t *testing.T) {
	t.Run("sends account credentials and stores the token", func(t *testing.T) {
		f := newFakeZoom(t)
		f.token = func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			assert.True(t, ok)
			assert.Equal(t, "id", user)
			assert.Equal(t, "secret", pass)
			assert.NoError(t, r.ParseForm())
			assert.Equal(t, "account_credentials", r.Form.Get("grant_type"))
			assert.Equal(t, "acc", r.Form.Get("account_id"))
			writeJSON(t, w, map[string]any{"access_token": "tok", "expires_in": 3600})
		}
		z := f.client(testClientConfig())

		require.NoError(t, z.Authorize())
		assert.Equal(t, "tok", z.token.AccessToken)
		// expiry is pulled 5 minutes early so a token is never used right at its deadline
		assert.WithinDuration(t, time.Now().Add(55*time.Minute), z.token.ExpiresAt, 5*time.Second)
	})

	t.Run("expiry is wall-clock time, so it still holds after a suspend", func(t *testing.T) {
		f := newFakeZoom(t)
		z := f.client(testClientConfig())
		require.NoError(t, z.Authorize())
		// a time with a monotonic reading prints it as "m=+..."
		assert.NotContains(t, z.token.ExpiresAt.String(), "m=")
	})

	t.Run("non-200 is an error", func(t *testing.T) {
		f := newFakeZoom(t)
		f.token = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }
		err := f.client(testClientConfig()).Authorize()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "status 401")
	})

	t.Run("malformed body is an error", func(t *testing.T) {
		f := newFakeZoom(t)
		f.token = func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{not json")) }
		assert.Error(t, f.client(testClientConfig()).Authorize())
	})

	t.Run("unreachable server is an error", func(t *testing.T) {
		f := newFakeZoom(t)
		z := f.client(testClientConfig())
		f.srv.Close()
		assert.Error(t, z.Authorize())
	})
}

func TestGetToken(t *testing.T) {
	t.Run("reuses a valid token", func(t *testing.T) {
		f := newFakeZoom(t)
		z := f.client(testClientConfig())

		tok1, err := z.GetToken()
		require.NoError(t, err)
		tok2, err := z.GetToken()
		require.NoError(t, err)
		assert.Same(t, tok1, tok2)
		assert.Equal(t, int32(1), f.tokenCalls.Load())
	})

	t.Run("re-authorizes an expired token", func(t *testing.T) {
		f := newFakeZoom(t)
		z := f.client(testClientConfig())

		_, err := z.GetToken()
		require.NoError(t, err)
		z.token.ExpiresAt = time.Now().Add(-time.Second)
		_, err = z.GetToken()
		require.NoError(t, err)
		assert.Equal(t, int32(2), f.tokenCalls.Load())
	})

	t.Run("returns nil token on auth failure", func(t *testing.T) {
		f := newFakeZoom(t)
		f.token = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadRequest) }
		tok, err := f.client(testClientConfig()).GetToken()
		assert.Error(t, err)
		assert.Nil(t, tok)
	})

	t.Run("concurrent callers authorize once", func(t *testing.T) {
		f := newFakeZoom(t)
		f.token = func(w http.ResponseWriter, _ *http.Request) {
			// slow enough that every caller arrives before the first token is stored
			time.Sleep(20 * time.Millisecond)
			writeJSON(t, w, map[string]any{"access_token": "tok", "expires_in": 3600})
		}
		z := f.client(testClientConfig())

		start := make(chan struct{})
		var wg sync.WaitGroup
		for range 20 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				tok, err := z.GetToken()
				if assert.NoError(t, err) {
					assert.Equal(t, "tok", tok.AccessToken)
				}
			}()
		}
		close(start)
		wg.Wait()

		assert.Equal(t, int32(1), f.tokenCalls.Load())
	})

	t.Run("refresh does not disturb callers using the token", func(t *testing.T) {
		f := newFakeZoom(t)
		// under 300 seconds the token is expired on arrival, so every call refreshes
		f.token = func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(t, w, map[string]any{"access_token": "tok", "expires_in": 1})
		}
		f.recordings = func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
			writeJSON(t, w, model.Recordings{})
		}
		z := f.client(testClientConfig())

		var wg sync.WaitGroup
		for range 20 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := z.GetIntervalMeetings(context.Background(), time.Now(), time.Now())
				assert.NoError(t, err)
			}()
		}
		wg.Wait()
	})

	t.Run("failed refresh keeps the previous token", func(t *testing.T) {
		f := newFakeZoom(t)
		var broken atomic.Bool
		f.token = func(w http.ResponseWriter, _ *http.Request) {
			if broken.Load() {
				_, _ = w.Write([]byte(`{"access_token":"half","expires_in":"soon"}`))
				return
			}
			writeJSON(t, w, map[string]any{"access_token": "tok", "expires_in": 3600})
		}
		z := f.client(testClientConfig())

		_, err := z.GetToken()
		require.NoError(t, err)
		z.token.ExpiresAt = time.Now().Add(-time.Second)
		broken.Store(true)

		tok, err := z.GetToken()
		assert.Error(t, err)
		assert.Nil(t, tok)
		assert.Equal(t, "tok", z.token.AccessToken)
	})
}

func TestGetIntervalMeetings(t *testing.T) {
	from := time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 5, 10, 0, 0, 0, time.UTC)

	t.Run("follows next_page_token across pages", func(t *testing.T) {
		f := newFakeZoom(t)
		var calls atomic.Int32
		f.recordings = func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
			assert.Equal(t, "2024-01-02", r.URL.Query().Get("from"))
			assert.Equal(t, "2024-01-05", r.URL.Query().Get("to"))
			assert.Equal(t, "300", r.URL.Query().Get("page_size"))
			switch calls.Add(1) {
			case 1:
				assert.Empty(t, r.URL.Query().Get("next_page_token"))
				writeJSON(t, w, model.Recordings{NextPageToken: "page2", Meetings: []model.Meeting{meeting("m1", from, 10)}})
			default:
				assert.Equal(t, "page2", r.URL.Query().Get("next_page_token"))
				writeJSON(t, w, model.Recordings{Meetings: []model.Meeting{meeting("m2", from, 20)}})
			}
		}

		got, err := f.client(testClientConfig()).GetIntervalMeetings(context.Background(), from, to)
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "m1", got[0].UUID)
		assert.Equal(t, "m2", got[1].UUID)
		assert.Equal(t, model.FileSize(20), got[1].Records[0].FileSize)
		assert.Equal(t, int32(2), calls.Load())
	})

	t.Run("canceled context stops paging", func(t *testing.T) {
		f := newFakeZoom(t)
		f.recordings = func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(t, w, model.Recordings{NextPageToken: "more", Meetings: []model.Meeting{meeting("m1", from)}})
		}
		cfg := testClientConfig()
		cfg.RateLimitingDelay.Medium = time.Hour
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		got, err := f.client(cfg).GetIntervalMeetings(ctx, from, to)
		assert.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, got)
	})

	t.Run("non-200 is an error", func(t *testing.T) {
		f := newFakeZoom(t)
		f.recordings = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTooManyRequests) }
		_, err := f.client(testClientConfig()).GetIntervalMeetings(context.Background(), from, to)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "status 429")
	})

	t.Run("malformed body is an error", func(t *testing.T) {
		f := newFakeZoom(t)
		f.recordings = func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("[")) }
		_, err := f.client(testClientConfig()).GetIntervalMeetings(context.Background(), from, to)
		assert.ErrorContains(t, err, "failed to unmarshal recordings")
	})

	t.Run("token failure is an error", func(t *testing.T) {
		f := newFakeZoom(t)
		f.token = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }
		_, err := f.client(testClientConfig()).GetIntervalMeetings(context.Background(), from, to)
		assert.ErrorContains(t, err, "unable to get token")
	})

	t.Run("unreachable server is an error", func(t *testing.T) {
		f := newFakeZoom(t)
		z := f.client(testClientConfig())
		_, err := z.GetToken()
		require.NoError(t, err)
		f.srv.Close()
		_, err = z.GetIntervalMeetings(context.Background(), from, to)
		assert.Error(t, err)
	})
}

func TestGetMeetings_AsksForOneDay(t *testing.T) {
	f := newFakeZoom(t)
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	f.recordings = func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, yesterday, r.URL.Query().Get("from"))
		assert.Equal(t, yesterday, r.URL.Query().Get("to"))
		writeJSON(t, w, model.Recordings{Meetings: []model.Meeting{meeting("m1", time.Now())}})
	}
	got, err := f.client(testClientConfig()).GetMeetings(context.Background(), 1)
	require.NoError(t, err)
	assert.Len(t, got, 1)
}

// windows serves one 30-day window per call, in order; calls past the end get an empty window
func windows(t *testing.T, pages ...[]model.Meeting) (http.HandlerFunc, *atomic.Int32) {
	var calls atomic.Int32
	return func(w http.ResponseWriter, _ *http.Request) {
		i := int(calls.Add(1)) - 1
		var ms []model.Meeting
		if i < len(pages) {
			ms = pages[i]
		}
		writeJSON(t, w, model.Recordings{Meetings: ms})
	}, &calls
}

func TestGetAllMeetings(t *testing.T) {
	t.Run("stops after two empty windows in a row", func(t *testing.T) {
		f := newFakeZoom(t)
		now := time.Now()
		var calls *atomic.Int32
		// a single empty window between two non-empty ones must not stop the walk
		f.recordings, calls = windows(t,
			[]model.Meeting{meeting("m1", now)},
			nil,
			[]model.Meeting{meeting("m2", now)},
		)

		got, err := f.client(testClientConfig()).GetAllMeetings(context.Background())
		require.NoError(t, err)
		assert.Len(t, got, 2)
		assert.Equal(t, int32(5), calls.Load())
	})

	t.Run("walks back in 30-day windows", func(t *testing.T) {
		f := newFakeZoom(t)
		var mu sync.Mutex
		var froms []string
		f.recordings = func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			froms = append(froms, r.URL.Query().Get("from"))
			mu.Unlock()
			writeJSON(t, w, model.Recordings{})
		}
		_, err := f.client(testClientConfig()).GetAllMeetings(context.Background())
		require.NoError(t, err)
		assert.Equal(t, []string{
			time.Now().AddDate(0, 0, -30).Format("2006-01-02"),
			time.Now().AddDate(0, 0, -60).Format("2006-01-02"),
		}, froms)
	})

	t.Run("an interval error aborts the walk", func(t *testing.T) {
		f := newFakeZoom(t)
		f.recordings = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }
		got, err := f.client(testClientConfig()).GetAllMeetings(context.Background())
		assert.ErrorContains(t, err, "unable to get interval meetings")
		assert.Nil(t, got)
	})
}

func TestGetAllMeetingsWithRetry(t *testing.T) {
	t.Run("retries after a failure", func(t *testing.T) {
		f := newFakeZoom(t)
		var calls atomic.Int32
		f.recordings = func(w http.ResponseWriter, _ *http.Request) {
			// the first attempt fails; the retry after it (0s delay on the first retry) succeeds
			if calls.Add(1) == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			writeJSON(t, w, model.Recordings{})
		}
		got, err := f.client(testClientConfig()).GetAllMeetingsWithRetry(context.Background())
		require.NoError(t, err)
		assert.Empty(t, got)
		assert.Equal(t, int32(3), calls.Load()) // 1 failed + 2 empty windows
	})

	t.Run("gives up when the context is canceled", func(t *testing.T) {
		f := newFakeZoom(t)
		f.recordings = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		got, err := f.client(testClientConfig()).GetAllMeetingsWithRetry(ctx)
		assert.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, got)
	})
}

func TestGetCloudStorageReport(t *testing.T) {
	t.Run("parses the report", func(t *testing.T) {
		f := newFakeZoom(t)
		f.report = func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
			assert.Equal(t, "2024-01-01", r.URL.Query().Get("from"))
			assert.Equal(t, "2024-01-07", r.URL.Query().Get("to"))
			_, _ = w.Write([]byte(`{"from":"2024-01-01","to":"2024-01-07","cloud_recording_storage":[
				{"date":"2024-01-07","usage":"1 GB","plan_usage":"0","free_usage":"10 GB"}]}`))
		}
		rep, err := f.client(testClientConfig()).GetCloudStorageReport("2024-01-01", "2024-01-07")
		require.NoError(t, err)
		require.Len(t, rep.CloudRecordingStorage, 1)
		assert.Equal(t, model.FileSize(1<<30), rep.CloudRecordingStorage[0].Usage)
		assert.Equal(t, model.FileSize(10<<30), rep.CloudRecordingStorage[0].FreeUsage)
	})

	t.Run("non-200 is an error", func(t *testing.T) {
		f := newFakeZoom(t)
		f.report = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadRequest) }
		_, err := f.client(testClientConfig()).GetCloudStorageReport("a", "b")
		assert.ErrorContains(t, err, "status 400")
	})

	t.Run("malformed body is an error", func(t *testing.T) {
		f := newFakeZoom(t)
		f.report = func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("nope")) }
		_, err := f.client(testClientConfig()).GetCloudStorageReport("a", "b")
		assert.Error(t, err)
	})

	t.Run("token failure is an error", func(t *testing.T) {
		f := newFakeZoom(t)
		f.token = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }
		_, err := f.client(testClientConfig()).GetCloudStorageReport("a", "b")
		assert.ErrorContains(t, err, "unable to get token")
	})

	t.Run("unreachable server is an error", func(t *testing.T) {
		f := newFakeZoom(t)
		z := f.client(testClientConfig())
		_, err := z.GetToken()
		require.NoError(t, err)
		f.srv.Close()
		_, err = z.GetCloudStorageReport("a", "b")
		assert.Error(t, err)
	})
}

func TestDeleteMeetingRecordings(t *testing.T) {
	t.Run("refuses when neither delete nor trash is configured", func(t *testing.T) {
		f := newFakeZoom(t)
		cfg := testClientConfig()
		cfg.TrashDownloaded = false
		err := f.client(cfg).DeleteMeetingRecordings("m1", true)
		assert.EqualError(t, err, "both delete_downloaded and trash_downloaded are false")
		assert.Equal(t, int32(0), f.tokenCalls.Load())
	})

	cases := []struct {
		name             string
		deleteDownloaded bool
		deleteArg        bool
		wantAction       string
	}{
		{"trashes by default", false, true, "trash"},
		{"trashes when not asked to delete", true, false, "trash"},
		{"deletes only when asked and configured", true, true, "delete"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeZoom(t)
			var gotAction string
			f.delete = func(w http.ResponseWriter, r *http.Request) {
				gotAction = r.URL.Query().Get("action")
				w.WriteHeader(http.StatusNoContent)
			}
			cfg := testClientConfig()
			cfg.DeleteDownloaded = tc.deleteDownloaded
			require.NoError(t, f.client(cfg).DeleteMeetingRecordings("m1", tc.deleteArg))
			assert.Equal(t, tc.wantAction, gotAction)
		})
	}

	t.Run("double-encodes the meeting UUID", func(t *testing.T) {
		f := newFakeZoom(t)
		uuid := "/ajXp112QmuoKj4854875=="
		var gotPath string
		f.delete = func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.EscapedPath()
			w.WriteHeader(http.StatusNoContent)
		}
		require.NoError(t, f.client(testClientConfig()).DeleteMeetingRecordings(uuid, false))
		assert.Equal(t, "/meetings/"+url.QueryEscape(url.QueryEscape(uuid))+"/recordings", gotPath)
	})

	t.Run("404 means already gone and is not an error", func(t *testing.T) {
		f := newFakeZoom(t)
		f.delete = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }
		assert.NoError(t, f.client(testClientConfig()).DeleteMeetingRecordings("m1", false))
	})

	t.Run("other statuses are errors", func(t *testing.T) {
		f := newFakeZoom(t)
		f.delete = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
		assert.ErrorContains(t, f.client(testClientConfig()).DeleteMeetingRecordings("m1", false), "status 200")
	})

	t.Run("token failure is an error", func(t *testing.T) {
		f := newFakeZoom(t)
		f.token = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }
		assert.ErrorContains(t, f.client(testClientConfig()).DeleteMeetingRecordings("m1", false), "unable to get token")
	})

	t.Run("unreachable server is an error", func(t *testing.T) {
		f := newFakeZoom(t)
		z := f.client(testClientConfig())
		_, err := z.GetToken()
		require.NoError(t, err)
		f.srv.Close()
		assert.Error(t, z.DeleteMeetingRecordings("m1", false))
	})
}

func TestDeleteRecordingsOverCapacity(t *testing.T) {
	now := time.Now()
	// served out of order on purpose: the client must sort newest first before accumulating
	pages := [][]model.Meeting{{
		meeting("old", now.Add(-3*time.Hour), 10),
		meeting("new", now.Add(-1*time.Hour), 6, 4),
		meeting("mid", now.Add(-2*time.Hour), 10),
	}}

	t.Run("zero capacity is an error", func(t *testing.T) {
		f := newFakeZoom(t)
		_, err := f.client(testClientConfig()).DeleteRecordingsOverCapacity(context.Background(), 0)
		assert.EqualError(t, err, "cloud storage capacity is not configured")
	})

	t.Run("deletes the oldest meetings once the newest ones fill the cap", func(t *testing.T) {
		f := newFakeZoom(t)
		f.recordings, _ = windows(t, pages...)
		var mu sync.Mutex
		var deletedIDs []string
		f.delete = func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			deletedIDs = append(deletedIDs, r.PathValue("id"))
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		}

		// new=10 fits in 15; mid brings the total to 20 and old to 30, both over the cap
		deleted, err := f.client(testClientConfig()).DeleteRecordingsOverCapacity(context.Background(), 15)
		require.NoError(t, err)
		assert.Equal(t, 2, deleted)
		assert.Equal(t, []string{"mid", "old"}, deletedIDs)
	})

	t.Run("failed deletes are not counted", func(t *testing.T) {
		f := newFakeZoom(t)
		f.recordings, _ = windows(t, pages...)
		f.delete = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }
		deleted, err := f.client(testClientConfig()).DeleteRecordingsOverCapacity(context.Background(), 15)
		require.NoError(t, err)
		assert.Equal(t, 0, deleted)
	})

	t.Run("stops when the context is canceled between deletes", func(t *testing.T) {
		f := newFakeZoom(t)
		f.recordings, _ = windows(t, pages...)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		f.delete = func(w http.ResponseWriter, _ *http.Request) {
			cancel()
			w.WriteHeader(http.StatusNoContent)
		}
		cfg := testClientConfig()
		cfg.RateLimitingDelay.Light = time.Hour
		deleted, err := f.client(cfg).DeleteRecordingsOverCapacity(ctx, 15)
		assert.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, 1, deleted)
	})

	t.Run("listing failure is an error", func(t *testing.T) {
		f := newFakeZoom(t)
		f.recordings = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := f.client(testClientConfig()).DeleteRecordingsOverCapacity(ctx, 15)
		assert.ErrorContains(t, err, "unable to GetAllMeetingsWithRetry")
	})
}

func TestStatusErrors(t *testing.T) {
	const zoomBody = `{"code":429,"message":"You have reached the maximum per-second rate limit"}`
	fail := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(body))
		}
	}

	t.Run("recordings failure carries Zoom's answer and does not blame authorization", func(t *testing.T) {
		f := newFakeZoom(t)
		f.recordings = fail(zoomBody + "\n")
		_, err := f.client(testClientConfig()).GetIntervalMeetings(context.Background(), time.Now(), time.Now())
		assert.EqualError(t, err, "unable to get recordings, status 429, message: "+zoomBody)
		assert.NotContains(t, err.Error(), "authorize")
	})

	t.Run("storage report failure carries Zoom's answer", func(t *testing.T) {
		f := newFakeZoom(t)
		f.report = fail(zoomBody)
		_, err := f.client(testClientConfig()).GetCloudStorageReport("a", "b")
		assert.EqualError(t, err, "unable to get cloud storage, status 429, message: "+zoomBody)
	})

	t.Run("delete failure carries Zoom's answer", func(t *testing.T) {
		f := newFakeZoom(t)
		f.delete = fail(zoomBody)
		err := f.client(testClientConfig()).DeleteMeetingRecordings("m1", false)
		assert.EqualError(t, err, "unable to delete recordings for meeting id: m1, status 429, message: "+zoomBody)
	})

	t.Run("empty body leaves no dangling message", func(t *testing.T) {
		f := newFakeZoom(t)
		f.report = fail("")
		_, err := f.client(testClientConfig()).GetCloudStorageReport("a", "b")
		assert.EqualError(t, err, "unable to get cloud storage, status 429")
	})

	t.Run("oversized body is cut off", func(t *testing.T) {
		f := newFakeZoom(t)
		f.report = fail(strings.Repeat("x", 10*maxErrorBody))
		_, err := f.client(testClientConfig()).GetCloudStorageReport("a", "b")
		require.Error(t, err)
		assert.Less(t, len(err.Error()), 2*maxErrorBody)
		assert.Contains(t, err.Error(), strings.Repeat("x", maxErrorBody))
	})
}

// numberedTokens makes the fake hand out tok1, tok2, ... so a test can tell a new token from a rejected one
func (f *fakeZoom) numberedTokens() {
	var n atomic.Int32
	f.token = func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(f.t, w, map[string]any{"access_token": fmt.Sprintf("tok%d", n.Add(1)), "expires_in": 3600})
	}
}

// rejecting answers 401 to requests carrying the given token and counts every request it sees
func rejecting(token string, calls *atomic.Int32, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") == "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":124,"message":"Invalid access token."}`))
			return
		}
		h(w, r)
	}
}

func TestUnauthorized(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	t.Run("recordings recover with a new token", func(t *testing.T) {
		f := newFakeZoom(t)
		f.numberedTokens()
		var calls atomic.Int32
		f.recordings = rejecting("tok1", &calls, func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "Bearer tok2", r.Header.Get("Authorization"))
			writeJSON(t, w, model.Recordings{Meetings: []model.Meeting{meeting("m1", now)}})
		})

		got, err := f.client(testClientConfig()).GetIntervalMeetings(ctx, now, now)
		require.NoError(t, err)
		assert.Len(t, got, 1)
		assert.Equal(t, int32(2), calls.Load())
		assert.Equal(t, int32(2), f.tokenCalls.Load())
	})

	t.Run("storage report recovers with a new token", func(t *testing.T) {
		f := newFakeZoom(t)
		f.numberedTokens()
		var calls atomic.Int32
		f.report = rejecting("tok1", &calls, func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "Bearer tok2", r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`{"from":"a","to":"b","cloud_recording_storage":[]}`))
		})

		_, err := f.client(testClientConfig()).GetCloudStorageReport("a", "b")
		require.NoError(t, err)
		assert.Equal(t, int32(2), calls.Load())
		assert.Equal(t, int32(2), f.tokenCalls.Load())
	})

	t.Run("delete recovers with a new token", func(t *testing.T) {
		f := newFakeZoom(t)
		f.numberedTokens()
		var calls atomic.Int32
		f.delete = rejecting("tok1", &calls, func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "Bearer tok2", r.Header.Get("Authorization"))
			assert.Equal(t, "trash", r.URL.Query().Get("action"))
			w.WriteHeader(http.StatusNoContent)
		})

		require.NoError(t, f.client(testClientConfig()).DeleteMeetingRecordings("m1", false))
		assert.Equal(t, int32(2), calls.Load())
		assert.Equal(t, int32(2), f.tokenCalls.Load())
	})

	t.Run("a later page recovers without losing earlier pages", func(t *testing.T) {
		f := newFakeZoom(t)
		f.numberedTokens()
		var tok1Calls atomic.Int32
		f.recordings = func(w http.ResponseWriter, r *http.Request) {
			page := r.URL.Query().Get("next_page_token")
			// the token dies between the first and the second page
			if r.Header.Get("Authorization") == "Bearer tok1" && tok1Calls.Add(1) > 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if page == "" {
				writeJSON(t, w, model.Recordings{NextPageToken: "page2", Meetings: []model.Meeting{meeting("m1", now)}})
				return
			}
			assert.Equal(t, "page2", page)
			assert.Equal(t, "Bearer tok2", r.Header.Get("Authorization"))
			writeJSON(t, w, model.Recordings{Meetings: []model.Meeting{meeting("m2", now)}})
		}

		got, err := f.client(testClientConfig()).GetIntervalMeetings(ctx, now, now)
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "m1", got[0].UUID)
		assert.Equal(t, "m2", got[1].UUID)
		assert.Equal(t, int32(2), f.tokenCalls.Load())
	})

	t.Run("a second 401 is an error and there is no third attempt", func(t *testing.T) {
		f := newFakeZoom(t)
		f.numberedTokens()
		var calls atomic.Int32
		f.report = func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
		}

		_, err := f.client(testClientConfig()).GetCloudStorageReport("a", "b")
		assert.EqualError(t, err, "unable to get cloud storage, status 401")
		assert.Equal(t, int32(2), calls.Load())
		assert.Equal(t, int32(2), f.tokenCalls.Load())
	})

	t.Run("a failed refresh is an error that names the call", func(t *testing.T) {
		f := newFakeZoom(t)
		var issued atomic.Int32
		f.token = func(w http.ResponseWriter, _ *http.Request) {
			if issued.Add(1) > 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			writeJSON(t, w, map[string]any{"access_token": "tok1", "expires_in": 3600})
		}
		var calls atomic.Int32
		f.report = rejecting("tok1", &calls, func(http.ResponseWriter, *http.Request) {})

		_, err := f.client(testClientConfig()).GetCloudStorageReport("a", "b")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unable to get cloud storage, status 401")
		assert.Contains(t, err.Error(), "unable to authorize")
		assert.Equal(t, int32(1), calls.Load())
		assert.Equal(t, int32(2), f.tokenCalls.Load())
	})

	t.Run("concurrent 401s cause one token request", func(t *testing.T) {
		const callers = 10
		f := newFakeZoom(t)
		f.numberedTokens()
		// hold every tok1 request until all callers have sent theirs, so each one sees the 401
		var arrived atomic.Int32
		release := make(chan struct{})
		f.recordings = func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") == "Bearer tok1" {
				if arrived.Add(1) == callers {
					close(release)
				}
				<-release
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			assert.Equal(t, "Bearer tok2", r.Header.Get("Authorization"))
			writeJSON(t, w, model.Recordings{})
		}
		z := f.client(testClientConfig())
		_, err := z.GetToken()
		require.NoError(t, err)

		var wg sync.WaitGroup
		for range callers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := z.GetIntervalMeetings(ctx, now, now)
				assert.NoError(t, err)
			}()
		}
		wg.Wait()

		assert.Equal(t, int32(2), f.tokenCalls.Load())
	})
}

func TestRefreshToken(t *testing.T) {
	t.Run("replaces the rejected token", func(t *testing.T) {
		f := newFakeZoom(t)
		f.numberedTokens()
		z := f.client(testClientConfig())
		old, err := z.GetToken()
		require.NoError(t, err)

		got, err := z.RefreshToken(old)
		require.NoError(t, err)
		assert.Equal(t, "tok2", got.AccessToken)
		assert.Equal(t, int32(2), f.tokenCalls.Load())
	})

	t.Run("returns the current token when someone already replaced it", func(t *testing.T) {
		f := newFakeZoom(t)
		f.numberedTokens()
		z := f.client(testClientConfig())
		old, err := z.GetToken()
		require.NoError(t, err)
		current, err := z.RefreshToken(old)
		require.NoError(t, err)

		got, err := z.RefreshToken(old)
		require.NoError(t, err)
		assert.Same(t, current, got)
		assert.Equal(t, int32(2), f.tokenCalls.Load())
	})

	t.Run("failure returns no token", func(t *testing.T) {
		f := newFakeZoom(t)
		z := f.client(testClientConfig())
		old, err := z.GetToken()
		require.NoError(t, err)
		f.token = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }

		got, err := z.RefreshToken(old)
		assert.Error(t, err)
		assert.Nil(t, got)
	})
}

func TestTokenIsNotLogged(t *testing.T) {
	var buf bytes.Buffer
	out := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(out) })

	f := newFakeZoom(t)
	f.token = func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{"access_token": "very-secret-token", "expires_in": 3600})
	}
	f.recordings = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }

	_, err := f.client(testClientConfig()).GetIntervalMeetings(context.Background(), time.Now(), time.Now())
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "very-secret-token")
	assert.NotContains(t, buf.String(), "very-secret-token")
}
