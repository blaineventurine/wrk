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
