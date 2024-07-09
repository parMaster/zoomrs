package client

import (
	"cmp"
	"context"
	b64 "encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/parMaster/zoomrs/config"
	"github.com/parMaster/zoomrs/storage/model"
)

type AccessToken struct {
	AccessToken string    `json:"access_token"`
	ExpiresIn   int       `json:"expires_in"`
	Scope       string    `json:"scope"`
	TokenType   string    `json:"token_type"`
	ExpiresAt   time.Time `json:"-"`
}

type ZoomClient struct {
	cfg    *config.Client
	client http.Client
	token  *AccessToken
}

func NewZoomClient(cfg config.Client) *ZoomClient {
	client := http.Client{}

	return &ZoomClient{cfg: &cfg, client: client}
}

// Authorize - get access token
func (z *ZoomClient) Authorize() error {
	bearer := b64.StdEncoding.EncodeToString([]byte(z.cfg.Id + ":" + z.cfg.Secret))

	params := url.Values{}
	params.Add(`grant_type`, `account_credentials`)
	params.Add(`account_id`, z.cfg.AccountId)

	req, err := http.NewRequest(http.MethodPost, "https://zoom.us/oauth/token",
		strings.NewReader(params.Encode()))
	if err != nil {
		return err
	}

	req.Header.Add(`Authorization`, fmt.Sprintf("Basic %s", bearer))
	req.Header.Add(`Host`, "zoom.us")
	req.Header.Add(`Content-Type`, "application/x-www-form-urlencoded")

	resp, err := z.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("[ERROR] failed to close response: %v", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unable to authorize with account id: %s and client id: %s, status %d",
			z.cfg.AccountId, z.cfg.Id, resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(&z.token); err != nil {
		return err
	}

	log.Printf("[DEBUG] token = %v", z.token.AccessToken)

	dur, err := time.ParseDuration(fmt.Sprintf("%ds", z.token.ExpiresIn))
	if err != nil {
		return err
	}
	z.token.ExpiresAt = time.Now().Add(dur).Add(-5 * time.Minute)

	return nil
}

// GetToken - get token, if token is expired, re-authorize
func (z *ZoomClient) GetToken() (*AccessToken, error) {
	var mx sync.Mutex

	mx.Lock()
	defer mx.Unlock()

	if z.token == nil || z.token.ExpiresAt.Before(time.Now()) {
		if err := z.Authorize(); err != nil {
			return nil, err
		}
	}
	return z.token, nil
}

// GetMeetings - get meetings for a given day (daysAgo = 0 for today, 1 for yestarday, etc.)
// Medium rate limit API
func (z *ZoomClient) GetMeetings(ctx context.Context, daysAgo int) ([]model.Meeting, error) {
	from := time.Now().AddDate(0, 0, -1*daysAgo)
	to := time.Now().AddDate(0, 0, -1*daysAgo)

	return z.GetIntervalMeetings(ctx, from, to)
}

// GetIntervalMeetings - get meetings for a from-to interval
// Medium rate limit API
func (z *ZoomClient) GetIntervalMeetings(ctx context.Context, from, to time.Time) ([]model.Meeting, error) {
	_, err := z.GetToken()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("unable to get token"), err)
	}

	params := url.Values{}
	params.Add(`page_size`, "300")
	params.Add(`from`, from.Format("2006-01-02"))
	params.Add(`to`, to.Format("2006-01-02"))
	log.Printf("[DEBUG] initial params = %s", params.Encode())
	req, err := http.NewRequest(http.MethodGet,
		"https://api.zoom.us/v2/users/me/recordings?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}

	req.Header.Add(`Authorization`, fmt.Sprintf("Bearer %s", z.token.AccessToken))
	req.Header.Add(`Host`, "zoom.us")
	req.Header.Add(`Content-Type`, "application/json")

	meetings := []model.Meeting{}

	for {
		log.Printf("[DEBUG] params = %s", params.Encode())
		req.URL.RawQuery = params.Encode()
		resp, err := z.client.Do(req)
		if err != nil {
			return nil, err
		}
		defer func() {
			if err := resp.Body.Close(); err != nil {
				log.Printf("[ERROR] failed to close response: %v", err)
			}
		}()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf(`unable to authorize with account id: %s and client id: %s,
			status %d, message: %s`, z.cfg.AccountId, z.cfg.Id, resp.StatusCode, resp.Body)
		}

		recordings := &model.Recordings{}

		if err := json.NewDecoder(resp.Body).Decode(recordings); err != nil {
			return nil, err
		}

		meetings = append(meetings, recordings.Meetings...)

		if recordings.NextPageToken == `` {
			break
		}
		log.Printf("[DEBUG] recordings.NextPageToken = %v", recordings.NextPageToken)
		params.Set(`next_page_token`, recordings.NextPageToken)

		select {
		case <-time.After(z.cfg.RateLimitingDelay.Medium):
			continue
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	return meetings, nil
}

// GetAllMeetings - get all meetings going from today back in the past by 30 days chunks
// as soon as we hit 2 empty chunks in a row, we assume there are no earlier meetings
func (z *ZoomClient) GetAllMeetings(ctx context.Context) ([]model.Meeting, error) {
	meetings := []model.Meeting{}
	var i, empty int
	for {
		i++
		from := time.Now().AddDate(0, 0, -1*i*30)   // from 30 days ago,    60 days ago, etc.
		to := time.Now().AddDate(0, 0, -1*(i-1)*30) //         to today, to 30 days ago, etc.
		m, err := z.GetIntervalMeetings(ctx, from, to)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("unable to get interval meetings"), err)
		}
		meetings = append(meetings, m...)

		if len(m) == 0 {
			empty++
		} else {
			empty = 0
		}
		if empty >= 2 {
			break
		}
	}
	return meetings, nil
}

// GetAllMeetingsWithRetry - runs GetAllMeetings() with up to 10 retries with increasing delay
func (z *ZoomClient) GetAllMeetingsWithRetry(ctx context.Context) ([]model.Meeting, error) {
	var meetings []model.Meeting
	var err error

	for i := 0; i < 10; i++ {
		meetings, err = z.GetAllMeetings(ctx)
		if err != nil {
			delay := 30 * time.Duration(i) * time.Second
			log.Printf("[ERROR] failed to get meetings, %v, retrying in %s sec", err, delay)

			select {
			case <-time.After(delay):
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		break
	}

	return meetings, err
}

// GetCloudStorageReport - get cloud storage usage
// https://developers.zoom.us/docs/api/rest/reference/zoom-api/methods/#operation/reportCloudRecording
// GET /report/cloud_recording
// - from string - start date in format yyyy-mm-dd
// - to string - end date in format yyyy-mm-dd
// HEAVY rate limit API
func (z *ZoomClient) GetCloudStorageReport(from, to string) (*model.CloudRecordingReport, error) {
	_, err := z.GetToken()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("unable to get token"), err)
	}

	params := url.Values{}
	params.Add(`from`, from)
	params.Add(`to`, to)
	log.Printf("[DEBUG] initial params = %s", params.Encode())
	req, err := http.NewRequest(http.MethodGet, "https://api.zoom.us/v2/report/cloud_recording?"+
		params.Encode(), nil)
	if err != nil {
		return nil, err
	}

	req.Header.Add(`Authorization`, fmt.Sprintf("Bearer %s", z.token.AccessToken))
	req.Header.Add(`Host`, "zoom.us")
	req.Header.Add(`Content-Type`, "application/json")

	resp, err := z.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("[ERROR] failed to close response: %v", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unable to get cloud storage, status %d, message: %s",
			resp.StatusCode, resp.Body)
	}

	report := &model.CloudRecordingReport{}
	if err := json.NewDecoder(resp.Body).Decode(report); err != nil {
		return nil, err
	}

	return report, nil
}

// DeleteMeetingRecordings - delete all recordings for a meeting
// https://developers.zoom.us/docs/api/rest/reference/zoom-api/methods/#operation/recordingDelete
// DELETE /meetings/{meetingId}/recordings
// - meetingId string is meeting.UUID
// - delete bool - true to delete, false to trash
// Light rate limit API
func (z *ZoomClient) DeleteMeetingRecordings(meetingId string, delete bool) error {

	if !z.cfg.DeleteDownloaded && !z.cfg.TrashDownloaded && !z.cfg.DeleteSkipped {
		return errors.New("both delete_downloaded and trash_downloaded are false")
	}

	_, err := z.GetToken()
	if err != nil {
		return errors.Join(fmt.Errorf("unable to get token"), err)
	}

	// @param action string - Default: trash; Allowed: trash | delete
	params := url.Values{}
	action := `trash`
	if delete && z.cfg.DeleteDownloaded {
		action = `delete`
	}
	params.Add(`action`, action)
	// https://developers.zoom.us/docs/meeting-sdk/apis/#operation/recordingDelete
	// If a UUID starts with "/" or contains "//" (example: "/ajXp112QmuoKj4854875=="),
	// you must double encode the UUID before making an API request.
	q := fmt.Sprintf("https://api.zoom.us/v2/meetings/%s/recordings?%s",
		url.QueryEscape(url.QueryEscape(meetingId)), params.Encode())
	log.Printf("[DEBUG] deleting with url = %s, params = %s", q, params.Encode())
	req, err := http.NewRequest(http.MethodDelete, q, nil)
	if err != nil {
		return err
	}

	req.Header.Add(`Authorization`, fmt.Sprintf("Bearer %s", z.token.AccessToken))
	req.Header.Add(`Host`, "zoom.us")
	req.Header.Add(`Content-Type`, "application/json")

	resp, err := z.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("[ERROR] failed to close response: %v", err)
		}
	}()

	// 404 StatusNotFound happens when meeting is already deleted or trashed, so ignore the error
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("unable to delete recordings for meeting id: %s, status %d, message: %s",
			meetingId, resp.StatusCode, resp.Body)
	}

	return nil
}

func (z *ZoomClient) DeleteRecordingsOverCapacity(ctx context.Context, cap model.FileSize,
) (deleted int, err error) {
	if cap == 0 {
		return 0, errors.New("cloud storage capacity is not configured")
	}
	log.Printf("[DEBUG] cloudStorageCap is set to: %s", cap)

	meetings, err := z.GetAllMeetingsWithRetry(ctx)
	if err != nil {
		log.Printf("[ERROR] failed to get meetings, %v", err)
		return 0, errors.Join(fmt.Errorf("unable to GetAllMeetingsWithRetry"), err)
	}

	// Sort meetings by start time - first meeting is the most recent
	slices.SortFunc(meetings, func(i, j model.Meeting) int {
		//     DESC sort by StartTime
		return -1 * cmp.Compare(i.StartTime.UnixNano(), j.StartTime.UnixNano())
	})

	sizeAccum := model.FileSize(0)
	for _, m := range meetings {

		for _, r := range m.Records {
			sizeAccum += r.FileSize
		}

		if sizeAccum > cap {
			log.Printf("[DEBUG] cap reached, cloud used: %s \t deleting: %s", sizeAccum, m.UUID)
			if err := z.DeleteMeetingRecordings(m.UUID, true); err != nil {
				log.Printf("[ERROR] deleting uuid: %s, %v", m.UUID, err)
			} else {
				deleted++
			}

			select {
			case <-time.After(z.cfg.RateLimitingDelay.Light):
				continue
			case <-ctx.Done():
				return deleted, ctx.Err()
			}

		} else {
			log.Printf("[DEBUG] uuid: %s \t cloud used: %s", m.UUID, sizeAccum)
		}
	}

	return
}
