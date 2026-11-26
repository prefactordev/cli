package profile

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// lockStale is how old a lock file must be before it is taken to belong to
// a process that died while holding it.
const lockStale = 30 * time.Second

// lockPoll is how often Lock checks whether the lock is free.
const lockPoll = 50 * time.Millisecond

// Lock takes a lock on the profiles file at path, held until the returned
// function is called, so that two processes reading, changing and saving a
// profile at once do not lose each other's change. It waits until the lock
// is free or ctx ends. A lock older than 30 seconds is broken.
func Lock(ctx context.Context, path string) (func(), error) {
	lock := path + ".lock"
	if err := os.MkdirAll(filepath.Dir(lock), 0o755); err != nil {
		return nil, err
	}
	for {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(lock) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		if info, err := os.Stat(lock); err == nil && time.Since(info.ModTime()) > lockStale {
			_ = os.Remove(lock)
			continue
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%s is locked by another prefactor command: %w", path, ctx.Err())
		case <-time.After(lockPoll):
		}
	}
}
