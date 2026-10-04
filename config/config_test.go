package config

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewConfig_Errors(t *testing.T) {
	_, err := NewConfig(filepath.Join(t.TempDir(), "missing.yml"))
	assert.ErrorContains(t, err, "can't read config")

	bad := filepath.Join(t.TempDir(), "bad.yml")
	require.NoError(t, os.WriteFile(bad, []byte("server: [unclosed"), 0o600))
	_, err = NewConfig(bad)
	assert.ErrorContains(t, err, "failed to parse config")

	badDelay := filepath.Join(t.TempDir(), "bad_delay.yml")
	require.NoError(t, os.WriteFile(badDelay, []byte("client:\n  rate_limiting_delay:\n    light: fast\n"), 0o600))
	_, err = NewConfig(badDelay)
	assert.ErrorContains(t, err, "failed to parse config")
}

func TestNewConfig_WarnsAboutRemovedTrashDownloaded(t *testing.T) {
	load := func(t *testing.T, yml string) string {
		t.Helper()
		var buf bytes.Buffer
		out := log.Writer()
		log.SetOutput(&buf)
		t.Cleanup(func() { log.SetOutput(out) })

		fname := filepath.Join(t.TempDir(), "config.yml")
		require.NoError(t, os.WriteFile(fname, []byte(yml), 0o600))
		conf, err := NewConfig(fname)
		require.NoError(t, err)
		assert.True(t, conf.Client.DeleteSkipped, "the rest of the config still loads")
		return buf.String()
	}

	for _, value := range []string{"true", "false"} {
		logged := load(t, "client:\n  trash_downloaded: "+value+"\n  delete_skipped: true\n")
		assert.Contains(t, logged, "[WARN]")
		assert.Contains(t, logged, "client.trash_downloaded, which is no longer used")
	}
	assert.Empty(t, load(t, "client:\n  delete_skipped: true\n"))
}

func Test_LoadConfig(t *testing.T) {
	conf, err := NewConfig("config_example.yml")
	require.NoError(t, err)
	assert.Equal(t, 300*time.Millisecond, conf.Client.RateLimitingDelay.Light)
	assert.Equal(t, 550*time.Millisecond, conf.Client.RateLimitingDelay.Medium)
	assert.Equal(t, 1050*time.Millisecond, conf.Client.RateLimitingDelay.Heavy)
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
	assert.IsType(t, conf.Client.DeleteDownloaded, true)

	assert.NotEmpty(t, conf.Client.RateLimitingDelay)
	assert.NotEmpty(t, conf.Client.RateLimitingDelay.Light)
	assert.NotEmpty(t, conf.Client.RateLimitingDelay.Medium)
	assert.NotEmpty(t, conf.Client.RateLimitingDelay.Heavy)
	assert.IsType(t, conf.Client.RateLimitingDelay.Light, time.Duration(0))
	assert.IsType(t, conf.Client.RateLimitingDelay.Medium, time.Duration(0))
	assert.IsType(t, conf.Client.RateLimitingDelay.Heavy, time.Duration(0))

	assert.NotEmpty(t, conf.Syncable)
	assert.NotEmpty(t, conf.Syncable.Important)
	assert.NotEmpty(t, conf.Syncable.Alternative)
	assert.NotEmpty(t, conf.Syncable.Optional)
	assert.NotEmpty(t, conf.Syncable.MinDuration)

	assert.NotEmpty(t, conf.Commander)
	assert.NotEmpty(t, conf.Commander.Instances)

	t.Logf("%v+", conf.Storage)
}
