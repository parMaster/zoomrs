package config

import (
	"log"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_LoadConfig(t *testing.T) {

	var conf *Parameters
	var err error
	conf, err = NewConfig("config_example.yml")
	if err != nil {
		log.Fatalf("[ERROR] can't load config, %s", err)
	}
	assert.NotEmpty(t, conf.Server)
	assert.NotEmpty(t, conf.Server.Domain)
	assert.NotEmpty(t, conf.Server.Listen)
	assert.NotEmpty(t, conf.Server.Dbg)
	assert.NotEmpty(t, conf.Server.OAuthClientId)
	assert.NotEmpty(t, conf.Server.OAuthClientSecret)
	assert.NotEmpty(t, conf.Server.AccessKeySalt)
	assert.NotEmpty(t, conf.Server.JWTSecret)
	assert.NotEmpty(t, conf.Server.Managers)

	assert.NotEmpty(t, conf.Client.AccountId)
	assert.NotEmpty(t, conf.Client.Id)
	assert.NotEmpty(t, conf.Client.Secret)
	assert.IsType(t, conf.Client.TrashDownloaded, true)
	assert.IsType(t, conf.Client.DeleteDownloaded, true)

	assert.NotEmpty(t, conf.Syncable)
	assert.NotEmpty(t, conf.Syncable.Important)
	assert.NotEmpty(t, conf.Syncable.Alternative)
	assert.NotEmpty(t, conf.Syncable.Optional)
	assert.NotEmpty(t, conf.Syncable.MinDuration)

	assert.NotEmpty(t, conf.Commander)
	assert.NotEmpty(t, conf.Commander.Instances)

	t.Logf("%v+", conf.Storage)
}
