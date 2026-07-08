package repository

import (
	"strings"
	"testing"
)

// TestNewRepositoryPanicsOnEmptyRoot pins M24: newRepository is an
// internal constructor and empty root / nil backend indicate a
// programmer bug. Panicking here surfaces the bug at construction
// rather than as a confusing nil-deref inside a VCS operation later.
func TestNewRepositoryPanicsOnEmptyRoot(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("newRepository(\"\", …) did not panic; want panic on empty root")
		}
		if !strings.Contains(strings.ToLower(msg(r)), "empty root") {
			t.Fatalf("panic = %v, want mention of empty root or nil backend", r)
		}
	}()

	_ = newRepository("", "local/test", "/tmp/.git", gitBackend{})
}

// TestNewRepositoryPanicsOnNilBackend covers the second half of the
// M24 guard.
func TestNewRepositoryPanicsOnNilBackend(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("newRepository(..., nil) did not panic; want panic on nil backend")
		}
		if !strings.Contains(strings.ToLower(msg(r)), "backend") {
			t.Fatalf("panic = %v, want mention of nil backend", r)
		}
	}()

	_ = newRepository("/tmp/root", "local/test", "/tmp/.git", nil)
}

// msg renders any panic value as a string; the guard panics with a
// literal string, but callers may reasonably expect either that or a
// wrapped error someday.
func msg(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if e, ok := v.(error); ok {
		return e.Error()
	}
	return ""
}

// TestRepositoryAccessors is a single spot check that the two trivial
// getters return the values wired in at construction time. The point
// isn't to prove the assignment — it's to prove the RUNTIME contract:
//   - VCS() reports the backend's kind, not a stale copy from a field
//     (a refactor that stored VCS in the struct AND had backend.kind()
//     drift would still pass a field-echo test but break real dispatch).
//   - MetadataDir() returns the exact string handed to newRepository,
//     because downstream code joins paths under it verbatim.
func TestRepositoryAccessors(t *testing.T) {
	// Real backend so VCS() actually exercises the interface
	// dispatch, not a hand-crafted mock returning whatever we like.
	r := newRepository(
		"/tmp/example",
		"local/abcdef",
		"/tmp/example/.git",
		gitBackend{},
	)

	if got := r.VCS(); got != Git {
		t.Fatalf("Repository.VCS() = %q, want %q", got, Git)
	}
	if got := r.MetadataDir(); got != "/tmp/example/.git" {
		t.Fatalf("Repository.MetadataDir() = %q, want %q",
			got, "/tmp/example/.git")
	}

	// And with the other backend to prove dispatch actually
	// switches — a hard-coded return would pass the previous check.
	rjj := newRepository(
		"/tmp/example",
		"local/abcdef",
		"/tmp/example/.git",
		jjBackend{},
	)
	if got := rjj.VCS(); got != JJ {
		t.Fatalf("Repository.VCS() with jjBackend = %q, want %q", got, JJ)
	}
}
