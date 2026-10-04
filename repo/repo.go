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
	"slices"
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
	GetIntervalMeetings(ctx context.Context, from, to time.Time) ([]model.Meeting, error)
	GetToken() (*client.AccessToken, error)
	RefreshToken(rejected *client.AccessToken) (*client.AccessToken, error)
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
	diskFree func(path string) (uint64, error) // free bytes on the drive holding path; tests swap it for a fake
	// asks other instances about loaded meetings; tests swap it. The timeout matches the
	// service's WriteTimeout - the server cuts a longer answer anyway.
	httpClient *http.Client
	// pause before the cleanup lists meetings or asks an instance again; tests shorten it
	retryWait time.Duration
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

	return &Repository{store: store, client: client, cfg: cfg, Syncable: sync, diskFree: diskFree,
		httpClient: &http.Client{Timeout: 30 * time.Second}, retryWait: time.Minute}
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
	return r.downloadOnce(ctx, r.store.GetQueuedRecord, r.store.ResetFailedRecords)
}

// failedRetryAge is how old a failed record can be and still get another attempt
const failedRetryAge = 3 * 24 * time.Hour

// Scope limits one run to the meetings it listed and remembers which records the run
// already put back in the queue
type Scope struct {
	meetings    []string
	failedSince time.Time
	requeued    []string
}

// NewScope starts a run over the given meetings
func NewScope(meetingUUIDs []string) *Scope {
	return &Scope{meetings: meetingUUIDs, failedSince: time.Now().Add(-failedRetryAge)}
}

// DownloadOnceOf is DownloadOnce limited to the records of the scope's meetings: records of
// any other meeting are neither downloaded nor requeued. An empty scope downloads nothing.
// Stuck records are requeued at any age, failed ones only if they are recent. A record is
// requeued once per scope, so one that fails every time can't keep the run going.
func (r *Repository) DownloadOnceOf(ctx context.Context, scope *Scope) error {
	return r.downloadOnce(ctx,
		func(ctx context.Context) (*model.Record, error) {
			return r.store.GetQueuedRecordOf(ctx, scope.meetings)
		},
		func(ctx context.Context) error {
			ids, err := r.store.ResetFailedRecordsOf(ctx, scope.meetings, scope.failedSince, scope.requeued)
			scope.requeued = append(scope.requeued, ids...)
			return err
		},
	)
}

// FailedRecordsOf returns the records of the scope's meetings that are in the failed state
func (r *Repository) FailedRecordsOf(ctx context.Context, scope *Scope) ([]model.Record, error) {
	failed, err := r.store.GetRecordsByStatus(ctx, model.StatusFailed)
	if err != nil {
		return nil, fmt.Errorf("failed to get records by status %s: %w", model.StatusFailed, err)
	}
	var recs []model.Record
	for _, rec := range failed {
		if slices.Contains(scope.meetings, rec.MeetingId) {
			recs = append(recs, rec)
		}
	}
	return recs, nil
}

func (r *Repository) downloadOnce(ctx context.Context,
	nextQueued func(context.Context) (*model.Record, error), requeue func(context.Context) error) error {
	queued, err := nextQueued(ctx)
	if err == storage.ErrNoRows {
		log.Printf("[DEBUG] No queued records")
		// retry 'failed' records and 'downloading' records - put them back to 'queued'
		err := requeue(ctx)
		if err != nil {
			return errors.Join(fmt.Errorf("failed to reset failed records"), err)
		}
		return ErrNoQueuedRecords
	}
	if err != nil {
		return errors.Join(fmt.Errorf("failed to get queued records"), err)
	}

	// download the record. The meeting stays in the cloud: only the cleanup removes it,
	// once every instance confirms its copy.
	if queued != nil {
		log.Printf("[DEBUG] ↓ %d MB | %s record %s meetingId %s", queued.FileSize/1024/1024, queued.Type, queued.Id, queued.MeetingId)
		log.Printf("[INFO] ↓ %d MB | %s | %s", queued.FileSize/1024/1024, queued.Id, queued.DateTime)
		downErr := r.DownloadRecord(ctx, queued)
		if downErr != nil {
			return errors.Join(fmt.Errorf("download returned error %s", queued.Id), downErr)
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
	if err := r.store.UpdateRecord(ctx, record.Id, model.StatusDownloading, ""); err != nil {
		log.Printf("[ERROR] failed to update record %s status to downloading, %v", record.Id, err)
	}

	path, _ := record.Paths(r.cfg.Storage.Repository)
	if err = r.prepareDestination(path); err != nil {
		return err
	}

	if _, err = r.freeUpSpace(ctx); err != nil {
		log.Printf("[ERROR] failed to free up space, %v", err)
	}

	// the token goes in the query, so errors name record.DownloadURL and never the full URL
	url := record.DownloadURL
	resp, err := grab.Get(path, downloadURL(url, token))
	// Zoom can reject a token before it expires, so a 401 gets one more attempt with a new one
	if errors.Is(err, grab.StatusCodeError(http.StatusUnauthorized)) {
		log.Printf("[WARN] download %s got status 401, retrying with a new token", url)
		if token, err = r.client.RefreshToken(token); err != nil {
			r.markDownloadFailed(ctx, record.Id)
			return fmt.Errorf("failed to download %s, status 401, unable to refresh token: %w", url, err)
		}
		resp, err = grab.Get(path, downloadURL(url, token))
	}
	if err != nil {
		r.markDownloadFailed(ctx, record.Id)
		// transport errors quote the request URL, token included
		return fmt.Errorf("failed to download %s, %s", url, strings.ReplaceAll(err.Error(), token.AccessToken, "******"))
	}

	// check if the download was successful
	if resp.HTTPResponse.StatusCode != 200 {
		r.markDownloadFailed(ctx, record.Id)
		return fmt.Errorf("failed to download %s, status %d", url, resp.HTTPResponse.StatusCode)
	}
	// check if the file is not empty
	if resp.Size() == 0 || resp.Size() != int64(record.FileSize) {
		r.markDownloadFailed(ctx, record.Id)
		return fmt.Errorf("failed to download %s, size %d", url, resp.Size())
	}

	// check if resp.Filename extension matches record.FileExtension
	if resp.Filename[len(resp.Filename)-len(record.FileExtension):] != strings.ToLower(record.FileExtension) {
		r.markDownloadFailed(ctx, record.Id)
		return fmt.Errorf("failed to download %s, extension %s", url, resp.Filename[len(resp.Filename)-len(record.FileExtension):])
	}

	log.Printf("[DEBUG] Download saved to %s", resp.Filename)
	if err := r.store.UpdateRecord(ctx, record.Id, model.StatusDownloaded, resp.Filename); err != nil {
		return fmt.Errorf("failed to update record %s, %w", record.Id, err)
	}

	return nil
}

// downloadURL adds the access token Zoom expects on a recording's download link
func downloadURL(url string, token *client.AccessToken) string {
	return fmt.Sprintf("%s?access_token=%s", url, token.AccessToken)
}

// markDownloadFailed marks a record as failed; the caller already has a more specific
// error to return, so this only logs if the status update itself fails.
func (r *Repository) markDownloadFailed(ctx context.Context, recordId string) {
	if err := r.store.UpdateRecord(ctx, recordId, model.StatusFailed, ""); err != nil {
		log.Printf("[ERROR] failed to update record %s status to failed, %v", recordId, err)
	}
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

// cleanupRetries is how many more times an instance that gave no answer is asked
const cleanupRetries = 10

// CleanupJob removes the recordings of the meetings from the from-to interval from Zoom Cloud.
// It calls /meetingsLoaded POST API of each instance listed in cfg.Commander.Instances and removes
// only the meetings every instance confirms as downloaded. With force no instance is asked and
// every meeting of the interval is removed.
func (r *Repository) CleanupJob(ctx context.Context, from, to time.Time, force bool) {
	var meetings []model.Meeting
	for {
		var err error
		if meetings, err = r.client.GetIntervalMeetings(ctx, from, to); err == nil {
			break
		}
		log.Printf("[ERROR] failed to get meetings, %v", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(r.retryWait):
		}
	}
	interval := from.Format(time.DateOnly) + " - " + to.Format(time.DateOnly)
	log.Printf("[INFO] Cleaning up meetings - %d in feed (%s)", len(meetings), interval)
	if len(meetings) == 0 {
		log.Printf("[INFO] No meetings to cleanup (%s)", interval)
		return
	}

	if !force {
		uuids := make([]string, len(meetings))
		for i, meeting := range meetings {
			uuids[i] = meeting.UUID
		}
		confirmed, ok := r.confirmedByAll(ctx, uuids)
		if !ok {
			return
		}
		var loaded []model.Meeting
		for _, meeting := range meetings {
			if confirmed[meeting.UUID] {
				loaded = append(loaded, meeting)
			}
		}
		log.Printf("[INFO] %d of %d meetings are loaded on every instance, the rest stay in the cloud", len(loaded), len(meetings))
		meetings = loaded
	}

	var deleted int
	for i, meeting := range meetings {
		if i > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(r.cfg.Client.RateLimitingDelay.Light):
			}
		}
		if ctx.Err() != nil {
			log.Printf("[DEBUG] Deleting canceled")
			return
		}
		log.Printf("[DEBUG] Deleting meeting %s", meeting.UUID)
		if err := r.client.DeleteMeetingRecordings(meeting.UUID, r.cfg.Client.DeleteDownloaded); err != nil {
			log.Printf("[ERROR] failed to delete meeting %s - %v", meeting.UUID, err)
			continue
		}
		deleted++
	}
	log.Printf("[INFO] Deleted %d out of %d meetings", deleted, len(meetings))
}

// confirmedByAll asks every configured instance which of the meetings it has loaded and returns
// the ones all of them confirm. ok is false when that can't be known, and then nothing may be
// removed: no instances are configured, or one still gave no answer after the retries.
// Instances that answered are not asked again, so the wait does not grow with their number.
func (r *Repository) confirmedByAll(ctx context.Context, uuids []string) (confirmed map[string]bool, ok bool) {
	pending := r.cfg.Commander.Instances
	if len(pending) == 0 {
		log.Printf("[WARN] no instances configured to confirm the meetings are loaded, nothing is deleted")
		return nil, false
	}

	body, err := json.Marshal(struct {
		Meetings []string `json:"meetings"`
	}{Meetings: uuids})
	if err != nil {
		log.Printf("[ERROR] failed to marshal meetings, %v", err)
		return nil, false
	}

	var answers []map[string]bool
	for retry := 0; ; retry++ {
		var unanswered []string
		for _, instance := range pending {
			loaded, err := r.instanceMeetingsLoaded(ctx, instance, body, uuids)
			if err != nil {
				log.Printf("[ERROR] meetingsLoaded returned error: %v", err)
				unanswered = append(unanswered, instance)
				continue
			}
			answers = append(answers, loaded)
		}
		if len(unanswered) == 0 {
			break
		}
		if retry == cleanupRetries {
			log.Printf("[ERROR] retry limit reached (%d), no answer from %v, nothing is deleted", cleanupRetries, unanswered)
			return nil, false
		}
		log.Printf("[INFO] (%d) asking %v again in %v", retry+1, unanswered, r.retryWait)
		select {
		case <-ctx.Done():
			return nil, false
		case <-time.After(r.retryWait):
		}
		pending = unanswered
	}

	confirmed = make(map[string]bool)
	for _, uuid := range uuids {
		if !slices.ContainsFunc(answers, func(loaded map[string]bool) bool { return !loaded[uuid] }) {
			confirmed[uuid] = true
		}
	}
	return confirmed, true
}

// instanceMeetingsLoaded asks one instance which of the meetings it has loaded; a function
// of its own so the response body is closed before the next instance is asked
func (r *Repository) instanceMeetingsLoaded(ctx context.Context, instance string, body []byte, uuids []string) (map[string]bool, error) {
	url := fmt.Sprintf("%s/meetingsLoaded/%s", instance, r.cfg.Server.AccessKeySalt)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to post meetingsLoaded to %s, %v", instance, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to post meetingsLoaded to %s, %v", instance, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to post meetingsLoaded to %s, status %d", instance, resp.StatusCode)
	}
	var result struct {
		Result string   `json:"result"`
		Loaded []string `json:"loaded"`
	}
	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		return nil, fmt.Errorf("failed to decode response body of %s, %v", instance, err)
	}

	loaded := make(map[string]bool)
	switch {
	case result.Loaded != nil:
		for _, uuid := range result.Loaded {
			loaded[uuid] = true
		}
	case result.Result == "ok":
		// an older instance sends no list and answers for all the meetings at once
		for _, uuid := range uuids {
			loaded[uuid] = true
		}
	}
	log.Printf("[INFO] %s/meetingsLoaded result: %s, %d of %d loaded", instance, result.Result, len(loaded), len(uuids))
	return loaded, nil
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
			result = errors.Join(result, fmt.Errorf("file does not exist: %s", rec.FilePath))
		}
		// check if file is not empty
		if info, err := os.Stat(rec.FilePath); err == nil {
			if info.Size() == 0 {
				log.Printf("File is empty: %s", rec.FilePath)
				result = errors.Join(result, fmt.Errorf("file is empty: %s", rec.FilePath))
			}
		}
		// check if file size matches record.FileSize
		if info, err := os.Stat(rec.FilePath); err == nil {
			if info.Size() != int64(rec.FileSize) {
				log.Printf("File size does not match: %s", rec.FilePath)
				result = errors.Join(result, fmt.Errorf("file size does not match: %s", rec.FilePath))
			}
		}
		checked++
	}
	log.Printf("Checked files: %d", checked)
	return
}

func diskFree(path string) (uint64, error) {
	usage, err := disk.Usage(path)
	if err != nil {
		return 0, err
	}
	return usage.Free, nil
}

// freeUpSpace deletes downloaded files if there is less than cfg.Storage.KeepFreeSpace bytes free
// on the drive where cfg.Storage.Repository located
func (r *Repository) freeUpSpace(ctx context.Context) (deleted int, result error) {
	free, err := r.diskFree(r.cfg.Storage.Repository)
	if err != nil {
		return 0, fmt.Errorf("failed to get disk usage: %w", err)
	}
	if free > uint64(r.cfg.Storage.KeepFreeSpace) {
		log.Printf("[DEBUG]Free space Available/Required: %d/%d bytes (%s/ %s) no need to free up space.",
			free,
			r.cfg.Storage.KeepFreeSpace,
			model.FileSize(free),
			model.FileSize(r.cfg.Storage.KeepFreeSpace),
		)
		return 0, nil
	}
	log.Printf("[DEBUG] Free space Available/Required: %d/%d bytes (%s/ %s), %d bytes (%s) over the limit", free, r.cfg.Storage.KeepFreeSpace, model.FileSize(free), model.FileSize(r.cfg.Storage.KeepFreeSpace), r.cfg.Storage.KeepFreeSpace-free, model.FileSize(r.cfg.Storage.KeepFreeSpace-free))

	recs, err := r.store.GetRecordsByStatus(ctx, model.StatusDownloaded)
	if err != nil {
		return deleted, fmt.Errorf("failed to get downloaded records %w", err)
	}
	for _, rec := range recs {
		free, err = r.diskFree(r.cfg.Storage.Repository)
		if err != nil {
			return deleted, fmt.Errorf("failed to get disk usage: %w", err)
		}
		if free > uint64(r.cfg.Storage.KeepFreeSpace) {
			log.Printf("[INFO] Free space is %s (%d bytes), deleted %d records", model.FileSize(free), free, deleted)
			break
		}

		recFolder, dateFolder := rec.Paths(r.cfg.Storage.Repository)
		if _, err := os.Stat(recFolder); err != nil {
			log.Printf("[ERROR] %s does not exist, skipping", recFolder)
			continue
		}
		if err := os.RemoveAll(recFolder); err != nil {
			log.Printf("[DEBUG] Failed to delete %s, %v", recFolder, err)
			result = errors.Join(result, fmt.Errorf("failed to delete %s, %v; ", recFolder, err))
		} else {
			deleted++
			log.Printf("[DEBUG] Deleted %s", recFolder)
			if err := r.store.UpdateRecord(ctx, rec.Id, model.StatusDeleted, ""); err != nil {
				log.Printf("[ERROR] failed to update record %s status to deleted, %v", rec.Id, err)
			}
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
	if err != nil {
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
