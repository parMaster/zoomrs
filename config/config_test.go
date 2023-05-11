package config

import (
	"log"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_LoadConfig(t *testing.T) {

	// Expected default config:
	expected := Parameters{
		Server: Server{
			Listen: ":8099",
			Dbg:    false,
		},
		Storage: Storage{
			Type:       "sqlite",
			Path:       "file:data.db?mode=rwc&_journal_mode=WAL",
			Repository: ".tmp",
		},
		Client: Client{
			DeleteDownloaded: false,
			TrashDownloaded:  false,
		},
	}

	var conf *Parameters
	var err error
	conf, err = NewConfig("config.yml")
	if err != nil {
		log.Fatalf("[ERROR] can't load config, %s", err)
	}
	assert.Equal(t, expected.Server, conf.Server)
	assert.Equal(t, expected.Storage, conf.Storage)
	assert.NotEmpty(t, conf.Client.AccountId)
	assert.NotEmpty(t, conf.Client.Id)
	assert.NotEmpty(t, conf.Client.Secret)
	assert.Equal(t, expected.Client.DeleteDownloaded, conf.Client.DeleteDownloaded)
	assert.Equal(t, expected.Client.TrashDownloaded, conf.Client.TrashDownloaded)
}
