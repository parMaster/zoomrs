package main

import (
	"testing"

	"github.com/parMaster/zoomrs/config"

	"github.com/stretchr/testify/assert"
)

func Test_Authorize(t *testing.T) {

	cfg, err := config.NewConfig("config/config.yml")
	assert.NoError(t, err)

	c := NewZoomClient(cfg.Client)
	assert.NotNil(t, c)

	err = c.Authorize()
	assert.NoError(t, err)
}
