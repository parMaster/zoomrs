package client

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"testing"
	"time"

	"github.com/parMaster/zoomrs/config"
	"github.com/parMaster/zoomrs/storage/model"

	"github.com/stretchr/testify/assert"
)

// Complete integration test. Requires Zoom credentials.
func Test_ZoomClient(t *testing.T) {

	cfgPath := "../config/config_cli.yml"

	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		t.Skip("Config file does not exist: " + cfgPath)
	}

	cfg, err := config.NewConfig(cfgPath)
	assert.NoError(t, err)

	if cfg.Client.Id == "secret" || cfg.Client.Secret == "secret" {
		t.Skip("Zoom credentials are not configured")
	}

	c := NewZoomClient(cfg.Client)
	assert.NotNil(t, c)

	err = c.Authorize()
	assert.NoError(t, err)

	meetings, err := c.GetMeetings(1)
	assert.NoError(t, err)
	assert.NotNil(t, meetings)

	// GetIntervalMeetings test
	meetingsInterval, err := c.GetIntervalMeetings(time.Now().AddDate(0, 0, -1), time.Now().AddDate(0, 0, -1))
	assert.NoError(t, err)
	assert.NotNil(t, meetingsInterval)
	assert.Equal(t, len(meetings), len(meetingsInterval))

	// DeleteRecordingsOverCapacity test
	storageCapacity := model.FileSize(500 * 1024 * 1024 * 1024) // 500GB
	deleted, err := c.DeleteRecordingsOverCapacity(context.Background(), storageCapacity)
	assert.NoError(t, err)
	assert.NotNil(t, deleted)

	// Get cloud storage
	// from the day before yesterday to yesterday
	from := time.Now().AddDate(0, 0, -2).Format("2006-01-02")
	to := time.Now().Format("2006-01-02")

	storageReport, err := c.GetCloudStorageReport(from, to)
	assert.NoError(t, err)
	assert.NotNil(t, storageReport)
	log.Printf("[DEBUG] Storage report: %+v", storageReport)

	// Get token error condition
	cfg.Client.Secret = "error"
	c = NewZoomClient(cfg.Client)
	assert.NotNil(t, c)
	token, err := c.GetToken()
	assert.Error(t, err)
	assert.Nil(t, token)
	// t.Logf("Error: %v", err)
}

// Tests a specific race condition in GetToken()
func Test_ZoomGetTokenRaceTest(t *testing.T) {

	cfgPath := "../config/config_cli.yml"

	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		t.Skip("Config file does not exist: " + cfgPath)
	}

	cfg, err := config.NewConfig(cfgPath)
	assert.NoError(t, err)

	if cfg.Client.Id == "secret" || cfg.Client.Secret == "secret" {
		t.Skip("Zoom credentials are not configured")
	}

	c := NewZoomClient(cfg.Client)
	assert.NotNil(t, c)

	// Get token race condition
	for i := 0; i < 10; i++ {
		go func() {
			token, err := c.GetToken()
			assert.NoError(t, err)
			assert.NotNil(t, token)
		}()
	}

	token, err := c.GetToken()
	assert.NoError(t, err)
	assert.NotNil(t, token)

	// Get token error condition
	cfg.Client.Secret = "error"
	c1 := NewZoomClient(cfg.Client)
	assert.NotNil(t, c1)
	token1, err := c1.GetToken()
	assert.Error(t, err)
	assert.Nil(t, token1)
	// t.Logf("Error: %v", err)
}

// Tests GetCloudStorageReport, shows the result in verbose mode
func Test_CloudStorageReport(t *testing.T) {
	cfgPath := "../config/config_cli.yml"

	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		t.Skip("Config file does not exist: " + cfgPath)
	}

	cfg, err := config.NewConfig(cfgPath)
	assert.NoError(t, err)

	if cfg.Client.Id == "secret" || cfg.Client.Secret == "secret" {
		t.Skip("Zoom credentials are not configured")
	}

	c := NewZoomClient(cfg.Client)
	assert.NotNil(t, c)

	err = c.Authorize()
	assert.NoError(t, err)

	// Get cloud storage
	// from 14 days ago to yesterday
	from := time.Now().AddDate(0, 0, -14).Format("2006-01-02")
	to := time.Now().Format("2006-01-02")

	storageReport, err := c.GetCloudStorageReport(from, to)
	assert.NoError(t, err)
	assert.NotNil(t, storageReport)

	// Print storage report
	s, _ := json.MarshalIndent(storageReport, "", "\t")
	log.Printf("[DEBUG] Storage report: %+v", s)
}
