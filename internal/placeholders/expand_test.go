package placeholders

import (
	"strings"
	"testing"
)

func TestExpandRoot(t *testing.T) {
	ctx := Context{
		Root: "/repo",
	}

	got := Expand("{root}/package.json", ctx)

	want := "/repo/package.json"

	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExpandParent(t *testing.T) {
	ctx := Context{
		Parent: "/repo/apps/web",
	}

	got := Expand("{parent}/package.json", ctx)

	want := "/repo/apps/web/package.json"

	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExpandMatch(t *testing.T) {
	ctx := Context{
		Match: "/repo/node_modules",
	}

	got := Expand("{match}", ctx)

	want := "/repo/node_modules"

	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExpandShared(t *testing.T) {
	ctx := Context{
		Shared: "/cache/node_modules/abc123",
	}

	got := Expand("{shared}", ctx)

	want := "/cache/node_modules/abc123"

	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExpandMultiple(t *testing.T) {
	ctx := Context{
		Root:   "/repo",
		Shared: "/cache",
	}

	got := Expand(
		"{root} -> {shared}",
		ctx,
	)

	want := "/repo -> /cache"

	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestUnknownPlaceholderIsLeftUnchanged(t *testing.T) {
	got := Expand(
		"{unknown}",
		Context{},
	)

	want := "{unknown}"

	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExpandStrictKeepsKnownPlaceholders(t *testing.T) {
	ctx := Context{
		Root:   "/repo",
		Shared: "/cache",
	}

	got, err := ExpandStrict("{shared}::{root}/pkg", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "/cache::/repo/pkg"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExpandStrictReportsUnknownPlaceholder(t *testing.T) {
	ctx := Context{
		Root:   "/repo",
		Shared: "/cache",
	}

	_, err := ExpandStrict("{shared}/{shred}/x", ctx)
	if err == nil {
		t.Fatal("expected error for unknown placeholder, got nil")
	}

	msg := err.Error()
	if !strings.Contains(msg, "{shred}") {
		t.Errorf("error %q missing offending placeholder {shred}", msg)
	}
	if !strings.Contains(msg, "{shared}/{shred}/x") {
		t.Errorf("error %q missing original input", msg)
	}
}

func TestExpandStrictDedupesUnknownPlaceholders(t *testing.T) {
	_, err := ExpandStrict("{a}/{a}/{b}", Context{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// The listing prefix carries the deduped unknown placeholders; count
	// there rather than in the whole message (which also echoes the
	// original input).
	msg := err.Error()
	prefix, _, ok := strings.Cut(msg, " in ")
	if !ok {
		t.Fatalf("error %q missing \" in \" separator", msg)
	}
	if strings.Count(prefix, "{a}") != 1 {
		t.Errorf("expected {a} once in listing %q", prefix)
	}
	if !strings.Contains(prefix, "{b}") {
		t.Errorf("expected {b} in listing %q", prefix)
	}
}

func TestExpandStrictAllowsUnrelatedBraces(t *testing.T) {
	// Uppercase / mixed-case content between braces is not a placeholder
	// shape — the regexp only flags all-lowercase words. This keeps
	// legitimate literal text (e.g. a JSON snippet, a shell brace expansion)
	// from being misreported.
	got, err := ExpandStrict("hello {FOO} world", Context{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello {FOO} world" {
		t.Fatalf("got %q, want unchanged", got)
	}
}

func TestExpandUnchangedByExpandStrictAddition(t *testing.T) {
	// Backward-compat pin: Expand keeps its lenient semantics, even for
	// callers that never migrated to ExpandStrict.
	got := Expand("{shared}/{shred}/x", Context{Shared: "/cache"})
	want := "/cache/{shred}/x"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
