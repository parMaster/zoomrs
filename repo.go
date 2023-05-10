package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/parMaster/zoomrs/storage"
	"github.com/parMaster/zoomrs/storage/model"
)

/*
 Repository will implement such features as:
 - provide the service to sync meetings with the storage
 - in the future: provide some kind of functions to catch the webhooks from Zoom and update the storage
*/

type Client interface {
	Authorize() error
	GetMeetings() ([]model.Meeting, error)
	GetToken() (*AccessToken, error)
}

type Repository struct {
	store  storage.Storer
	client Client
}

func NewRepository(store storage.Storer, client Client) *Repository {
	return &Repository{store: store, client: client}
}

func (r *Repository) Run(ctx context.Context) {
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
