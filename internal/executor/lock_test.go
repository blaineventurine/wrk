package executor

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

// asyncBuf is a concurrency-safe byte buffer used to observe stderr
// output from a goroutine.
type asyncBuf struct {
	mu  sync.Mutex
	buf []byte
}

func (a *asyncBuf) Write(p []byte) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.buf = append(a.buf, p...)
	return len(p), nil
}

func (a *asyncBuf) String() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return string(a.buf)
}

// TestWithLockWaitsAndDiagnoses covers S1: when the lock is already
// held, the second withLock call takes the TryLock-then-wait branch and
// emits a diagnostic to stderr before blocking. Asserts (a) the second
// call is blocked while the first still holds the lock, (b) the
// diagnostic reaches stderr with the target path in it, and (c) the
// second call completes cleanly once the first releases.
func TestWithLockWaitsAndDiagnoses(t *testing.T) {
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	restoreStderr := func() {
		os.Stderr = origStderr
	}

	captured := &asyncBuf{}
	copyDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(captured, r)
		close(copyDone)
	}()

	target := filepath.Join(t.TempDir(), "shared")

	firstAcquired := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)

	go func() {
		firstDone <- withLock(target, func() error {
			close(firstAcquired)
			<-release
			return nil
		})
	}()

	<-firstAcquired

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- withLock(target, func() error {
			return nil
		})
	}()

	// Poll for the waiting diagnostic — that proves the second call took
	// the TryLock-fails-then-Lock-blocks path rather than just Lock().
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(captured.String(), "waiting on lock for") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !strings.Contains(captured.String(), "waiting on lock for") {
		close(release)
		<-firstDone
		<-secondDone
		restoreStderr()
		_ = w.Close()
		<-copyDone
		t.Fatalf("expected waiting diagnostic on stderr, got %q", captured.String())
	}

	// Confirm the second call is still blocked before we let it through.
	select {
	case err := <-secondDone:
		close(release)
		<-firstDone
		restoreStderr()
		_ = w.Close()
		<-copyDone
		t.Fatalf("second withLock returned before first released (err=%v)", err)
	default:
	}

	close(release)

	if err := <-firstDone; err != nil {
		restoreStderr()
		_ = w.Close()
		<-copyDone
		t.Fatalf("first withLock: %v", err)
	}
	if err := <-secondDone; err != nil {
		restoreStderr()
		_ = w.Close()
		<-copyDone
		t.Fatalf("second withLock: %v", err)
	}

	restoreStderr()
	if err := w.Close(); err != nil {
		t.Fatalf("closing stderr pipe: %v", err)
	}
	<-copyDone

	if !strings.Contains(captured.String(), target) {
		t.Errorf("expected target %q in stderr diagnostic, got %q", target, captured.String())
	}
}

// TestWithLockUncontendedIsSilent covers the happy path: when the lock
// is uncontended, TryLock succeeds and no diagnostic is printed. This
// guards against a future regression where withLock unconditionally
// emits the "waiting on lock" line.
func TestWithLockUncontendedIsSilent(t *testing.T) {
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w

	captured := &bytes.Buffer{}
	copyDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(captured, r)
		close(copyDone)
	}()

	target := filepath.Join(t.TempDir(), "shared")

	called := int32(0)
	if err := withLock(target, func() error {
		atomic.AddInt32(&called, 1)
		return nil
	}); err != nil {
		os.Stderr = origStderr
		_ = w.Close()
		<-copyDone
		t.Fatalf("withLock: %v", err)
	}

	os.Stderr = origStderr
	if err := w.Close(); err != nil {
		t.Fatalf("closing stderr pipe: %v", err)
	}
	<-copyDone

	if atomic.LoadInt32(&called) != 1 {
		t.Errorf("expected fn to run exactly once, ran %d times", called)
	}
	if captured.Len() != 0 {
		t.Errorf("expected silent uncontended path, got stderr %q", captured.String())
	}
}

// TestWithLockPropagatesFnError pins the error contract: whatever fn
// returns is what withLock returns, unwrapped. A refactor that
// swallowed the fn error or wrapped it would silently mask hook
// failures.
func TestWithLockPropagatesFnError(t *testing.T) {
	target := filepath.Join(t.TempDir(), "shared")
	sentinel := errFnFailed

	err := withLock(target, func() error {
		return sentinel
	})

	if err != sentinel { //nolint:errorlint // exact identity is the contract.
		t.Fatalf("withLock err = %v, want exact identity %v", err, sentinel)
	}
}

// errFnFailed is a package-level sentinel so the identity assertion
// in TestWithLockPropagatesFnError is unambiguous.
var errFnFailed = &fnError{msg: "fn failed"}

type fnError struct{ msg string }

func (e *fnError) Error() string { return e.msg }

// TestWithLockCreatesLockDirectory pins the "lock parent dir may not
// exist yet" contract: withLock does MkdirAll on the lock's parent
// before creating the lock file. On a fresh workspace the shared
// storage tree hasn't been provisioned, so this MkdirAll is what
// keeps the first-ever run from failing.
func TestWithLockCreatesLockDirectory(t *testing.T) {
	root := t.TempDir()
	// Deeply nested target so the entire path chain is missing.
	target := filepath.Join(root, "not", "yet", "here", "resource")

	called := false
	if err := withLock(target, func() error {
		called = true
		return nil
	}); err != nil {
		t.Fatalf("withLock: %v", err)
	}

	if !called {
		t.Error("fn was not invoked")
	}

	// The MkdirAll should have materialized the parent chain, and the
	// lock file should sit at target + ".wrk-lock".
	if info, err := os.Stat(filepath.Dir(target)); err != nil {
		t.Errorf("expected lock parent dir created, got err=%v", err)
	} else if !info.IsDir() {
		t.Errorf("lock parent is not a directory: mode=%v", info.Mode())
	}
	if _, err := os.Stat(target + ".wrk-lock"); err != nil {
		t.Errorf("expected lock file at %s, got err=%v", target+".wrk-lock", err)
	}
}

// TestWithLockRefusesUncreatableLockDir pins that a MkdirAll failure
// on the lock's parent surfaces the error before any TryLock is
// attempted. Trigger: a regular file at the mid-path where the lock
// dir would need to sit — MkdirAll cannot descend through a file.
func TestWithLockRefusesUncreatableLockDir(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, []byte("in the way"), 0o644); err != nil {
		t.Fatal(err)
	}

	// dir(target).wrk-lock = <blocker>/child — MkdirAll must fail.
	target := filepath.Join(blocker, "child", "shared")

	called := false
	err := withLock(target, func() error {
		called = true
		return nil
	})
	if err == nil {
		t.Fatal("expected MkdirAll error, got nil")
	}
	if called {
		t.Error("fn ran despite MkdirAll failure")
	}
}

// TestWithLockTryLockFails pins the TryLock error path: when the
// lock path cannot be opened for creation, withLock surfaces the
// error before invoking fn. Trigger by chmod'ing the lock parent to
// read-only after MkdirAll has already accepted it — OpenFile with
// O_CREATE then fails with EACCES.
//
// Skipped when running as root because permission checks are
// bypassed for uid 0.
func TestWithLockTryLockFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks bypassed for root")
	}

	root := t.TempDir()
	parent := filepath.Join(root, "readonly")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	// Restore write bit so t.TempDir cleanup works.
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}

	// dir(target).wrk-lock = readonly/shared.wrk-lock. MkdirAll(dir)
	// no-ops (already exists as a dir), then flock.OpenFile(...,
	// O_CREATE|O_RDONLY, 0o600) fails EACCES against the read-only
	// parent.
	target := filepath.Join(parent, "shared")

	called := false
	err := withLock(target, func() error {
		called = true
		return nil
	})
	if err == nil {
		t.Fatal("expected TryLock OpenFile to fail against read-only parent, got nil")
	}
	if called {
		t.Error("fn ran despite TryLock failure")
	}
}
