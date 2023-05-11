package main

import (
	"context"
	"fmt"
	"log"
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
	// ToDo: handle 'downloading' and 'failed' records - switch to 'queued'?
	ticker := time.NewTicker(1 * time.Minute)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		var queued *model.Record
		var err error
		if false && r.cfg.Server.Dbg { // debug switch
			queued, err = r.store.GetQueuedRecord(model.AudioOnly)
		} else {
			queued, err = r.store.GetQueuedRecord(model.ChatFile, model.SharedScreenWithGalleryView)
		}
		if err != nil {
			if err == storage.ErrNoRows {
				log.Printf("[DEBUG] No queued records")
				continue
			}
			log.Printf("[ERROR] failed to get queued records, %v", err)
			continue
		}

		// download the record
		if queued != nil {
			log.Printf("[DEBUG] Downloading record %s, meetingId %s, type %s", queued.Id, queued.MeetingId, queued.Type)
			err = r.DownloadRecord(queued)
			if err != nil {
				log.Printf("[ERROR] failed to download record %s, %v", queued.Id, err)
				continue
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

	url := record.DownloadURL + "?access_token=" + token.AccessToken

	r.store.UpdateRecord(record.Id, model.Downloading, "")
	resp, err := grab.Get(r.cfg.Storage.Repository, url)
	// ToDo: handle "server returned 401 Unauthorized" error
	if err != nil {
		r.store.UpdateRecord(record.Id, model.Failed, "")
		return err
	}
	log.Printf("[DEBUG] Download saved to %s", resp.Filename)
	r.store.UpdateRecord(record.Id, model.Downloaded, resp.Filename)
	return nil
}
