package main

import (
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

func (s *Server) router() http.Handler {
	router := chi.NewRouter()
	router.Use(rest.Throttle(5))

	// auth routes
	authRoutes, avaRoutes := s.authService.Handlers()
	router.Mount("/auth", authRoutes)
	router.Mount("/avatar", avaRoutes)

	// Private routes
	m := s.authService.Middleware()
	router.With(m.Auth).Get("/listMeetings", s.listMeetingsHandler)

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

	router.With(m.Auth).Route("/stats", func(r chi.Router) {
		r.Get("/{divider}", s.statsHandler)
		r.Get("/", s.statsHandler)
	})

	router.Post("/meetingsLoaded/{accessKey}", s.meetingsLoadedHandler)

	router.With(m.Auth).Get("/check", s.checkConsistencyHandler)

	// Public routes
	router.Get("/status", s.statusHandler)
	router.Get("/watchMeeting/{accessKey}", s.watchMeetingHandler)

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

	fs := http.FileServer(http.Dir(s.cfg.Storage.Repository))
	router.Handle("/"+s.cfg.Storage.Repository+"/*", http.StripPrefix("/"+s.cfg.Storage.Repository, filesOnly(fs)))

	return router
}

func (s *Server) responseWithFile(file string, rw http.ResponseWriter) error {
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

func (s *Server) statusHandler(rw http.ResponseWriter, r *http.Request) {
	stats, _ := s.store.Stats()

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

	var cloudStorageReport *model.CloudRecordingReport
	cachedCloud, err := s.cache.Get("cloudStorageReport")
	if err != nil {
		log.Printf("[DEBUG] miss")

		cloudStorageReport, err = s.client.GetCloudStorageReport(time.Now().AddDate(0, 0, -2).Format("2006-01-02"), time.Now().Format("2006-01-02"))
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

func (s *Server) listMeetingsHandler(rw http.ResponseWriter, r *http.Request) {
	userInfo, err := token.GetUserInfo(r)
	if err != nil {
		log.Printf("[ERROR] failed to get user info, %v", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}
	log.Printf("[INFO] /listMeetings: %s (%s)", userInfo.Email, r.Header.Get("X-Real-Ip"))

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
}

func (s *Server) watchMeetingHandler(rw http.ResponseWriter, r *http.Request) {
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

	log.Printf("[INFO] /watchMeeting granted")

	resp := map[string]interface{}{
		"meeting": meeting,
		"records": records,
	}
	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(resp)
}

func (s *Server) statsHandler(rw http.ResponseWriter, r *http.Request) {
	div := chi.URLParam(r, "divider")
	div = strings.ToUpper(div)

	stats, _ := s.store.GetRecordsByStatus(model.StatusDownloaded)
	if stats == nil {
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	// group stats by day, calculate sum of the file size
	resp := map[string]int64{}

	for _, r := range stats {
		day := r.DateTime[:10]
		if _, ok := resp[day]; !ok {
			resp[day] = 0
		}
		resp[day] += int64(r.FileSize)
	}

	dividers := map[string]int64{
		"K": 1024,
		"M": 1024 * 1024,
		"G": 1024 * 1024 * 1024,
	}
	divider, ok := dividers[div]
	if !ok {
		divider = 1
	}
	for k, v := range resp {
		resp[k] = v / divider
	}

	rw.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(rw)
	enc.SetIndent("", "    ")
	enc.Encode(resp)
}

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
func (s *Server) meetingsLoadedHandler(rw http.ResponseWriter, r *http.Request) {
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
	err := json.NewDecoder(r.Body).Decode(&uuids)
	if err != nil {
		log.Printf("[ERROR] failed to decode request body, %v", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	log.Printf("[DEBUG] Checking if uuids loaded: \r\n %+v", uuids.Meetings)
	resp := map[string]interface{}{}
	for _, uuid := range uuids.Meetings {
		recs, err := s.store.GetRecords(uuid)
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

// checkConsistencyHandler is called to check if every record has a corresponding file and file size is correct
func (s *Server) checkConsistencyHandler(rw http.ResponseWriter, r *http.Request) {
	checked, err := s.repo.CheckConsistency()
	response := map[string]interface{}{"checked": checked, "error": err}
	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(response)
}
