package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-pkgz/lgr"
	"github.com/parMaster/zoomrs/client"
	"github.com/parMaster/zoomrs/config"
	"github.com/parMaster/zoomrs/repo"
	"github.com/parMaster/zoomrs/storage"
	"github.com/parMaster/zoomrs/storage/sqlite"
	"github.com/umputun/go-flags"
)

type Commander struct {
	cfg    *config.Parameters
	client *client.ZoomClient
	store  storage.Storer
	ctx    context.Context
	cancel context.CancelFunc
}

func NewCommander(conf *config.Parameters, ctx context.Context, cancel context.CancelFunc) *Commander {
	client := client.NewZoomClient(conf.Client)
	return &Commander{cfg: conf, client: client, ctx: ctx, cancel: cancel}
}

func (s *Commander) Run(opts Options) {
	log.Printf("[INFO] starting cli commander")

	err := LoadStorage(s.ctx, s.cfg.Storage, &s.store)
	if err != nil {
		log.Fatalf("[ERROR] failed to init storage: %e", err)
	}

	r := repo.NewRepository(s.store, s.client, s.cfg)

	switch opts.Cmd {
	case "check":
		log.Printf("[INFO] starting CheckConsistency")
		checked, err := r.CheckConsistency()
		if err != nil {
			log.Printf("[ERROR] CheckConsistency: %d, %e", checked, err)
		} else {
			log.Printf("[INFO] CheckConsistency: OK, %d", checked)
		}
	case "trash":
		log.Printf("[INFO] starting CleanupJob")
		// Run cleanup job. crontab line example:
		// 00 10 * * * cd $HOME/go/src/zoomrs/dist && ./zoomrs-cli --dbg --cmd trash --trash 2 --config ../config/config_cli.yml >> /var/log/cron.log 2>&1
		if opts.Trash == -1 { // -1 is default value, so "0" value is allowed - it will delete today's meetings
			log.Printf("[ERROR] CleanupJob: '--trash' option (days) is not set")
			break
		}
		r.CleanupJob(s.ctx, opts.Trash)
	case "sync":
		log.Printf("[INFO] starting SyncJob")

		if len(r.Syncable.Important)+len(r.Syncable.Alternative)+len(r.Syncable.Optional) == 0 {
			log.Printf("[DEBUG] No sync types configured. Sync job will not run")
			return
		}
		for {
			select {
			case <-s.ctx.Done():
				return
			default:
			}

			meetings, err := s.client.GetMeetings(opts.Days)
			if err != nil {
				log.Printf("[ERROR] failed to get meetings, %v, retrying in 30 sec", err)
				time.Sleep(30 * time.Second)
				continue
			}
			log.Printf("[DEBUG] Syncing meetings - %d in feed", len(meetings))

			err = r.SyncMeetings(&meetings)
			if err != nil {
				log.Printf("[ERROR] failed to sync meetings, %v, retrying in 30 sec", err)
				time.Sleep(30 * time.Second)
				continue
			}
			break
		}

		var lastError error
		for {
			select {
			case <-s.ctx.Done():
				return
			default:
			}
			err = r.DownloadOnce()
			if err == repo.ErrNoQueuedRecords {
				if err == lastError {
					log.Printf("[DEBUG] no queued records, exiting")
					break
				}
				lastError = err
				continue
			}
			if err != nil {
				log.Printf("[ERROR] failed to download meetings, %v, retrying in 30 sec", err)
				lastError = err
				time.Sleep(30 * time.Second)
				continue
			}
		}
	default:
		s.ShowUI()
	}

	log.Printf("[INFO] cli job done\n*********************************")
	s.cancel()
	<-s.ctx.Done()
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

type Options struct {
	Config string `long:"config" env:"CONFIG" default:"config/config_cli.yml" description:"yaml config file name"`
	Days   int    `long:"days" env:"DEBUG" description:"(today - 'days') day to sync. Default is 1 (yesterday)" default:"1"`
	Dbg    bool   `long:"dbg" env:"DEBUG" description:"show debug info"`
	Trash  int    `long:"trash" description:"trash old meetings after N days. Required when '--cmd=trash'" default:"-1"`
	Cmd    string `long:"cmd" description:"run command"`
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
		signal.Notify(stop, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
		<-stop
		log.Println("Shutdown signal received\n*********************************")
		cancel()
	}()

	NewCommander(conf, ctx, cancel).Run(opts)
}
