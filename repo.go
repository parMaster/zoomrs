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

type Repository struct {
	store  storage.Storer
	client Client
	cfg    config.Parameters
}

func NewRepository(store storage.Storer, client Client, cfg config.Parameters) *Repository {
	return &Repository{store: store, client: client, cfg: cfg}
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

	var saved int
	for _, meeting := range *meetings {
		_, err := r.store.GetMeeting(meeting.UUID)
		if err != nil {
			if err == storage.ErrNoRows {
				err := r.store.SaveMeeting(meeting)
				if err != nil {
					return fmt.Errorf("failed to save meeting %s, %v", meeting.UUID, err)
				}
				saved++
				continue
			}
			return fmt.Errorf("failed to get meeting %s, %v", meeting.UUID, err)
		}
	}

	log.Printf("[DEBUG] Saved %d new meetings", saved)
	return nil
}

func (r *Repository) DownloadJob(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		var queued *model.Record
		var err error
		queued, err = r.store.GetQueuedRecord(model.ChatFile, model.SharedScreenWithGalleryView)
		if err != nil {
			if err == storage.ErrNoRows {
				log.Printf("[DEBUG] No queued records")
				// retry 'failed' records and 'downloading' records - put them back to 'queued'
				err := r.store.ResetFailedRecords()
				if err != nil {
					log.Printf("[ERROR] failed to reset failed records, %v", err)
					continue
				}
				continue
			}
			log.Printf("[ERROR] failed to get queued records, %v", err)
			continue
		}

		// download the record
		if queued != nil {
			log.Printf("[INFO] Downloading %s record %s meetingId %s", queued.Type, queued.Id, queued.MeetingId)
			err = r.DownloadRecord(queued)
			if err != nil {
				log.Printf("[ERROR] failed to download record %s - %v", queued.Id, err)
				continue
			}
			if r.cfg.Client.DeleteDownloaded || r.cfg.Client.TrashDownloaded { // debug switch
				err := r.client.DeleteMeetingRecordings(queued.MeetingId, true)
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
		log.Printf("[DEBUG] Creating directory %s", path)
		err := os.MkdirAll(path, os.ModePerm)
		if err != nil {
			return err
		}
	}
	return nil
}
