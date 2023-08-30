package client

import (
	"log"
	"os"
	"testing"
	"time"

	"github.com/parMaster/zoomrs/config"

	"github.com/stretchr/testify/assert"
)

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

	meetingsInterval, err := c.GetIntervalMeetings(time.Now().AddDate(0, 0, -1), time.Now().AddDate(0, 0, -1))
	assert.NoError(t, err)
	assert.NotNil(t, meetingsInterval)
	assert.Equal(t, len(meetings), len(meetingsInterval))

	// Get cloud storage
	// from the day before yesterday to yesterday
	from := time.Now().AddDate(0, 0, -2).Format("2006-01-02")
	to := time.Now().Format("2006-01-02")

	storageReport, err := c.GetCloudStorageReport(from, to)
	assert.NoError(t, err)
	assert.NotNil(t, storageReport)
	log.Printf("[DEBUG] Storage report: %+v", storageReport)

	// Get token race condition
	go func() {
		token, err := c.GetToken()
		assert.NoError(t, err)
		assert.NotNil(t, token)
	}()

	token, err := c.GetToken()
	assert.NoError(t, err)
	assert.NotNil(t, token)

	// Get token error condition
	cfg.Client.Secret = "error"
	c = NewZoomClient(cfg.Client)
	assert.NotNil(t, c)
	token, err = c.GetToken()
	assert.Error(t, err)
	assert.Nil(t, token)
	// t.Logf("Error: %v", err)
}
