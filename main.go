package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	"zoomrs/config"

	"github.com/go-chi/chi/v5"
	"github.com/go-pkgz/lgr"
	"github.com/go-pkgz/rest"
	flags "github.com/umputun/go-flags"
)

type Server struct {
	cfg    *config.Parameters
	client *ZoomClient
}

func NewServer(conf *config.Parameters) *Server {
	client := NewZoomClient(conf.Client)
	return &Server{cfg: conf, client: client}
}

func (s *Server) Run(ctx context.Context) {
	log.Printf("[INFO] starting server at %s", s.cfg.Server.Listen)

	go s.startServer(ctx)

	<-ctx.Done()
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
		resp := map[string]string{
			"status": "OK",
		}

		rw.Header().Set("Content-Type", "application/json")
		json.NewEncoder(rw).Encode(resp)
	})

	router.Get("/meetings", func(rw http.ResponseWriter, r *http.Request) {
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
		conf.Server.Dbg = opts.Dbg
	}

	// Logger setup
	logOpts := []lgr.Option{
		lgr.LevelBraces,
		lgr.StackTraceOnError,
	}
	if opts.Dbg {
		logOpts = append(logOpts, lgr.Debug)
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

	NewServer(conf).Run(ctx)
}
