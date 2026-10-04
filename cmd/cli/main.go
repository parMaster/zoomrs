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
	"github.com/jessevdk/go-flags"
	"github.com/parMaster/zoomrs/client"
	"github.com/parMaster/zoomrs/config"
	"github.com/parMaster/zoomrs/repo"
	"github.com/parMaster/zoomrs/storage"
	"github.com/parMaster/zoomrs/storage/sqlite"
)

const (
	// daysUnset is the value of '--days' and '--trash' when the flag is not given
	daysUnset = -1
	// windowDays is how far back a run without '--days' goes. Zoom lists at most one month per query.
	windowDays = 30
)

type Commander struct {
	cfg    *config.Parameters
	client *client.ZoomClient
	store  storage.Storer
	// pause before another attempt after a failed listing, save or download
	retryWait time.Duration
	// how long a sync may keep downloading
	syncTimeout time.Duration
}

func NewCommander(conf *config.Parameters) *Commander {
	client := client.NewZoomClient(conf.Client)
	return &Commander{cfg: conf, client: client, retryWait: 30 * time.Second, syncTimeout: 12 * time.Hour}
}

func (s *Commander) Run(ctx context.Context, opts Options) error {
	log.Printf("[INFO] starting cli commander")

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// force deletes without confirmation, so it must name its day and never reach the whole window
	if opts.Cmd == "trash" && opts.Force && opts.Days == daysUnset {
		return errors.New("cleanupJob: '--force' needs '--days' to be set")
	}

	switch opts.Cmd {
	case "sync", "trash", "cloudcap":
		release, err := acquireLock(lockPath(s.cfg.Storage.Path))
		if err != nil {
			return err
		}
		defer release()
	}

	// the store is not tied to ctx: a sync stopped by a signal still reads its failed records
	storeCtx, closeStore := context.WithCancel(context.WithoutCancel(ctx))
	defer closeStore()
	err := LoadStorage(storeCtx, s.cfg.Storage, &s.store)
	if err != nil {
		err := fmt.Errorf("failed to init storage: %w", err)
		return err
	}

	r := repo.NewRepository(s.store, s.client, s.cfg)

	switch opts.Cmd {
	case "check":
		log.Printf("[INFO] starting CheckConsistency")
		checked, err := r.CheckConsistency(ctx)
		if err != nil {
			err := fmt.Errorf("checkConsistency: %d, %w", checked, err)
			return err
		} else {
			log.Printf("[INFO] CheckConsistency: OK, %d", checked)
		}
	case "trash":
		log.Printf("[INFO] starting CleanupJob")
		// Run cleanup job. crontab line example:
		// 00 10 * * * cd $HOME/go/src/zoomrs/dist && ./zoomrs-cli --dbg --cmd trash --config ../config/config_cli.yml >> /var/log/cron.log 2>&1
		from, to := opts.interval(time.Now())
		r.CleanupJob(ctx, from, to, opts.Force)
	case "cloudcap":
		log.Printf("[INFO] starting DeleteRecordingsOverCapacity")
		// Last line of defence against Zoom cloud storage overuse:
		// 00 10 * * * cd $HOME/go/src/zoomrs/dist && ./zoomrs-cli --dbg --cmd cloudcap --config ../config/config_cli.yml >> /var/log/cron.log 2>&1
		deleted, err := s.client.DeleteRecordingsOverCapacity(ctx, s.cfg.Client.CloudCapacityHardLimit)
		if err != nil {
			err := fmt.Errorf("deleteRecordingsOverCapacity: %d, %w", deleted, err)
			return err
		} else {
			log.Printf("[INFO] DeleteRecordingsOverCapacity: OK, %d meetings deleted", deleted)
		}
	case "sync":
		log.Printf("[INFO] starting SyncJob")
		if err := s.sync(ctx, r, opts); err != nil {
			return err
		}
	default:
		s.ShowUI()
	}

	log.Printf("[INFO] cli job done\n*********************************")
	return nil
}

// sync lists the meetings of the run's interval, saves the new ones and downloads their records
func (s *Commander) sync(ctx context.Context, r *repo.Repository, opts Options) error {
	if len(r.Syncable.Important)+len(r.Syncable.Alternative)+len(r.Syncable.Optional) == 0 {
		log.Printf("[INFO] No sync types configured. Sync job will not run")
		return fmt.Errorf("sync job will not run: no sync types configured")
	}
	from, to := opts.interval(time.Now())
	// the download below is limited to these meetings, so a run leaves the backlog
	// and the failures of days it did not list alone
	var listed []string
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("sync job terminated early: %w", ctx.Err())
		default:
		}

		meetings, err := s.client.GetIntervalMeetings(ctx, from, to)
		if err != nil {
			log.Printf("[ERROR] failed to get meetings, %v, retrying in %v", err, s.retryWait)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(s.retryWait):
				continue
			}
		}
		log.Printf("[DEBUG] Syncing meetings - %d in feed", len(meetings))

		err = r.SyncMeetings(ctx, &meetings)
		if err != nil {
			log.Printf("[ERROR] failed to sync meetings, %v, retrying in %v", err, s.retryWait)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(s.retryWait):
				continue
			}
		}
		for _, m := range meetings {
			listed = append(listed, m.UUID)
		}
		break
	}

	scope := repo.NewScope(listed)
	defer s.logFailed(context.WithoutCancel(ctx), r, scope)

	syncTimelimitCtx, cancel := context.WithTimeout(ctx, s.syncTimeout)
	defer cancel()
	var lastError error
	for {
		select {
		case <-syncTimelimitCtx.Done():
			return fmt.Errorf("downloading terminated early: %w", syncTimelimitCtx.Err())
		default:
		}
		err := r.DownloadOnceOf(ctx, scope)
		if err == repo.ErrNoQueuedRecords {
			if err == lastError {
				log.Printf("[DEBUG] no queued records, exiting")
				break
			}
			lastError = err
			continue
		}
		if err != nil {
			log.Printf("[ERROR] failed to download meetings, %v, retrying in %v", err, s.retryWait)
			lastError = err
			select {
			case <-syncTimelimitCtx.Done():
				return fmt.Errorf("downloading terminated in the process: %w", syncTimelimitCtx.Err())
			case <-time.After(s.retryWait):
				continue
			}
		}
	}
	return nil
}

// logFailed names the records of the run's meetings that are left failed, however the run
// ends: nothing puts them back in the queue, so the log is where they get noticed
func (s *Commander) logFailed(ctx context.Context, r *repo.Repository, scope *repo.Scope) {
	failed, err := r.FailedRecordsOf(ctx, scope)
	if err != nil {
		log.Printf("[ERROR] failed to list the failed records, %v", err)
		return
	}
	if len(failed) == 0 {
		return
	}
	log.Printf("[WARN] %d records are left failed:", len(failed))
	for _, rec := range failed {
		topic := "unknown meeting " + rec.MeetingId
		if m, err := s.store.GetMeeting(ctx, rec.MeetingId); err == nil {
			topic = m.Topic
		}
		log.Printf("[WARN] failed: %s | record %s | %s", topic, rec.Id, rec.DateTime)
	}
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

type Options struct {
	Config string `long:"config" env:"CONFIG" default:"config_cli.yml" description:"yaml config file name"`
	Days   int    `long:"days" description:"(today - 'days') day to sync or trash, 0 is today. When not set, the last 30 days" default:"-1"`
	Dbg    bool   `long:"dbg" env:"DEBUG" description:"show debug info"`
	Force  bool   `long:"force" env:"FORCE" description:"force operation, e.g. cleanup job won't confirm meetings are loaded. Needs '--days'"`
	Trash  int    `long:"trash" description:"deprecated, use '--days'" default:"-1"`
	Cmd    string `long:"cmd" description:"run command"`
}

// interval is the span of days a run covers: the one day asked for, or the whole window
func (o Options) interval(now time.Time) (from, to time.Time) {
	if o.Days == daysUnset {
		return now.AddDate(0, 0, -windowDays), now
	}
	day := now.AddDate(0, 0, -o.Days)
	return day, day
}

// parseOptions reads the command line and folds the deprecated '--trash' into '--days'
func parseOptions(args []string) (Options, error) {
	var opts Options
	p := flags.NewParser(&opts, flags.PassDoubleDash|flags.HelpFlag)
	if _, err := p.ParseArgs(args); err != nil {
		return opts, err
	}
	if opts.Days < daysUnset || opts.Trash < daysUnset {
		return opts, errors.New("'--days' and '--trash' can't be negative")
	}
	if opts.Trash != daysUnset {
		if opts.Days != daysUnset && opts.Days != opts.Trash {
			return opts, fmt.Errorf("'--trash %d' and '--days %d' disagree, use '--days' alone", opts.Trash, opts.Days)
		}
		log.Printf("[WARN] '--trash' is deprecated, use '--days %d'", opts.Trash)
		opts.Days = opts.Trash
	}
	return opts, nil
}

func main() {
	// Parsing cmd parameters
	opts, err := parseOptions(os.Args[1:])
	if err != nil {
		var flagsErr *flags.Error
		if errors.As(err, &flagsErr) && flagsErr.Type == flags.ErrHelp {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Printf("%v\n", err)
		os.Exit(1)
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

	err = NewCommander(conf).Run(ctx, opts)
	if err != nil {
		log.Printf("[ERROR] Commander returned error: %v\n", err)
	}
}
