package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-pkgz/auth/token"
	"github.com/golang-jwt/jwt"
	"github.com/parMaster/mcache/v2"
	"github.com/parMaster/zoomrs/client"
	"github.com/parMaster/zoomrs/config"
	"github.com/parMaster/zoomrs/repo"
	"github.com/parMaster/zoomrs/storage"
	"github.com/parMaster/zoomrs/storage/model"
	"github.com/parMaster/zoomrs/webauth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedMeeting saves one meeting with an MP4 (video) and an M4A (audio) record,
// both already "downloaded", and writes matching files under cfg.Storage.Repository.
// ListMeetings only returns meetings that have an MP4 record, so the video
// record is required to exercise /listMeetings; the pair together gives /check
// and /stats something deterministic to report on.
func seedMeeting(t *testing.T, cfg *config.Parameters, store storage.Storer) {
	t.Helper()

	startTime := time.Now()
	records := []model.Record{
		{
			Id:            "recMP4",
			MeetingId:     "testUUID",
			Type:          model.SharedScreenWithGalleryView,
			StartTime:     startTime,
			FileExtension: "MP4",
			FileSize:      8,
			Status:        model.StatusDownloaded,
			FilePath:      cfg.Storage.Repository + "/recMP4.mp4",
		},
		{
			Id:            "recM4A",
			MeetingId:     "testUUID",
			Type:          model.AudioOnly,
			StartTime:     startTime,
			FileExtension: "M4A",
			FileSize:      4,
			Status:        model.StatusDownloaded,
			FilePath:      cfg.Storage.Repository + "/recM4A.m4a",
		},
	}

	meeting := model.Meeting{
		UUID:      "testUUID",
		Id:        11122223333,
		Topic:     "testTopic",
		StartTime: startTime,
		Records:   records,
	}

	require.NoError(t, store.SaveMeeting(context.Background(), meeting))
	require.NoError(t, os.WriteFile(records[0].FilePath, []byte("videodat"), 0644)) // 8 bytes
	require.NoError(t, os.WriteFile(records[1].FilePath, []byte("aud4"), 0644))     // 4 bytes
}

// fakeZoomAPI answers the two Zoom endpoints the service calls on its own: the OAuth
// token and the cloud recording report
type fakeZoomAPI struct {
	srv          *httptest.Server
	reportCalls  atomic.Int32
	reportStatus atomic.Int32
	report       atomic.Value // string
}

func newFakeZoomAPI(t *testing.T) *fakeZoomAPI {
	t.Helper()
	f := &fakeZoomAPI{}
	f.reportStatus.Store(http.StatusOK)
	f.report.Store(`{"cloud_recording_storage":[
		{"date":"2024-01-06","usage":"1 GB","plan_usage":"0","free_usage":"10 GB"},
		{"date":"2024-01-07","usage":"2 GB","plan_usage":"0","free_usage":"10 GB"}]}`)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /oauth/token", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
	})
	mux.HandleFunc("GET /report/cloud_recording", func(w http.ResponseWriter, _ *http.Request) {
		f.reportCalls.Add(1)
		w.WriteHeader(int(f.reportStatus.Load()))
		_, _ = w.Write([]byte(f.report.Load().(string)))
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// newTestServer builds a fully wired Server (real sqlite storage, real repo,
// real auth service, Zoom client pointed at a fake API) suitable for exercising
// router(ctx) end to end without touching the network.
func newTestServer(t *testing.T) (*Server, context.Context) {
	t.Helper()
	s, ctx, _ := newTestServerWithZoom(t)
	return s, ctx
}

func newTestServerWithZoom(t *testing.T) (*Server, context.Context, *fakeZoomAPI) {
	t.Helper()

	cfg, err := config.NewConfig("../../config/config_example.yml")
	require.NoError(t, err)

	cfg.Server.Dbg = false // serve web assets from the embedded FS, independent of test cwd
	cfg.Storage.Repository = t.TempDir()
	cfg.Storage.Path = "file:" + t.TempDir() + "/router_test.db?mode=rwc&_journal_mode=WAL"

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	var store storage.Storer
	require.NoError(t, LoadStorage(ctx, cfg.Storage, &store))
	seedMeeting(t, cfg, store)

	zoom := newFakeZoomAPI(t)
	zoomClient := client.NewZoomClient(cfg.Client, client.WithBaseURLs(zoom.srv.URL, zoom.srv.URL))
	authService, err := webauth.NewAuthService(cfg.Server)
	require.NoError(t, err)

	srv := &Server{
		cfg:         cfg,
		client:      zoomClient,
		store:       store,
		authService: authService,
		repo:        repo.NewRepository(store, zoomClient, cfg),
		cache:       mcache.NewCache[any](),
	}
	return srv, ctx, zoom
}

// authHeader mints a JWT for one of the configured managers and returns it
// as an "X-JWT" header value. Sending the token via header (rather than the
// JWT cookie) sidesteps the XSRF-cookie dance while still exercising the
// real auth.Middleware().Auth code path.
func authHeader(t *testing.T, s *Server) string {
	t.Helper()
	claims := token.Claims{
		StandardClaims: jwt.StandardClaims{
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
			Issuer:    "zoom-record-service",
		},
		User: &token.User{
			ID:    "google_test-user",
			Name:  "Test Manager",
			Email: s.cfg.Server.Managers[0],
		},
	}
	tkn, err := s.authService.TokenService().Token(claims)
	require.NoError(t, err)
	return tkn
}

func TestRouter_PublicRoutes(t *testing.T) {
	s, ctx := newTestServer(t)
	router := s.router(ctx)

	cases := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{"root redirects anonymous user to login", http.MethodGet, "/", http.StatusFound},
		{"login page", http.MethodGet, "/login", http.StatusOK},
		{"favicon", http.MethodGet, "/favicon.ico", http.StatusOK},
		{"status", http.MethodGet, "/status", http.StatusOK},
		{"watch page with access key", http.MethodGet, "/watch/somekey", http.StatusOK},
		{"watch page without access key is not found", http.MethodGet, "/watch/", http.StatusNotFound}, // {accessKey} wildcard requires a path segment
		{"watchMeeting without uuid query param", http.MethodGet, "/watchMeeting/somekey", http.StatusBadRequest},
		{"meetingsLoaded with wrong access key is forbidden", http.MethodPost, "/meetingsLoaded/wrong-key", http.StatusForbidden},
		{"unknown route", http.MethodGet, "/does-not-exist", http.StatusNotFound},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rw := httptest.NewRecorder()
			router.ServeHTTP(rw, req)
			assert.Equal(t, tc.wantStatus, rw.Code)
		})
	}
}

func TestRouter_ProtectedRoutesRejectAnonymous(t *testing.T) {
	s, ctx := newTestServer(t)
	router := s.router(ctx)

	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"listMeetings", http.MethodGet, "/listMeetings"},
		{"stats with divider", http.MethodGet, "/stats/K"},
		{"stats without divider", http.MethodGet, "/stats/"},
		{"check", http.MethodPost, "/check"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rw := httptest.NewRecorder()
			router.ServeHTTP(rw, req)
			assert.Equal(t, http.StatusUnauthorized, rw.Code)
		})
	}
}

func TestRouter_ProtectedRoutesWithValidToken(t *testing.T) {
	s, ctx := newTestServer(t)
	router := s.router(ctx)
	authTok := authHeader(t, s)

	t.Run("listMeetings returns seeded meeting", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/listMeetings", nil)
		req.Header.Set("X-JWT", authTok)
		rw := httptest.NewRecorder()
		router.ServeHTTP(rw, req)
		require.Equal(t, http.StatusOK, rw.Code)

		var resp struct {
			Data []struct {
				UUID string `json:"uuid"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rw.Body.Bytes(), &resp))
		require.Len(t, resp.Data, 1)
		assert.Equal(t, "testUUID", resp.Data[0].UUID)
	})

	t.Run("check reports both seeded records as consistent", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/check", nil)
		req.Header.Set("X-JWT", authTok)
		rw := httptest.NewRecorder()
		router.ServeHTTP(rw, req)
		require.Equal(t, http.StatusOK, rw.Code)
		assert.Contains(t, rw.Body.String(), `"checked":2`)
	})

	// the router registers both "GET /stats/{divider}" and "GET /stats/" - make
	// sure each pattern is wired to the right divider instead of one shadowing the other.
	t.Run("stats/{divider} and stats/ resolve to different divisors", func(t *testing.T) {
		reqRaw := httptest.NewRequest(http.MethodGet, "/stats/", nil)
		reqRaw.Header.Set("X-JWT", authTok)
		rwRaw := httptest.NewRecorder()
		router.ServeHTTP(rwRaw, reqRaw)
		require.Equal(t, http.StatusOK, rwRaw.Code)

		reqK := httptest.NewRequest(http.MethodGet, "/stats/K", nil)
		reqK.Header.Set("X-JWT", authTok)
		rwK := httptest.NewRecorder()
		router.ServeHTTP(rwK, reqK)
		require.Equal(t, http.StatusOK, rwK.Code)

		var raw, k map[string]int64
		require.NoError(t, json.Unmarshal(rwRaw.Body.Bytes(), &raw))
		require.NoError(t, json.Unmarshal(rwK.Body.Bytes(), &k))

		require.Len(t, raw, 1)
		for day, bytes := range raw {
			assert.Equal(t, int64(12), bytes) // 8 + 4 bytes, no divider applied
			assert.Equal(t, int64(0), k[day]) // same sum, divided by 1024
		}
	})
}

// TestRouter_AuthProviderRoutesAreMounted guards against a real bug introduced
// while porting from chi to routegroup: chi's Mount("/auth", ...) matches the
// whole subtree, but routegroup.Handle("/auth", ...) (no trailing slash) only
// matches the exact path "/auth" - every oauth subpath like /auth/google/login
// would 404. Registering "/auth/" (and "/avatar/") restores subtree matching.
func TestRouter_AuthProviderRoutesAreMounted(t *testing.T) {
	s, ctx := newTestServer(t)
	router := s.router(ctx)

	for _, path := range []string{"/auth/google/login", "/auth/logout", "/auth/user"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rw := httptest.NewRecorder()
			router.ServeHTTP(rw, req)
			assert.NotEqual(t, http.StatusNotFound, rw.Code, "auth subpath must not 404")
		})
	}
}

func TestRouter_StaticFileServing(t *testing.T) {
	s, ctx := newTestServer(t)
	router := s.router(ctx)

	// the router mounts the file server directly at cfg.Storage.Repository (an absolute path).
	filePath := s.cfg.Storage.Repository + "/recMP4.mp4"

	t.Run("serves an existing file", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, filePath, nil)
		rw := httptest.NewRecorder()
		router.ServeHTTP(rw, req)
		require.Equal(t, http.StatusOK, rw.Code)
		assert.Equal(t, "videodat", rw.Body.String())
	})

	t.Run("blocks directory listing", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, s.cfg.Storage.Repository+"/", nil)
		rw := httptest.NewRecorder()
		router.ServeHTTP(rw, req)
		assert.Equal(t, http.StatusNotFound, rw.Code)
	})
}
