package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/cavaliergopher/grab/v3"

	"github.com/parMaster/zoomrs/config"
	"github.com/parMaster/zoomrs/storage"
	"github.com/parMaster/zoomrs/storage/model"
)

type Client interface {
	Authorize() error
	GetMeetings() ([]model.Meeting, error)
	GetToken() (*AccessToken, error)
	DeleteMeetingRecordings(meetingId string, delete bool) error
}

type syncable struct {
	Important   map[model.RecordType]bool
	Alternative map[model.RecordType]bool
	Optional    map[model.RecordType]bool
}

type Repository struct {
	store    storage.Storer
	client   Client
	cfg      config.Parameters
	syncable syncable
}

func NewRepository(store storage.Storer, client Client, cfg config.Parameters) *Repository {

	sync := syncable{
		Important:   make(map[model.RecordType]bool),
		Alternative: make(map[model.RecordType]bool),
		Optional:    make(map[model.RecordType]bool),
	}
	for _, t := range cfg.Syncable.Important {
		sync.Important[model.RecordType(t)] = true
	}
	for _, t := range cfg.Syncable.Alternative {
		sync.Alternative[model.RecordType(t)] = true
	}
	for _, t := range cfg.Syncable.Optional {
		sync.Optional[model.RecordType(t)] = true
	}

	return &Repository{store: store, client: client, cfg: cfg, syncable: sync}
}

func (r *Repository) SyncJob(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Minute)
	for {
		meetings, err := r.client.GetMeetings()
		if err != nil {
			log.Printf("[ERROR] failed to get meetings, %v", err)
			continue
		}
		log.Printf("[DEBUG] Syncing meetings - %d in feed", len(meetings))

		err = r.SyncMeetings(&meetings)
		if err != nil {
			log.Printf("[ERROR] failed to sync meetings, %v", err)
			continue
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Repository) SyncMeetings(meetings *[]model.Meeting) error {
	if len(*meetings) == 0 {
		log.Printf("[DEBUG] No meetings to sync")
		return nil
	}

	if len(r.syncable.Important)+len(r.syncable.Alternative)+len(r.syncable.Optional) == 0 {
		log.Printf("[DEBUG] No sync types configured")
		return nil
	}

	var saved, skipDuration, skipEmpty, skipExists int
	for _, meeting := range *meetings {
		if meeting.Duration < r.cfg.Syncable.MinDuration {
			log.Printf("[DEBUG] Skipping meeting %s - duration %d is less than %d", meeting.UUID, meeting.Duration, r.cfg.Syncable.MinDuration)
			skipDuration++
			if r.cfg.Client.DeleteSkipped {
				err := r.client.DeleteMeetingRecordings(meeting.UUID, r.cfg.Client.DeleteDownloaded)
				if err != nil {
					log.Printf("[ERROR] failed to delete meeting %s - %v", meeting.UUID, err)
				}
			}
			continue
		}
		_, err := r.store.GetMeeting(meeting.UUID)
		if err != nil {
			if err == storage.ErrNoRows {

				// filter out meeting recordings that are not supported
				// and sort them by importance
				var important, alternative, optional []model.Record
				for _, record := range meeting.Records {
					if _, ok := r.syncable.Important[record.Type]; ok {
						important = append(important, record)
					}
					if _, ok := r.syncable.Alternative[record.Type]; ok {
						alternative = append(alternative, record)
					}
					if _, ok := r.syncable.Optional[record.Type]; ok {
						optional = append(optional, record)
					}
				}

				meeting.Records = []model.Record{}

				// if there are no important records, use alternative
				if len(important) > 0 {
					meeting.Records = important
				} else if len(alternative) > 0 {
					meeting.Records = alternative
				}
				// use optional if there any
				if len(optional) > 0 {
					meeting.Records = append(meeting.Records, optional...)
				}

				if len(meeting.Records) == 0 {
					log.Printf("[DEBUG] Skipping meeting %s - no records to sync", meeting.UUID)
					skipEmpty++
					if r.cfg.Client.DeleteSkipped {
						err := r.client.DeleteMeetingRecordings(meeting.UUID, r.cfg.Client.DeleteDownloaded)
						if err != nil {
							log.Printf("[ERROR] failed to delete meeting %s - %v", meeting.UUID, err)
						}
					}
					continue
				}

				err := r.store.SaveMeeting(meeting)
				if err != nil {
					return fmt.Errorf("failed to save meeting %s, %v", meeting.UUID, err)
				}
				saved++

				continue
			}
			return fmt.Errorf("failed to get meeting %s, %v", meeting.UUID, err)
		} else {
			skipExists++
		}
	}

	log.Printf("[DEBUG] Saved %d new meetings. Skipped: %d (already saved) %d (too short), %d (empty)", saved, skipExists, skipDuration, skipEmpty)
	return nil
}

func (r *Repository) DownloadJob(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		var queued *model.Record
		var err error
		queued, err = r.store.GetQueuedRecord()
		if err != nil {
			if err == storage.ErrNoRows {
				log.Printf("[DEBUG] No queued records")
				// retry 'failed' records and 'downloading' records - put them back to 'queued'
				err := r.store.ResetFailedRecords()
				if err != nil {
					log.Printf("[ERROR] failed to reset failed records, %v", err)
					continue
				}
				ticker.Reset(1 * time.Minute)
				continue
			}
			log.Printf("[ERROR] failed to get queued records, %v", err)
			continue
		}

		// download the record
		if queued != nil {
			log.Printf("[INFO] Downloading %s record %s meetingId %s", queued.Type, queued.Id, queued.MeetingId)
			downErr := r.DownloadRecord(queued)
			if downErr != nil {
				log.Printf("[ERROR] failed to download record %s - %v", queued.Id, downErr)
				continue
			}
			ticker.Reset(1 * time.Second)
			if downErr == nil && (r.cfg.Client.DeleteDownloaded || r.cfg.Client.TrashDownloaded) {
				err := r.client.DeleteMeetingRecordings(queued.MeetingId, r.cfg.Client.DeleteDownloaded)
				if err != nil {
					log.Printf("[ERROR] failed to delete meeting %s - %v", queued.MeetingId, err)
					continue
				}
			}
		}
	}
}

// DownloadRecord downloads the record from Zoom
func (r *Repository) DownloadRecord(record *model.Record) error {

	token, err := r.client.GetToken()
	if err != nil {
		return err
	}
	r.store.UpdateRecord(record.Id, model.Downloading, "")

	url := record.DownloadURL + "?access_token=" + token.AccessToken
	path := r.cfg.Storage.Repository + "/" + record.Id
	err = r.prepareDestination(path)
	if err != nil {
		return err
	}

	resp, err := grab.Get(path, url)
	if err != nil {
		r.store.UpdateRecord(record.Id, model.Failed, "")
		log.Printf("[DEBUG] Failed to download %s, %v", url, err)
		return err
	}

	log.Printf("[DEBUG] Download saved to %s", resp.Filename)
	r.store.UpdateRecord(record.Id, model.Downloaded, resp.Filename)
	return nil
}

// PrepareDestination creates directory for the downloaded file
func (r *Repository) prepareDestination(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		err := os.MkdirAll(path, os.ModePerm)
		if err != nil {
			return err
		}
	}
	return nil
}
