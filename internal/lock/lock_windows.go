//go:build windows

// Package lock provides a process-wide advisory file lock.
package lock

import "errors"

// ErrRunning means another process already holds the lock.
var ErrRunning = errors.New("another `tiup backup sync` is already running")

// Acquire is a no-op on Windows (tiup itself does not run there).
func Acquire(string) (func(), error) { return func() {}, nil }
