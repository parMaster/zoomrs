package main

import (
	"errors"
	"strings"
)

// errLocked is returned when another run holds the lock
var errLocked = errors.New("another run holds the lock")

// lockPath puts the lock file next to the database file, wherever the CLI is run from:
// the SQLite URI "file:/data/_db/main.db?mode=rwc" gives "/data/_db/main.db.lock"
func lockPath(dsn string) string {
	path, _, _ := strings.Cut(strings.TrimPrefix(dsn, "file:"), "?")
	return path + ".lock"
}
