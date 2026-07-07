package executor

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestWithLockSerializesSameTarget(t *testing.T) {
	target := filepath.Join(t.TempDir(), "shared")

	var (
		inside  int32
		maxSeen int32
		wg      sync.WaitGroup
	)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			_ = withLock(target, func() error {
				n := atomic.AddInt32(&inside, 1)

				// Track the peak concurrency observed inside the critical
				// section; it must never exceed 1.
				for {
					old := atomic.LoadInt32(&maxSeen)
					if n <= old || atomic.CompareAndSwapInt32(&maxSeen, old, n) {
						break
					}
				}

				atomic.AddInt32(&inside, -1)
				return nil
			})
		}()
	}

	wg.Wait()

	if maxSeen != 1 {
		t.Fatalf("expected at most 1 goroutine inside the lock, saw %d", maxSeen)
	}
}

func TestWithLockDifferentTargetsDoNotBlock(t *testing.T) {
	dir := t.TempDir()

	// Two different targets should be lockable independently; if they
	// serialized, this would deadlock the barrier below.
	var start sync.WaitGroup
	start.Add(2)

	done := make(chan struct{}, 2)

	run := func(name string) {
		_ = withLock(filepath.Join(dir, name), func() error {
			start.Done()
			start.Wait() // both must be inside simultaneously
			done <- struct{}{}
			return nil
		})
	}

	go run("a")
	go run("b")

	<-done
	<-done
}
