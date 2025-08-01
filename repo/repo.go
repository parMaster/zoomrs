package repo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cavaliergopher/grab/v3"
	"github.com/shirou/gopsutil/v4/disk"

	"github.com/parMaster/zoomrs/client"
	"github.com/parMaster/zoomrs/config"
	"github.com/parMaster/zoomrs/storage"
	"github.com/parMaster/zoomrs/storage/model"
)

var (
	ErrNoQueuedRecords = errors.New("no records queued to download")
)

// Client is an interface for the Zoom API client
type Client interface {
	Authorize() error
	GetMeetings(ctx context.Context, daysAgo int) ([]model.Meeting, error)
	GetToken() (*client.AccessToken, error)
	DeleteMeetingRecordings(meetingId string, delete bool) error
}

// syncable is a struct that holds record types grouped by priority for syncing
// these are set in the config file
type syncable struct {
	Important   map[model.RecordType]bool
	Alternative map[model.RecordType]bool
	Optional    map[model.RecordType]bool
}

// Repository does the heavy lifting of syncing meetings and downloading recordings
// it can call the Zoom API client to get meetings, store and mutate them in the database
// according to their download status. Perform the actual download of recordings and
// delete them from Zoom if configured to do so.
type Repository struct {
	store    storage.Storer
	client   Client
	cfg      *config.Parameters
	Syncable syncable
}

func NewRepository(store storage.Storer, client Client, cfg *config.Parameters) *Repository {

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

	return &Repository{store: store, client: client, cfg: cfg, Syncable: sync}
}

// SyncJob is a long running job that tries SyncMeeting on a regular interval
func (r *Repository) SyncJob(ctx context.Context) {

	if len(r.Syncable.Important)+len(r.Syncable.Alternative)+len(r.Syncable.Optional) == 0 {
		log.Printf("[DEBUG] No sync types configured. Sync job will not run")
		return
	}

	ticker := time.NewTicker(60 * time.Minute)
	for {
		meetings, err := r.client.GetMeetings(ctx, 1)
		if err != nil {
			log.Printf("[ERROR] failed to get meetings, %v, retrying in 30 sec", err)

			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
				continue
			}
		}
		log.Printf("[DEBUG] Syncing meetings - %d in feed", len(meetings))

		if err = r.SyncMeetings(ctx, &meetings); err != nil {
			log.Printf("[ERROR] failed to sync meetings, %v, retrying in 30 sec", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
				continue
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// SyncMeeting gets a slice of meetings and saves new ones to the database.
// Filter for MinDuration and RecordType is applied.
func (r *Repository) SyncMeetings(ctx context.Context, meetings *[]model.Meeting) error {
	if len(*meetings) == 0 {
		log.Printf("[DEBUG] No meetings to sync")
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
		_, err := r.store.GetMeeting(ctx, meeting.UUID)
		if err != nil {
			if err == storage.ErrNoRows {

				// filter out meeting recordings that are not supported
				// and sort them by priority
				var important, alternative, optional []model.Record
				for _, record := range meeting.Records {
					if _, ok := r.Syncable.Important[record.Type]; ok {
						important = append(important, record)
					}
					if _, ok := r.Syncable.Alternative[record.Type]; ok {
						alternative = append(alternative, record)
					}
					if _, ok := r.Syncable.Optional[record.Type]; ok {
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

				err := r.store.SaveMeeting(ctx, meeting)
				if err != nil {
					return fmt.Errorf("failed to save meeting %s, %w", meeting.UUID, err)
				}
				saved++

				continue
			}
			return fmt.Errorf("failed to get meeting %s, %w", meeting.UUID, err)
		} else {
			skipExists++
		}
	}

	log.Printf("[INFO] Saved %d new meetings. Skipped: %d (already saved) %d (too short), %d (empty)", saved, skipExists, skipDuration, skipEmpty)
	return nil
}

// DownloadJob is a long running job that tries DownloadOnce on a regular interval
func (r *Repository) DownloadJob(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		err := r.DownloadOnce(ctx)
		if err == ErrNoQueuedRecords {
			ticker.Reset(1 * time.Minute)
			continue
		}
		if err != nil {
			log.Printf("[ERROR] %v", err)
		}
		ticker.Reset(1 * time.Second)
	}
}

// DownloadOnce gets a queued record and downloads it
func (r *Repository) DownloadOnce(ctx context.Context) error {
	queued, err := r.store.GetQueuedRecord(ctx)
	if err == storage.ErrNoRows {
		log.Printf("[DEBUG] No queued records")
		// retry 'failed' records and 'downloading' records - put them back to 'queued'
		err := r.store.ResetFailedRecords(ctx)
		if err != nil {
			return errors.Join(fmt.Errorf("failed to reset failed records"), err)
		}
		return ErrNoQueuedRecords
	}
	if err != nil {
		return errors.Join(fmt.Errorf("failed to get queued records"), err)
	}

	// download the record
	if queued != nil {
		log.Printf("[DEBUG] ↓ %d MB | %s record %s meetingId %s", queued.FileSize/1024/1024, queued.Type, queued.Id, queued.MeetingId)
		log.Printf("[INFO] ↓ %d MB | %s | %s", queued.FileSize/1024/1024, queued.Id, queued.DateTime)
		downErr := r.DownloadRecord(ctx, queued)
		if downErr != nil {
			return errors.Join(fmt.Errorf("download returned error %s", queued.Id), downErr)
		}

		if r.meetingRecordsLoaded(ctx, queued.MeetingId) && (r.cfg.Client.DeleteDownloaded || r.cfg.Client.TrashDownloaded) {
			err := r.client.DeleteMeetingRecordings(queued.MeetingId, r.cfg.Client.DeleteDownloaded)
			if err != nil {
				return errors.Join(fmt.Errorf("failed to delete meeting %s", queued.MeetingId), err)
			}
		}
	}
	return nil
}

// DownloadRecord downloads the record file from the given URL
func (r *Repository) DownloadRecord(ctx context.Context, record *model.Record) error {

	token, err := r.client.GetToken()
	if err != nil {
		return err
	}
	r.store.UpdateRecord(ctx, record.Id, model.StatusDownloading, "")

	path, _ := record.Paths(r.cfg.Storage.Repository)
	if err = r.prepareDestination(path); err != nil {
		return err
	}

	if _, err = r.freeUpSpace(ctx); err != nil {
		log.Printf("[ERROR] failed to free up space, %v", err)
	}

	url := fmt.Sprintf("%s?access_token=%s", record.DownloadURL, token.AccessToken)
	resp, err := grab.Get(path, url)
	if err != nil {
		r.store.UpdateRecord(ctx, record.Id, model.StatusFailed, "")
		return fmt.Errorf("failed to download %s, %v", url, err)
	}

	// check if the download was successful
	if resp.HTTPResponse.StatusCode != 200 {
		r.store.UpdateRecord(ctx, record.Id, model.StatusFailed, "")
		return fmt.Errorf("failed to download %s, status %d", url, resp.HTTPResponse.StatusCode)
	}
	// check if the file is not empty
	if resp.Size() == 0 || resp.Size() != int64(record.FileSize) {
		r.store.UpdateRecord(ctx, record.Id, model.StatusFailed, "")
		return fmt.Errorf("failed to download %s, size %d", url, resp.Size())
	}

	// check if resp.Filename extension matches record.FileExtension
	if resp.Filename[len(resp.Filename)-len(record.FileExtension):] != strings.ToLower(record.FileExtension) {
		r.store.UpdateRecord(ctx, record.Id, model.StatusFailed, "")
		return fmt.Errorf("failed to download %s, extension %s", url, resp.Filename[len(resp.Filename)-len(record.FileExtension):])
	}

	log.Printf("[DEBUG] Download saved to %s", resp.Filename)
	if err := r.store.UpdateRecord(ctx, record.Id, model.StatusDownloaded, resp.Filename); err != nil {
		return fmt.Errorf("failed to update record %s, %w", record.Id, err)
	}

	return nil
}

// PrepareDestination creates directory for the downloaded file
func (r *Repository) prepareDestination(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.MkdirAll(path, os.ModePerm); err != nil {
			return err
		}
	}
	return nil
}

// meetingRecordsLoaded returns true if all records for the meeting are loaded
func (r *Repository) meetingRecordsLoaded(ctx context.Context, meetingId string) bool {
	records, err := r.store.GetRecords(ctx, meetingId)
	if err != nil {
		return false
	}
	for _, record := range records {
		if record.Status != model.StatusDownloaded {
			return false
		}
	}
	return true
}

// CleanupJob is a long running job that tries to delete recordings from Zoom Cloud if they are downloaded.
// It calls /meetingsLoaded POST API of each instance listed in cfg.Commander.Instances to ask if the list of
// meetings (uuids) recordings are downloaded. If all instances return "ok", the recordings are deleted.
func (r *Repository) CleanupJob(ctx context.Context, daysAgo int) {
	var retry int
	for {
		meetings, err := r.client.GetMeetings(ctx, daysAgo)
		if err != nil {
			log.Printf("[ERROR] failed to get meetings, %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(1 * time.Minute):
				continue
			}
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
				select {
				case <-ctx.Done():
					return
				case <-time.After(1 * time.Minute):
				}
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
					select {
					case <-ctx.Done():
						return
					case <-time.After(r.cfg.Client.RateLimitingDelay.Light):
						continue
					}
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

	if len(r.cfg.Commander.Instances) == 0 {
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
		url := fmt.Sprintf("%s/meetingsLoaded/%s", instance, r.cfg.Server.AccessKeySalt)
		resp, err := http.Post(url, "application/json", bytes.NewBuffer(body))
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
		log.Printf("[INFO] %s/meetingsLoaded result: %v", instance, result)
		if result.Result != "ok" {
			return false, nil
		}
	}
	// all instances returned "ok"
	return true, nil
}

// CheckConsistency checks if all downloaded files exist and have correct size
// returns number of checked files and error
func (r *Repository) CheckConsistency(ctx context.Context) (checked int, result error) {
	recs, err := r.store.GetRecordsByStatus(ctx, model.StatusDownloaded)
	if err != nil {
		return 0, fmt.Errorf("failed to get records by status %s: %w", model.StatusDownloaded, err)
	}

	for _, rec := range recs {
		// check if file with path exists
		if _, err := os.Stat(rec.FilePath); os.IsNotExist(err) {
			log.Printf("File does not exist: %s", rec.FilePath)
			errors.Join(result, fmt.Errorf("file does not exist: %s", rec.FilePath))
		}
		// check if file is not empty
		if info, err := os.Stat(rec.FilePath); err == nil {
			if info.Size() == 0 {
				log.Printf("File is empty: %s", rec.FilePath)
				errors.Join(result, fmt.Errorf("file is empty: %s", rec.FilePath))
			}
		}
		// check if file size matches record.FileSize
		if info, err := os.Stat(rec.FilePath); err == nil {
			if info.Size() != int64(rec.FileSize) {
				log.Printf("File size does not match: %s", rec.FilePath)
				errors.Join(result, fmt.Errorf("file size does not match: %s", rec.FilePath))
			}
		}
		checked++
	}
	log.Printf("Checked files: %d", checked)
	return
}

// freeUpSpace deletes downloaded files if there is less than cfg.Storage.KeepFreeSpace bytes free
// on the drive where cfg.Storage.Repository located
func (r *Repository) freeUpSpace(ctx context.Context) (deleted int, result error) {
	usage, err := disk.Usage(r.cfg.Storage.Repository)
	if err != nil {
		return 0, fmt.Errorf("failed to get disk usage: %w", err)
	}
	if usage.Free > uint64(r.cfg.Storage.KeepFreeSpace) {
		log.Printf("[DEBUG]Free space Available/Required: %d/%d bytes (%s/ %s) no need to free up space.",
			usage.Free,
			r.cfg.Storage.KeepFreeSpace,
			model.FileSize(usage.Free),
			model.FileSize(r.cfg.Storage.KeepFreeSpace),
		)
		return 0, nil
	}
	log.Printf("[DEBUG] Free space Available/Required: %d/%d bytes (%s/ %s), %d bytes (%s) over the limit", usage.Free, r.cfg.Storage.KeepFreeSpace, model.FileSize(usage.Free), model.FileSize(r.cfg.Storage.KeepFreeSpace), r.cfg.Storage.KeepFreeSpace-usage.Free, model.FileSize(r.cfg.Storage.KeepFreeSpace-usage.Free))

	recs, err := r.store.GetRecordsByStatus(ctx, model.StatusDownloaded)
	if err != nil {
		return deleted, fmt.Errorf("failed to get downloaded records %w", err)
	}
	for _, rec := range recs {
		usage, err = disk.Usage(r.cfg.Storage.Repository)
		if err != nil {
			return deleted, fmt.Errorf("failed to get disk usage: %w", err)
		}
		if usage.Free > uint64(r.cfg.Storage.KeepFreeSpace) {
			log.Printf("[INFO] Free space is %d (%d bytes), deleted %d records", model.FileSize(usage.Free), usage.Free, deleted)
			break
		}

		recFolder, dateFolder := rec.Paths(r.cfg.Storage.Repository)
		if _, err := os.Stat(recFolder); err != nil {
			log.Printf("[ERROR] %s does not exist, skipping", recFolder)
			continue
		}
		if err := os.RemoveAll(recFolder); err != nil {
			log.Printf("[DEBUG] Failed to delete %s, %v", recFolder, err)
			errors.Join(result, fmt.Errorf("failed to delete %s, %v; ", recFolder, err))
		} else {
			deleted++
			log.Printf("[DEBUG] Deleted %s", recFolder)
			r.store.UpdateRecord(ctx, rec.Id, model.StatusDeleted, "")
		}

		// if dateFolder is empty, delete it
		if files, err := os.ReadDir(dateFolder); err != nil {
			log.Printf("[ERROR] Failed to read %s, %v", dateFolder, err)
		} else {
			if len(files) == 0 {
				if err := os.Remove(dateFolder); err != nil {
					log.Printf("[ERROR] Failed to delete %s, %v", dateFolder, err)
				} else {
					log.Printf("[DEBUG] Deleted %s", dateFolder)
				}
			}
		}

	}
	return
}

// GetStats - returns statistics about the repository. d is a divider for the file size: 'K', 'M', 'G'.
// returns map[day]size in d units (K, M, G) for all downloaded records grouped by day. day is in format YYYY-MM-DD
// if d is not one of the supported dividers, the size is returned in bytes
func (r *Repository) GetStats(ctx context.Context, d rune) (stats map[string]int64, err error) {
	recs, err := r.store.GetRecordsByStatus(ctx, model.StatusDownloaded)
	if recs == nil {
		return nil, fmt.Errorf("failed to get records by status %s: %w", model.StatusDownloaded, err)
	}

	// group stats by day, calculate sum of the file size
	resp := map[string]int64{}
	for _, rec := range recs {
		day := rec.DateTime[:10]
		if _, ok := resp[day]; !ok {
			resp[day] = 0
		}
		resp[day] += int64(rec.FileSize)
	}

	dividers := map[rune]int64{
		'K': 1024,
		'M': 1024 * 1024,
		'G': 1024 * 1024 * 1024,
	}
	divider, ok := dividers[d]
	if !ok {
		divider = 1
	}
	for k, v := range resp {
		resp[k] = v / divider
	}

	return resp, nil
}
