package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/parMaster/zoomrs/client"
	"github.com/parMaster/zoomrs/config"
	"github.com/parMaster/zoomrs/repo"
	"github.com/parMaster/zoomrs/storage"
	"github.com/parMaster/zoomrs/storage/sqlite"
	"github.com/parMaster/zoomrs/webauth"

	"github.com/parMaster/mcache/v2"

	"github.com/go-pkgz/auth"
	"github.com/go-pkgz/lgr"
	flags "github.com/jessevdk/go-flags"
)

type Server struct {
	cfg         *config.Parameters
	client      *client.ZoomClient
	store       storage.Storer
	authService *auth.Service
	repo        *repo.Repository
	cache       mcache.Cacher[any]
}

func NewServer(conf *config.Parameters) *Server {
	client := client.NewZoomClient(conf.Client)
	authService, err := webauth.NewAuthService(conf.Server)
	if err != nil {
		log.Fatalf("[ERROR] failed to init auth service: %v", err)
	}
	cache := mcache.NewCache[any]()

	return &Server{cfg: conf, client: client, authService: authService, cache: cache}
}

func LoadStorage(ctx context.Context, cfg config.Storage, s *storage.Storer) error {
	var err error
	switch cfg.Type {
	case "sqlite":
		*s, err = sqlite.NewStorage(ctx, cfg.Path)
		if err != nil {
			return fmt.Errorf("failed to init SQLite storage: %w", err)
		}
	case "":
		return errors.New("storage is not configured")
	default:
		return fmt.Errorf("storage type %s is not supported", cfg.Type)
	}
	return err
}

func (s *Server) Run(ctx context.Context) {

	err := LoadStorage(ctx, s.cfg.Storage, &s.store)
	if err != nil {
		log.Fatalf("[ERROR] failed to init storage: %v", err)
	}

	s.repo = repo.NewRepository(s.store, s.client, s.cfg)

	log.Printf("[INFO] starting server at %s", s.cfg.Server.Listen)
	served := make(chan struct{})
	go func() {
		defer close(served)
		s.startServer(ctx)
	}()

	if s.cfg.Server.SyncJob {
		log.Printf("[INFO] starting sync job")
		go s.repo.SyncJob(ctx)
	}
	if s.cfg.Server.DownloadJob {
		log.Printf("[INFO] starting download job")
		go s.repo.DownloadJob(ctx)
	}

	<-ctx.Done()
	// the process exits when Run returns, so open requests need this wait to finish
	<-served
}

// drainTimeout leaves room inside Docker's default 10s between SIGTERM and SIGKILL
const drainTimeout = 5 * time.Second

func (s *Server) startServer(ctx context.Context) {
	serveHTTP(ctx, s.cfg.Server.Listen, s.router(ctx), drainTimeout)
}

// serveHTTP answers requests on addr until ctx is done, then gives open requests
// up to drain to finish. It returns once the server is fully stopped.
func serveHTTP(ctx context.Context, addr string, handler http.Handler, drain time.Duration) {
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       time.Second,
	}

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[ERROR] http server: %v", err)
		}
	}()

	<-ctx.Done()
	log.Printf("[INFO] Terminating http server")

	// ctx is already canceled here; Shutdown with it would not wait for open requests
	drainCtx, cancel := context.WithTimeout(context.Background(), drain)
	defer cancel()
	if err := httpServer.Shutdown(drainCtx); err != nil {
		log.Printf("[ERROR] shutdown http server: %v", err)
		// a timed out Shutdown leaves the stuck connections open
		if err := httpServer.Close(); err != nil {
			log.Printf("[ERROR] close http server: %v", err)
		}
	}
	<-stopped
}

type Options struct {
	Config  string `long:"config" env:"CONFIG" default:"config.yml" description:"yaml config file name"`
	Dbg     bool   `long:"dbg" env:"DEBUG" description:"show debug info"`
	Version bool   `short:"v" description:"Show version and exit"`
}

var version = "undefined" // version is set during build

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

	// Version
	if opts.Version {
		fmt.Printf("Version: %s\n", version)
		os.Exit(0)
	}
	log.Printf("[DEBUG] Pid: %d, ver: %s", os.Getpid(), version)

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
		lgr.Secret(conf.Client.AccountId, conf.Client.Id, conf.Client.Secret, conf.Server.OAuthClientId, conf.Server.OAuthClientSecret, conf.Server.JWTSecret),
	}
	if conf.Server.Dbg {
		logOpts = append(logOpts, lgr.Debug)
	}
	lgr.SetupStdLogger(logOpts...)

	// Graceful termination
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// catch signal and invoke graceful termination
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
		<-stop
		log.Println("Shutdown signal received\n*********************************")
		cancel()
	}()

	defer func() {
		if x := recover(); x != nil {
			log.Printf("[WARN] run time panic: %+v", x)
		}
	}()

	NewServer(conf).Run(ctx)
}
