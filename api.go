package main

import (
	"crypto/md5"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/go-pkgz/auth/token"
	"github.com/go-pkgz/rest"
	"github.com/parMaster/zoomrs/storage"
)

//go:embed web/*
var web_assets embed.FS

func (s *Server) responseWithFile(file string, rw http.ResponseWriter) error {
	var html []byte
	var err error
	if s.cfg.Server.Dbg {
		html, err = os.ReadFile(file)
	} else {
		html, err = web_assets.ReadFile(file)
	}
	if err != nil {
		log.Printf("[ERROR] failed to read %s, %v", file, err)
		rw.WriteHeader(http.StatusInternalServerError)
		return err
	}
	rw.Write(html)
	return nil
}

func (s *Server) router() http.Handler {
	router := chi.NewRouter()
	router.Use(rest.Throttle(5))

	// auth routes
	authRoutes, avaRoutes := s.authService.Handlers()
	router.Mount("/auth", authRoutes)
	router.Mount("/avatar", avaRoutes)
	m := s.authService.Middleware()

	router.Get("/status", func(rw http.ResponseWriter, r *http.Request) {
		stats, _ := s.store.Stats()

		if stats == nil {
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}

		resp := map[string]interface{}{
			"status": "OK",
			"stats":  stats,
		}

		rw.Header().Set("Content-Type", "application/json")
		json.NewEncoder(rw).Encode(resp)
	})

	router.With(m.Auth).Get("/listMeetings", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		rw.Header().Set("Access-Control-Allow-Origin", "*")
		m, err := s.store.ListMeetings()
		if err != nil {
			log.Printf("[ERROR] failed to list meetings, %v", err)
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}

		// mix in an accessKey for each meeting to be used in watchMeeting
		for i := range m {
			s := m[i].UUID + s.cfg.Server.AccessKeySalt
			h := md5.New()
			io.WriteString(h, s)
			m[i].AccessKey = fmt.Sprintf("%x", h.Sum(nil))
			// log.Printf("[DEBUG] salted uuid: %s, accessKey: %s", s, m[i].AccessKey)
		}

		resp := map[string]interface{}{
			"data": m,
		}
		json.NewEncoder(rw).Encode(resp)
	})

	router.Get("/watch/{accessKey}", func(rw http.ResponseWriter, r *http.Request) {
		accessKey := chi.URLParam(r, "accessKey")
		if accessKey == "" {
			rw.WriteHeader(http.StatusBadRequest)
			return
		}
		s.responseWithFile("web/watch.html", rw)
	})

	router.Get("/login", func(rw http.ResponseWriter, r *http.Request) {
		s.responseWithFile("web/auth.html", rw)
	})

	router.Get("/favicon.ico", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "image/x-icon")
		s.responseWithFile("web/favicon.ico", rw)
	})

	router.Get("/watchMeeting/{accessKey}", func(rw http.ResponseWriter, r *http.Request) {
		// uuid is get parameter
		accessKey := chi.URLParam(r, "accessKey")
		uuid := r.URL.Query().Get("uuid")
		log.Printf("[DEBUG] /watchMeeting/%s?uuid=%s", accessKey, uuid)

		if accessKey == "" || uuid == "" {
			rw.WriteHeader(http.StatusBadRequest)
			return
		}

		// check accessKey
		h := md5.New()
		saltedUUID := uuid + s.cfg.Server.AccessKeySalt
		log.Printf("[DEBUG] salted uuid: %s", saltedUUID)
		io.WriteString(h, saltedUUID)
		key := fmt.Sprintf("%x", h.Sum(nil))
		log.Printf("[DEBUG] accessKey: %s, key: %s", accessKey, key)
		if accessKey != key {
			rw.WriteHeader(http.StatusForbidden)
			return
		}

		meeting, err := s.store.GetMeeting(uuid)
		log.Printf("[DEBUG] meeting: %+v", meeting)
		if err != nil {
			if err == storage.ErrNoRows {
				rw.WriteHeader(http.StatusNotFound)
				return
			}
			log.Printf("[ERROR] failed to get meeting, %v", err)
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}

		records, err := s.store.GetRecordsInfo(meeting.UUID)
		if err != nil {
			log.Printf("[ERROR] failed to get records, %v", err)
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}

		resp := map[string]interface{}{
			"meeting": meeting,
			"records": records,
		}

		rw.Header().Set("Content-Type", "application/json")
		json.NewEncoder(rw).Encode(resp)
	})

	router.Get("/loadMeetings", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		rw.Header().Set("Access-Control-Allow-Origin", "*")

		meetings, err := s.client.GetMeetings()
		if err != nil {
			log.Printf("[ERROR] failed to get meetings, %v", err)
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}
		json.NewEncoder(rw).Encode(meetings)
	})

	router.With(m.Trace).Get("/", func(rw http.ResponseWriter, r *http.Request) {
		// Check if user logged in
		userInfo, err := token.GetUserInfo(r)
		log.Printf("[DEBUG] userInfo: %+v", userInfo)
		log.Printf("[DEBUG] err: %+v", err)
		if err != nil || userInfo.Attributes["email"] == "" {
			http.Redirect(rw, r, "/login", http.StatusFound)
			return
		}

		s.responseWithFile("web/index.html", rw)
	})

	fs := http.FileServer(http.Dir(s.cfg.Storage.Repository))
	router.Handle("/"+s.cfg.Storage.Repository+"/*", http.StripPrefix("/"+s.cfg.Storage.Repository, fs))

	return router
}
