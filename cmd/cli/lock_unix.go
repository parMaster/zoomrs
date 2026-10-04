//go:build unix

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// acquireLock takes the run lock without waiting; errLocked means another run holds it.
// The kernel drops the lock when the process dies, so a killed run leaves nothing to clean up.
func acquireLock(path string) (release func(), err error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("failed to open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		holder, _ := io.ReadAll(io.LimitReader(f, 32))
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w: %s, pid %s", errLocked, path, strings.TrimSpace(string(holder)))
		}
		return nil, fmt.Errorf("failed to lock %s: %w", path, err)
	}
	// the pid is only read for the message of a run that finds the lock held
	if err := f.Truncate(0); err == nil {
		_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())), 0)
	}
	return func() { _ = f.Close() }, nil
}
