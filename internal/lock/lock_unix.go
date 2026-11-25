//go:build !windows

// Package lock provides a process-wide advisory file lock.
package lock

import (
	"errors"
	"os"
	"syscall"
)

// ErrRunning means another process already holds the lock.
var ErrRunning = errors.New("another `tiup backup sync` is already running")

// Acquire takes an exclusive non-blocking lock on path and returns its release
// function. The lock is also released when the process exits.
func Acquire(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrRunning
		}
		return nil, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}
