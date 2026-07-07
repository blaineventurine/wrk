package executor

import (
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

// withLock runs fn while holding an exclusive advisory lock keyed to target.
//
// The lock is per shared-resource path, so different resources (and
// different repositories) provision concurrently, while two workspaces
// racing to create the *same* shared resource are serialized.
//
// The lock is released when fn returns. Because it is an OS-level flock, it
// is also released automatically if the process dies, so no stale-lock
// recovery is required.
func withLock(target string, fn func() error) error {
	lockPath := target + ".wrk-lock"

	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return err
	}

	lock := flock.New(lockPath)

	if err := lock.Lock(); err != nil {
		return err
	}
	defer func() {
		_ = lock.Unlock()
	}()

	return fn()
}
