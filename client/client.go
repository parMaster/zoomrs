package client

import (
	"cmp"
	"context"
	b64 "encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

const (
	defaultAuthURL = "https://zoom.us"
	defaultAPIURL  = "https://api.zoom.us/v2"
)

type ZoomClient struct {
	cfg    *config.Client
	client http.Client
	// mx guards token: the service's jobs share one client
	mx      sync.Mutex
	token   *AccessToken
	authURL string
	apiURL  string
}

// Option configures a ZoomClient
type Option func(*ZoomClient)

// WithBaseURLs overrides the Zoom OAuth and API base URLs, e.g. to point the client at a test server
func WithBaseURLs(authURL, apiURL string) Option {
	return func(z *ZoomClient) {
		z.authURL = authURL
		z.apiURL = apiURL
	}
}

func NewZoomClient(cfg config.Client, opts ...Option) *ZoomClient {
	z := &ZoomClient{cfg: &cfg, client: http.Client{}, authURL: defaultAuthURL, apiURL: defaultAPIURL}
	for _, opt := range opts {
		opt(z)
	}
	return z
}

// Authorize - get access token
func (z *ZoomClient) Authorize() error {
	z.mx.Lock()
	defer z.mx.Unlock()

	return z.authorize()
}

// authorize requests a new token and stores it; the caller holds z.mx
func (z *ZoomClient) authorize() error {
	bearer := b64.StdEncoding.EncodeToString([]byte(z.cfg.Id + ":" + z.cfg.Secret))

	params := url.Values{}
	params.Add(`grant_type`, `account_credentials`)
	params.Add(`account_id`, z.cfg.AccountId)

	req, err := http.NewRequest(http.MethodPost, z.authURL+"/oauth/token",
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

	// a fresh value, so callers still holding the previous token are not disturbed
	token := &AccessToken{}
	if err := json.NewDecoder(resp.Body).Decode(token); err != nil {
		return err
	}

	dur, err := time.ParseDuration(fmt.Sprintf("%ds", token.ExpiresIn))
	if err != nil {
		return err
	}
	token.ExpiresAt = time.Now().Add(dur).Add(-5 * time.Minute)
	z.token = token

	return nil
}

// GetToken - get token, if token is expired, re-authorize
func (z *ZoomClient) GetToken() (*AccessToken, error) {
	z.mx.Lock()
	defer z.mx.Unlock()

	if z.token == nil || z.token.ExpiresAt.Before(time.Now()) {
		if err := z.authorize(); err != nil {
			return nil, err
		}
	}
	return z.token, nil
}

// RefreshToken replaces a token Zoom rejected and returns the current one. Jobs share the
// client, so if another caller already replaced that token no new request is made.
func (z *ZoomClient) RefreshToken(rejected *AccessToken) (*AccessToken, error) {
	z.mx.Lock()
	defer z.mx.Unlock()

	if z.token == nil || z.token == rejected || z.token.ExpiresAt.Before(time.Now()) {
		if err := z.authorize(); err != nil {
			return nil, err
		}
	}
	return z.token, nil
}

// send makes one API request with the given token
func (z *ZoomClient) send(method, url string, token *AccessToken) (*http.Response, error) {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Add(`Authorization`, fmt.Sprintf("Bearer %s", token.AccessToken))
	req.Header.Add(`Host`, "zoom.us")
	req.Header.Add(`Content-Type`, "application/json")

	return z.client.Do(req)
}

// do makes an API request with the stored token. Zoom can reject a token before it expires,
// so a 401 gets one more attempt with a new token; what names the call in the error.
func (z *ZoomClient) do(method, url, what string) (*http.Response, error) {
	token, err := z.GetToken()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("unable to get token"), err)
	}

	resp, err := z.send(method, url, token)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}

	rejected := statusError(what, resp)
	if err := resp.Body.Close(); err != nil {
		log.Printf("[ERROR] failed to close response: %v", err)
	}
	log.Printf("[WARN] %v, retrying with a new token", rejected)

	if token, err = z.RefreshToken(token); err != nil {
		return nil, errors.Join(rejected, err)
	}
	return z.send(method, url, token)
}

// maxErrorBody caps how much of a failed response goes into the error: it is logged on every retry
const maxErrorBody = 1024

// statusError describes an unexpected response: what failed, the status and what Zoom answered
func statusError(what string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		return fmt.Errorf("%s, status %d", what, resp.StatusCode)
	}
	return fmt.Errorf("%s, status %d, message: %s", what, resp.StatusCode, msg)
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
	const what = "unable to get recordings"

	params := url.Values{}
	params.Add(`page_size`, "300")
	params.Add(`from`, from.Format("2006-01-02"))
	params.Add(`to`, to.Format("2006-01-02"))
	log.Printf("[DEBUG] initial params = %s", params.Encode())

	meetings := []model.Meeting{}

	for {
		log.Printf("[DEBUG] params = %s", params.Encode())
		resp, err := z.do(http.MethodGet, z.apiURL+"/users/me/recordings?"+params.Encode(), what)
		if err != nil {
			return nil, err
		}
		defer func() {
			if err := resp.Body.Close(); err != nil {
				log.Printf("[ERROR] failed to close response: %v", err)
			}
		}()

		if resp.StatusCode != http.StatusOK {
			return nil, statusError(what, resp)
		}

		recordings := &model.Recordings{}

		if err := json.NewDecoder(resp.Body).Decode(recordings); err != nil {
			return nil, fmt.Errorf("failed to unmarshal recordings: %w", err)
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

	for i := range 10 {
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
	const what = "unable to get cloud storage"

	params := url.Values{}
	params.Add(`from`, from)
	params.Add(`to`, to)
	log.Printf("[DEBUG] initial params = %s", params.Encode())

	resp, err := z.do(http.MethodGet, z.apiURL+"/report/cloud_recording?"+params.Encode(), what)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("[ERROR] failed to close response: %v", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, statusError(what, resp)
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

	what := "unable to delete recordings for meeting id: " + meetingId

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
	q := fmt.Sprintf("%s/meetings/%s/recordings?%s", z.apiURL,
		url.QueryEscape(url.QueryEscape(meetingId)), params.Encode())
	log.Printf("[DEBUG] deleting with url = %s, params = %s", q, params.Encode())
	resp, err := z.do(http.MethodDelete, q, what)
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
		return statusError(what, resp)
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
