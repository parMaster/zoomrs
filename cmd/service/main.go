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

	"github.com/go-pkgz/auth"
	"github.com/go-pkgz/lgr"
	flags "github.com/umputun/go-flags"
)

type Server struct {
	cfg         *config.Parameters
	client      *client.ZoomClient
	store       storage.Storer
	ctx         context.Context
	authService *auth.Service
	repo        *repo.Repository
}

func NewServer(conf *config.Parameters, ctx context.Context) *Server {
	client := client.NewZoomClient(conf.Client)
	authService, err := webauth.NewAuthService(conf.Server)
	if err != nil {
		log.Fatalf("[ERROR] failed to init auth service: %e", err)
	}
	return &Server{cfg: conf, client: client, ctx: ctx, authService: authService}
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

	s.repo = repo.NewRepository(s.store, s.client, *s.cfg)

	go s.startServer(s.ctx)
	go s.repo.SyncJob(s.ctx)
	go s.repo.DownloadJob(s.ctx)

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
