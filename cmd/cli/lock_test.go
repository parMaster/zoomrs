//go:build unix

package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLockPath(t *testing.T) {
	for dsn, want := range map[string]string{
		"file:/data/_db/main.db?mode=rwc&_journal_mode=WAL": "/data/_db/main.db.lock",
		"file:/data/_db/main.db":                            "/data/_db/main.db.lock",
		"/data/_db/main.db":                                 "/data/_db/main.db.lock",
		"file:main.db?mode=rwc":                             "main.db.lock",
	} {
		assert.Equal(t, want, lockPath(dsn), dsn)
	}
}

func TestAcquireLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.db.lock")

	release, err := acquireLock(path)
	require.NoError(t, err)
	pid, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(os.Getpid()), string(pid))

	_, err = acquireLock(path)
	require.ErrorIs(t, err, errLocked)
	assert.ErrorContains(t, err, path+", pid "+strconv.Itoa(os.Getpid()))

	release()
	release, err = acquireLock(path)
	require.NoError(t, err)
	release()

	_, err = acquireLock(filepath.Join(t.TempDir(), "no-such-dir", "x.db.lock"))
	assert.ErrorContains(t, err, "failed to open lock file")
}

func TestRun_Lock(t *testing.T) {
	ctx := context.Background()
	// the test configs use a URI-style path, like the shipped ones
	dbFile := func(c *Commander) string {
		path, _, _ := strings.Cut(strings.TrimPrefix(c.cfg.Storage.Path, "file:"), "?")
		return path
	}

	t.Run("sync, trash and cloudcap do not start while another run holds the lock", func(t *testing.T) {
		c, f := newTestCommander(t)
		f.serveMeeting(t)
		release, err := acquireLock(dbFile(c) + ".lock")
		require.NoError(t, err)

		for _, cmd := range []string{"sync", "trash", "cloudcap"} {
			assert.ErrorIs(t, c.Run(ctx, Options{Cmd: cmd, Days: daysUnset}), errLocked, cmd)
		}
		assert.Empty(t, f.listings(), "Zoom is not asked")
		assert.NoFileExists(t, dbFile(c), "the database is not opened")

		// check only reads the local database, so it runs next to a sync
		assert.NoError(t, c.Run(ctx, Options{Cmd: "check"}))

		release()
		require.NoError(t, c.Run(ctx, Options{Cmd: "sync", Days: daysUnset}))
		assert.Len(t, f.listings(), 1)
	})

	t.Run("a run releases the lock when it ends, with an error too", func(t *testing.T) {
		c, _ := newTestCommander(t)
		c.cfg.Client.CloudCapacityHardLimit = 0
		require.NoError(t, c.Run(ctx, Options{Cmd: "sync", Days: 1}))
		require.ErrorContains(t, c.Run(ctx, Options{Cmd: "cloudcap"}), "cloud storage capacity is not configured")
		require.NoError(t, c.Run(ctx, Options{Cmd: "trash", Days: 1}))
	})

	t.Run("the lock file of a killed run does not block the next one", func(t *testing.T) {
		c, f := newTestCommander(t)
		// a dead process leaves the file with its pid, but the kernel has dropped its lock
		require.NoError(t, os.WriteFile(dbFile(c)+".lock", []byte("4194304"), 0o600))

		require.NoError(t, c.Run(ctx, Options{Cmd: "sync", Days: 1}))
		assert.Len(t, f.listings(), 1)
		pid, err := os.ReadFile(dbFile(c) + ".lock")
		require.NoError(t, err)
		assert.Equal(t, strconv.Itoa(os.Getpid()), string(pid))
	})

	t.Run("the lock file sits next to the database, whatever the working directory", func(t *testing.T) {
		c, _ := newTestCommander(t)
		cwd := t.TempDir()
		t.Chdir(cwd)

		require.NoError(t, c.Run(ctx, Options{Cmd: "sync", Days: 1}))
		assert.FileExists(t, dbFile(c)+".lock")
		inCwd, err := os.ReadDir(cwd)
		require.NoError(t, err)
		assert.Empty(t, inCwd)
	})
}
