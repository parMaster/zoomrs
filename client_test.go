package main

import (
	"testing"

	"github.com/parMaster/zoomrs/config"

	"github.com/stretchr/testify/assert"
)

func Test_ZoomClient(t *testing.T) {

	cfg, err := config.NewConfig("config/config_dbg.yml")
	assert.NoError(t, err)

	c := NewZoomClient(cfg.Client)
	assert.NotNil(t, c)

	err = c.Authorize()
	assert.NoError(t, err)

	meetings, err := c.GetMeetings()
	assert.NoError(t, err)
	assert.NotNil(t, meetings)

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
	t.Logf("Error: %v", err)

}
