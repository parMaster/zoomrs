//go:build !unix

package main

import "log"

// acquireLock takes no lock where flock is not available: two runs at once are not stopped there
func acquireLock(path string) (release func(), err error) {
	log.Printf("[WARN] the run lock is not supported on this system, make sure only one run is active")
	return func() {}, nil
}
