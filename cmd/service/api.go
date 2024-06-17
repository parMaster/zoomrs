package main

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-pkgz/auth/token"
	"github.com/go-pkgz/rest"
	"github.com/parMaster/zoomrs/storage"
	"github.com/parMaster/zoomrs/storage/model"
	"github.com/parMaster/zoomrs/web"
	"github.com/shirou/gopsutil/v3/disk"
)

func (s *Server) router(ctx context.Context) http.Handler {
	router := chi.NewRouter()
	router.Use(rest.Throttle(5))

	// auth routes
	authRoutes, avaRoutes := s.authService.Handlers()
	router.Mount("/auth", authRoutes)
	router.Mount("/avatar", avaRoutes)

	// Private routes
	m := s.authService.Middleware()
	router.With(m.Auth).Get("/listMeetings", s.listMeetings(ctx))

	router.With(m.Trace).Get("/", s.indexPageHandler)

	router.With(m.Auth).Route("/stats", func(r chi.Router) {
		r.Get("/{divider}", s.statsHandler(ctx))
		r.Get("/", s.statsHandler(ctx))
	})

	router.Post("/meetingsLoaded/{accessKey}", s.meetingsLoadedHandler(ctx))

	router.With(m.Auth).Get("/check", s.checkConsistencyHandler(ctx))

	// Public routes
	router.Get("/status", s.statusHandler(ctx))

	router.Get("/watchMeeting/{accessKey}", s.watchMeetingHandler(ctx))
	router.Get("/watch/{accessKey}", s.watchHandler)

	router.Get("/login", func(rw http.ResponseWriter, r *http.Request) {
		s.respondWithFile("web/auth.html", rw)
	})

	router.Get("/favicon.ico", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "image/x-icon")
		s.respondWithFile("web/favicon.ico", rw)
	})

	fs := http.FileServer(http.Dir(s.cfg.Storage.Repository))
	router.Handle("/"+s.cfg.Storage.Repository+"/*", http.StripPrefix("/"+s.cfg.Storage.Repository, filesOnly(fs)))

	return router
}

// indexPageHandler serves / path (web/index.html)
func (s *Server) indexPageHandler(rw http.ResponseWriter, r *http.Request) {
	// Check if user logged in
	userInfo, err := token.GetUserInfo(r)
	log.Printf("[DEBUG] userInfo: %+v", userInfo)
	log.Printf("[DEBUG] err: %+v", err)
	if err != nil || userInfo.Attributes["email"] == "" {
		http.Redirect(rw, r, "/login", http.StatusFound)
		return
	}
	s.respondWithFile("web/index.html", rw)
}

// watchHandler serves /watchHandler/{accessKey} path (web/watchHandler.html)
func (s *Server) watchHandler(rw http.ResponseWriter, r *http.Request) {
	accessKey := chi.URLParam(r, "accessKey")
	if accessKey == "" {
		rw.WriteHeader(http.StatusBadRequest)
		return
	}
	s.respondWithFile("web/watch.html", rw)
}

func (s *Server) respondWithFile(file string, rw http.ResponseWriter) error {
	var html []byte
	var err error
	if s.cfg.Server.Dbg {
		html, err = os.ReadFile(file)
	} else {
		file = file[4:] // cut off web/ prefix
		html, err = web.WebAssets.ReadFile(file)
	}
	if err != nil {
		log.Printf("[ERROR] failed to read %s, %v", file, err)
		rw.WriteHeader(http.StatusInternalServerError)
		return err
	}
	rw.Write(html)
	return nil
}

func (s *Server) statusHandler(ctx context.Context) func(rw http.ResponseWriter, r *http.Request) {
	return func(rw http.ResponseWriter, r *http.Request) {
		stats, _ := s.store.Stats(ctx)

		if stats == nil {
			rw.WriteHeader(http.StatusNoContent)
			return
		}

		_, qok := stats[model.StatusQueued]
		_, fok := stats[model.StatusFailed]
		_, dok := stats[model.StatusDownloading]

		var status string
		if qok || dok {
			status = "LOADING"
		} else if fok && !dok && !qok {
			status = "FAILED"
		} else {
			status = "OK"
		}

		resp := map[string]interface{}{
			"status": status,
			"stats":  stats,
		}

		var lastDownloadedMeeting model.Meeting
		cachedLast, err := s.cache.Get("lastDownloadedMeeting")
		if err != nil {
			log.Printf("[DEBUG] miss")

			meetingsLoaded, err := s.store.ListMeetings(ctx)
			if err != nil {
				log.Printf("[ERROR] failed to list meetings, %v", err)
				rw.WriteHeader(http.StatusInternalServerError)
				return
			}
			lastDownloadedMeeting = meetingsLoaded[0]
			s.cache.Set("lastDownloadedMeeting", lastDownloadedMeeting, 10*60)
		} else {
			log.Printf("[DEBUG] hit")
			lastDownloadedMeeting = cachedLast.(model.Meeting)
		}
		resp["last_downloaded"] = lastDownloadedMeeting.DateTime

		var cloudStorageReport *model.CloudRecordingReport
		cachedCloud, err := s.cache.Get("cloudStorageReport")
		if err != nil {
			log.Printf("[DEBUG] miss")

			cloudStorageReport, err = s.client.GetCloudStorageReport(time.Now().AddDate(0, 0, -7).Format("2006-01-02"), time.Now().Format("2006-01-02"))
			if err != nil {
				log.Printf("[ERROR] failed to get cloud storage report, %v", err)
				rw.WriteHeader(http.StatusInternalServerError)
				return
			}
			s.cache.Set("cloudStorageReport", cloudStorageReport, 60*60)
		} else {
			log.Printf("[DEBUG] hit")
			cloudStorageReport = cachedCloud.(*model.CloudRecordingReport)
		}

		// cloud storage stats
		var cloud model.CloudRecordingStorage
		if cloudStorageReport != nil && cloudStorageReport.CloudRecordingStorage != nil && len(cloudStorageReport.CloudRecordingStorage) > 0 {

			cloud = cloudStorageReport.CloudRecordingStorage[len(cloudStorageReport.CloudRecordingStorage)-1]

			// remove " GB" suffix from FreeUsage, PlanUsage and Usage fields to convert them to float
			freeUsage, _ := strconv.ParseFloat(strings.TrimSuffix(cloud.FreeUsage, " GB"), 64)
			planUsage, _ := strconv.ParseFloat(strings.TrimSuffix(cloud.PlanUsage, " GB"), 64)
			usage, _ := strconv.ParseFloat(strings.TrimSuffix(cloud.Usage, " GB"), 64)
			// calculate usage percent
			cloud.UsagePercent = int((usage / (freeUsage + planUsage)) * 100)

			resp["cloud"] = cloud
		}

		// disk storage stats
		diskStorageReport, err := disk.Usage(s.cfg.Storage.Repository)
		if err != nil {
			log.Printf("[ERROR] failed to get disk storage report, %v", err)
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}

		resp["storage"] = map[string]interface{}{
			"total":         model.FileSize(diskStorageReport.Total),
			"free":          model.FileSize(diskStorageReport.Free),
			"used":          model.FileSize(diskStorageReport.Used),
			"usage_percent": int(diskStorageReport.UsedPercent),
		}

		rw.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(rw)
		enc.SetIndent("", "    ")
		enc.Encode(resp)
	}
}

func (s *Server) listMeetings(ctx context.Context) func(rw http.ResponseWriter, r *http.Request) {
	return func(rw http.ResponseWriter, r *http.Request) {
		userInfo, err := token.GetUserInfo(r)
		if err != nil {
			log.Printf("[ERROR] failed to get user info, %v", err)
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}
		log.Printf("[INFO] /listMeetings: %s (%s)", userInfo.Email, r.Header.Get("X-Real-Ip"))

		rw.Header().Set("Content-Type", "application/json")
		rw.Header().Set("Access-Control-Allow-Origin", "*")
		m, err := s.store.ListMeetings(ctx)
		if err != nil {
			log.Printf("[ERROR] failed to list meetings, %v", err)
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}

		// mix in an accessKey for each meeting to be used in watchMeeting
		for i := range m {

			s := fmt.Sprintf("%s%s", m[i].UUID, s.cfg.Server.AccessKeySalt)
			h := md5.New()
			io.WriteString(h, s)
			m[i].AccessKey = fmt.Sprintf("%x", h.Sum(nil))
			// log.Printf("[DEBUG] salted uuid: %s, accessKey: %s", s, m[i].AccessKey)
		}

		resp := map[string]interface{}{
			"data": m,
		}
		json.NewEncoder(rw).Encode(resp)
	}
}

func (s *Server) watchMeetingHandler(ctx context.Context) func(rw http.ResponseWriter, r *http.Request) {
	return func(rw http.ResponseWriter, r *http.Request) {
		// uuid is get parameter
		accessKey := chi.URLParam(r, "accessKey")
		uuid := r.URL.Query().Get("uuid")
		log.Printf("[INFO] /watchMeeting/%s?uuid=%s (%s)", accessKey, uuid, r.Header.Get("X-Real-Ip"))

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

		meeting, err := s.store.GetMeeting(ctx, uuid)
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

		records, err := s.store.GetRecords(ctx, meeting.UUID)
		if err != nil {
			log.Printf("[ERROR] failed to get records, %v", err)
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}
		// cleanup records of columns FileExtension, DownloadURL, PlayURL
		for i := range records {
			records[i].FileExtension = ""
			records[i].DownloadURL = ""
			records[i].PlayURL = ""
		}

		log.Printf("[INFO] /watchMeeting granted")

		resp := map[string]interface{}{
			"meeting": meeting,
			"records": records,
		}
		rw.Header().Set("Content-Type", "application/json")
		json.NewEncoder(rw).Encode(resp)
	}
}

func (s *Server) statsHandler(ctx context.Context) func(rw http.ResponseWriter, r *http.Request) {
	return func(rw http.ResponseWriter, r *http.Request) {
		divider := strings.ToUpper(chi.URLParam(r, "divider"))
		var d rune
		if len(divider) > 0 {
			d = []rune(divider)[0]
		}

		stats, err := s.repo.GetStats(ctx, d)
		if err != nil {
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}

		if stats == nil {
			rw.WriteHeader(http.StatusNoContent)
			return
		}

		rw.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(rw)
		enc.SetIndent("", "    ")
		enc.Encode(stats)
	}
}

// filesOnly is a middleware to allow only files to be served, no directory listings allowed
func filesOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		log.Printf("[INFO] %s (%s)", r.URL.Path, r.Header.Get("X-Real-Ip"))
		next.ServeHTTP(w, r)
	})
}

// meetingsLoadedHandler is called to ask if every meeting from the list is loaded
// list is passed as a JSON array of UUIDs in the request body
// response is result:ok or result:pending
func (s *Server) meetingsLoadedHandler(ctx context.Context) func(rw http.ResponseWriter, r *http.Request) {
	return func(rw http.ResponseWriter, r *http.Request) {
		accessKey := chi.URLParam(r, "accessKey")
		log.Printf("[INFO] /meetingsLoaded/%s (%s)", accessKey, r.Header.Get("X-Real-Ip"))
		if accessKey == "" || accessKey != s.cfg.Server.AccessKeySalt {
			rw.WriteHeader(http.StatusForbidden)
			return
		}

		type req struct {
			Meetings []string `json:"meetings"`
		}
		var uuids req
		r.Body = http.MaxBytesReader(rw, r.Body, int64(1<<22)) // 4MB
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		err := decoder.Decode(&uuids)
		if err != nil {
			log.Printf("[ERROR] failed to decode request body, %v", err)
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}

		log.Printf("[DEBUG] Checking if uuids loaded: \r\n %+v", uuids.Meetings)
		resp := map[string]interface{}{}
		for _, uuid := range uuids.Meetings {
			recs, err := s.store.GetRecords(ctx, uuid)
			if err != nil {
				log.Printf("[ERROR] failed to get records, %v", err)
				rw.WriteHeader(http.StatusInternalServerError)
				return
			}
			if len(recs) == 0 {
				resp["result"] = "pending"
				log.Printf("[DEBUG] Pending caused by no records for uuid: %s", uuid)
				json.NewEncoder(rw).Encode(resp)
				return
			}

			log.Printf("[DEBUG] Checking recs: \r\n %+v", recs)
			for _, rec := range recs {

				if rec.Status != model.StatusDownloaded {
					resp["result"] = "pending"
					log.Printf("[DEBUG] Pending caused by status %s - %s", rec.Id, rec.Status)
					json.NewEncoder(rw).Encode(resp)
					return
				}

				if info, err := os.Stat(rec.FilePath); err == nil {
					if info.Size() != int64(rec.FileSize) {
						resp["result"] = "pending"
						log.Printf("[DEBUG] Pending caused by filesize %s - %d", rec.Id, rec.FileSize)
						json.NewEncoder(rw).Encode(resp)
						return
					}
				}
			}
		}

		log.Printf("[DEBUG] All records are downloaded, returning ok")
		resp["result"] = "ok"
		json.NewEncoder(rw).Encode(resp)
	}
}

// checkConsistencyHandler is called to check if every record has a corresponding file and file size is correct
func (s *Server) checkConsistencyHandler(ctx context.Context) func(rw http.ResponseWriter, r *http.Request) {
	return func(rw http.ResponseWriter, r *http.Request) {
		checked, err := s.repo.CheckConsistency(ctx)
		response := map[string]interface{}{"checked": checked, "error": err}
		rw.Header().Set("Content-Type", "application/json")
		json.NewEncoder(rw).Encode(response)
	}
}
