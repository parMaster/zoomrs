package repo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cavaliergopher/grab/v3"

	"github.com/parMaster/zoomrs/client"
	"github.com/parMaster/zoomrs/config"
	"github.com/parMaster/zoomrs/storage"
	"github.com/parMaster/zoomrs/storage/model"
)

type Client interface {
	Authorize() error
	GetMeetings(daysAgo int) ([]model.Meeting, error)
	GetToken() (*client.AccessToken, error)
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
		meetings, err := r.client.GetMeetings(1)
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

	log.Printf("[INFO] Saved %d new meetings. Skipped: %d (already saved) %d (too short), %d (empty)", saved, skipExists, skipDuration, skipEmpty)
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
			log.Printf("[DEBUG] ↓ %d MB | %s record %s meetingId %s", queued.FileSize/1024/1024, queued.Type, queued.Id, queued.MeetingId)
			log.Printf("[INFO] ↓ %d MB | %s", queued.FileSize/1024/1024, queued.Id)
			downErr := r.DownloadRecord(queued)
			if downErr != nil {
				log.Printf("[ERROR] download returned error: %s - %v", queued.Id, downErr)
				continue
			}
			ticker.Reset(1 * time.Second)
			if downErr == nil && r.meetingRecordsLoaded(queued.MeetingId) && (r.cfg.Client.DeleteDownloaded || r.cfg.Client.TrashDownloaded) {
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

	path := r.cfg.Storage.Repository + "/" + record.DateTime[:10] + "/" + record.Id
	err = r.prepareDestination(path)
	if err != nil {
		return err
	}

	url := record.DownloadURL + "?access_token=" + token.AccessToken
	resp, err := grab.Get(path, url)
	if err != nil {
		r.store.UpdateRecord(record.Id, model.Failed, "")
		return fmt.Errorf("failed to download %s, %v", url, err)
	}

	// check if the download was successful
	if resp.HTTPResponse.StatusCode != 200 {
		r.store.UpdateRecord(record.Id, model.Failed, "")
		return fmt.Errorf("failed to download %s, status %d", url, resp.HTTPResponse.StatusCode)
	}
	// check if the file is not empty
	if resp.Size() == 0 || resp.Size() != int64(record.FileSize) {
		r.store.UpdateRecord(record.Id, model.Failed, "")
		return fmt.Errorf("failed to download %s, size %d", url, resp.Size())
	}

	// check if resp.Filename extension matches record.FileExtension
	if resp.Filename[len(resp.Filename)-len(record.FileExtension):] != strings.ToLower(record.FileExtension) {
		r.store.UpdateRecord(record.Id, model.Failed, "")
		return fmt.Errorf("failed to download %s, extension %s", url, resp.Filename[len(resp.Filename)-len(record.FileExtension):])
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

// meetingRecordsLoaded returns true if all records for the meeting are loaded
func (r *Repository) meetingRecordsLoaded(meetingId string) bool {
	records, err := r.store.GetRecords(meetingId)
	if err != nil {
		return false
	}
	for _, record := range records {
		if record.Status != model.Downloaded {
			return false
		}
	}
	return true
}

func (r *Repository) CleanupJob(ctx context.Context, daysAgo int) {
	var retry int
	for {
		meetings, err := r.client.GetMeetings(daysAgo)
		if err != nil {
			log.Printf("[ERROR] failed to get meetings, %v", err)
			time.Sleep(1 * time.Minute)
			continue
		}
		log.Printf("[INFO] Cleaning up meetings - %d in feed", len(meetings))
		if len(meetings) == 0 {
			log.Printf("[INFO] No meetings to cleanup %d days ago", daysAgo)
			return
		}

		uuids := []string{}
		for _, meeting := range meetings {
			uuids = append(uuids, meeting.UUID)
		}

		loaded, err := r.requestMeetingsLoaded(uuids)
		if err != nil {
			log.Printf("[ERROR] meetingsLoaded returned error: %v", err)
			select {
			case <-ctx.Done():
				return
			default:
				retry++
				if retry > 10 {
					log.Printf("[ERROR] retry limit reached (10)")
					return
				}
				log.Printf("[INFO] (%d) retrying after 1 minute", retry)
				time.Sleep(time.Minute)
			}
			continue
		}

		if loaded {
			var deleted int
			for _, meeting := range meetings {
				select {
				case <-ctx.Done():
					log.Printf("[DEBUG] Deleting canceled")
					return
				default:
					log.Printf("[DEBUG] Deleting meeting %s", meeting.UUID)
					err := r.client.DeleteMeetingRecordings(meeting.UUID, r.cfg.Client.DeleteDownloaded)
					if err != nil {
						log.Printf("[ERROR] failed to delete meeting %s - %v", meeting.UUID, err)
					} else {
						deleted++
					}
					time.Sleep(1 * time.Second) // avoid rate limit
				}
			}
			log.Printf("[INFO] Deleted %d out of %d meetings", deleted, len(meetings))
		} else {
			log.Printf("[INFO] Deleting skipped - not all meetings are loaded")
		}
		return
	}
}

// requestMeetingsLoaded calls /meetingsLoaded POST API of each instance listed in cfg.Commander.Instances
// to ask if the list of meetings (uuids) recordings are downloaded
func (r *Repository) requestMeetingsLoaded(meetings []string) (loaded bool, err error) {

	if r.cfg.Commander.Instances == nil || len(r.cfg.Commander.Instances) == 0 {
		return false, fmt.Errorf("no instances configured")
	}

	req := struct {
		Meetings []string `json:"meetings"`
	}{Meetings: meetings}

	body, err := json.Marshal(req)
	if err != nil {
		return false, fmt.Errorf("failed to marshal meetings, %v", err)
	}

	for _, instance := range r.cfg.Commander.Instances {
		resp, err := http.Post(instance+"/meetingsLoaded/"+r.cfg.Server.AccessKeySalt, "application/json", bytes.NewBuffer(body))
		if err != nil {
			return false, fmt.Errorf("failed to post meetingsLoaded to %s, %v", instance, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return false, fmt.Errorf("failed to post meetingsLoaded to %s, status %d", instance, resp.StatusCode)
		}
		var result struct {
			Result string `json:"result"`
		}
		err = json.NewDecoder(resp.Body).Decode(&result)
		if err != nil {
			return false, fmt.Errorf("failed to decode response body, %v", err)
		}
		if result.Result != "ok" {
			return false, nil
		}
	}
	// all instances returned "ok"
	return true, nil
}
