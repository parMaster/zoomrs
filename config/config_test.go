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
			Dbg:    true,
		},
		Storage: Storage{
			Type:       "sqlite",
			Path:       "file:.tmp/data1.db?mode=rwc&_journal_mode=WAL",
			Repository: ".tmp",
			// SyncTypes:  []string{"shared_screen_with_gallery_view", "chat_file"},
			SyncTypes: []string{"audio_only", "chat_file"},
		},
		Client: Client{
			DeleteDownloaded: false,
			TrashDownloaded:  false,
		},
	}

	var conf *Parameters
	var err error
	conf, err = NewConfig("config_dbg.yml")
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

	t.Logf("%v+", conf.Storage)
}
