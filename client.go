package main

import (
	b64 "encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
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
	client *http.Client
	token  *AccessToken
	mx     sync.Mutex
}

func NewZoomClient(cfg config.Client) *ZoomClient {
	client := &http.Client{}

	return &ZoomClient{cfg: &cfg, client: client}
}

func (z *ZoomClient) Authorize() error {
	bearer := b64.StdEncoding.EncodeToString([]byte(z.cfg.Id + ":" + z.cfg.Secret))

	params := url.Values{}
	params.Add(`grant_type`, `account_credentials`)
	params.Add(`account_id`, z.cfg.AccountId)

	req, err := http.NewRequest(http.MethodPost, "https://zoom.us/oauth/token", strings.NewReader(params.Encode()))
	if err != nil {
		return err
	}

	req.Header.Add(`Authorization`, fmt.Sprintf("Basic %s", bearer))
	req.Header.Add(`Host`, "zoom.us")
	req.Header.Add(`Content-Type`, "application/x-www-form-urlencoded")

	res, err := z.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("unable to authorize with account id: %s and client id: %s, status %d, message: %s", z.cfg.AccountId, z.cfg.Id, res.StatusCode, res.Body)
	}

	if err := json.NewDecoder(res.Body).Decode(&z.token); err != nil {
		return err
	}

	log.Printf("[DEBUG] token = %v", z.token.AccessToken)

	dur, err := time.ParseDuration(fmt.Sprintf("%ds", z.token.ExpiresIn))
	if err != nil {
		return err
	}
	z.token.ExpiresAt = time.Now().Add(dur)

	return nil
}

func (z *ZoomClient) GetToken() (*AccessToken, error) {
	z.mx.Lock()
	defer z.mx.Unlock()

	if z.token == nil || z.token.ExpiresAt.Before(time.Now()) {
		if err := z.Authorize(); err != nil {
			return nil, err
		}
	}
	return z.token, nil
}

func (z *ZoomClient) GetMeetings() ([]model.Meeting, error) {

	z.GetToken()

	params := url.Values{}
	params.Add(`page_size`, "300")
	params.Add(`from`, time.Now().AddDate(0, 0, -1).Format("2006-01-02"))
	params.Add(`to`, time.Now().AddDate(0, 0, -1).Format("2006-01-02"))
	log.Printf("[DEBUG] initial params = %s", params.Encode())
	req, err := http.NewRequest(http.MethodGet, "https://api.zoom.us/v2/users/me/recordings?"+params.Encode(), nil)
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
		res, err := z.client.Do(req)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()

		if res.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("unable to authorize with account id: %s and client id: %s, status %d, message: %s", z.cfg.AccountId, z.cfg.Id, res.StatusCode, res.Body)
		}

		recordings := &model.Recordings{}

		if err := json.NewDecoder(res.Body).Decode(recordings); err != nil {
			return nil, err
		}

		meetings = append(meetings, recordings.Meetings...)

		if recordings.NextPageToken == `` {
			break
		}
		log.Printf("[DEBUG] recordings.NextPageToken = %v", recordings.NextPageToken)
		params.Set(`next_page_token`, recordings.NextPageToken)
		time.Sleep(500 * time.Millisecond)
	}

	return meetings, nil
}
