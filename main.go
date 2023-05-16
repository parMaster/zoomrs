package main

import (
	"context"
	"crypto/md5"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/parMaster/zoomrs/config"
	"github.com/parMaster/zoomrs/storage"
	"github.com/parMaster/zoomrs/storage/sqlite"

	"github.com/go-chi/chi/v5"
	"github.com/go-pkgz/lgr"
	"github.com/go-pkgz/rest"
	flags "github.com/umputun/go-flags"
)

//go:embed web/index.html
var index_html string

//go:embed web/watch.html
var watch_html string

type Server struct {
	cfg    *config.Parameters
	client *ZoomClient
	store  storage.Storer
	ctx    context.Context
}

func NewServer(conf *config.Parameters, ctx context.Context) *Server {
	client := NewZoomClient(conf.Client)
	return &Server{cfg: conf, client: client, ctx: ctx}
}

func LoadStorage(ctx context.Context, cfg config.Storage, s *storage.Storer) error {
	var err error
	switch cfg.Type {
	case "sqlite":
		*s, err = sqlite.NewStorage(ctx, cfg.Path)
		if err != nil {
			return fmt.Errorf("failed to init SQLite storage: %e", err)
		}
	case "":
		return errors.New("storage is not configured")
	default:
		return fmt.Errorf("storage type %s is not supported", cfg.Type)
	}
	return err
}

func (s *Server) Run() {
	log.Printf("[INFO] starting server at %s", s.cfg.Server.Listen)

	err := LoadStorage(s.ctx, s.cfg.Storage, &s.store)
	if err != nil {
		log.Fatalf("[ERROR] failed to init storage: %e", err)
	}

	repo := NewRepository(s.store, s.client, *s.cfg)

	go s.startServer(s.ctx)
	go repo.SyncJob(s.ctx)
	go repo.DownloadJob(s.ctx)

	<-s.ctx.Done()
}

func (s *Server) startServer(ctx context.Context) {
	httpServer := &http.Server{
		Addr:              s.cfg.Server.Listen,
		Handler:           s.router(),
		ReadHeaderTimeout: time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       time.Second,
	}

	httpServer.ListenAndServe()

	<-ctx.Done()
	log.Printf("[INFO] Terminating http server")

	if err := httpServer.Close(); err != nil {
		log.Printf("[ERROR] failed to close http server, %v", err)
	}
}

func (s *Server) router() http.Handler {
	router := chi.NewRouter()
	router.Use(rest.Throttle(5))

	router.Get("/status", func(rw http.ResponseWriter, r *http.Request) {
		stats, _ := s.store.Stats()

		resp := map[string]interface{}{
			"status": "OK",
			"stats":  stats,
		}

		rw.Header().Set("Content-Type", "application/json")
		json.NewEncoder(rw).Encode(resp)
	})

	router.Get("/listMeetings", func(rw http.ResponseWriter, r *http.Request) {
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
			log.Printf("[DEBUG] salted uuid: %s, accessKey: %s", s, m[i].AccessKey)
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
		if s.cfg.Server.Dbg {
			if b, err := os.ReadFile("web/watch.html"); err == nil {
				rw.Write([]byte(b))
			}
		} else {
			rw.Write([]byte(watch_html))
		}
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

	router.Get("/", func(rw http.ResponseWriter, r *http.Request) {
		if s.cfg.Server.Dbg {
			if b, err := os.ReadFile("web/index.html"); err == nil {
				rw.Write([]byte(b))
			}
		} else {
			rw.Write([]byte(index_html))
		}
	})

	fs := http.FileServer(http.Dir(s.cfg.Storage.Repository))
	router.Handle("/"+s.cfg.Storage.Repository+"/*", http.StripPrefix("/"+s.cfg.Storage.Repository, fs))

	return router
}

type Options struct {
	Config string `long:"config" env:"CONFIG" default:"config.yml" description:"yaml config file name"`
	Dbg    bool   `long:"dbg" env:"DEBUG" description:"show debug info"`
}

func main() {
	// Parsing cmd parameters
	var opts Options
	p := flags.NewParser(&opts, flags.PassDoubleDash|flags.HelpFlag)
	if _, err := p.Parse(); err != nil {
		if err.(*flags.Error).Type != flags.ErrHelp {
			fmt.Printf("%v\n", err)
			os.Exit(1)
		}
		p.WriteHelp(os.Stderr)
		os.Exit(2)
	}

	var conf *config.Parameters
	if opts.Config != "" {
		var err error
		conf, err = config.NewConfig(opts.Config)
		if err != nil {
			log.Fatalf("[ERROR] can't load config, %s", err)
		}
		if opts.Dbg {
			conf.Server.Dbg = opts.Dbg
		}
	}

	// Logger setup
	logOpts := []lgr.Option{
		lgr.LevelBraces,
		lgr.StackTraceOnError,
	}
	if conf.Server.Dbg {
		logOpts = append(logOpts, lgr.Debug)
		logOpts = append(logOpts, lgr.StackTraceOnError)
	}
	lgr.SetupStdLogger(logOpts...)

	lgr.Secret(conf.Client.AccountId, conf.Client.Id, conf.Client.Secret)

	// Graceful termination
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		if x := recover(); x != nil {
			log.Printf("[WARN] run time panic:\n%v", x)
			panic(x)
		}

		// catch signal and invoke graceful termination
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
		<-stop
		log.Println("Shutdown signal received\n*********************************")
		cancel()
	}()

	NewServer(conf, ctx).Run()
}
