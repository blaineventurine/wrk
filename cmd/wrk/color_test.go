package main

import (
	"strings"
	"testing"
)

// TestColorWrapPlainText verifies that when no color code is supplied,
// colorWrap returns the input unchanged — no tabwriter escape bytes,
// no ANSI escapes.
func TestColorWrapPlainText(t *testing.T) {
	got := colorWrap("", "linked")
	if got != "linked" {
		t.Fatalf("colorWrap(\"\", %q) = %q, want %q", "linked", got, "linked")
	}
}

// TestColorWrapBracketedWithTabEscape verifies that colored output is
// bracketed by tabwriter.Escape (\xff) bytes around each ANSI escape
// sequence. Without those, tabwriter treats the escape bytes as
// printable width and misaligns every downstream column.
func TestColorWrapBracketedWithTabEscape(t *testing.T) {
	got := colorWrap(ansiGreen, "linked")
	want := "\xff" + ansiGreen + "\xff" + "linked" + "\xff" + ansiReset + "\xff"
	if got != want {
		t.Fatalf(
			"colorWrap(green, linked)\n got=%q\nwant=%q",
			got, want,
		)
	}

	// Sanity: exactly four sentinel bytes should appear (open code,
	// close code, open reset, close reset).
	if n := strings.Count(got, "\xff"); n != 4 {
		t.Fatalf("expected 4 tabwriter.Escape bytes, got %d in %q", n, got)
	}
}

// TestDimUnwrapped verifies that dim does NOT include tabwriter escape
// bytes — dim is written straight to stdout, never through a
// tabwriter, so those sentinels would render as garbage on the
// terminal. When color is disabled it must be a no-op.
func TestDimUnwrapped(t *testing.T) {
	// Force color off (no TTY in the test environment anyway) and
	// confirm dim is a no-op.
	prev := noColor
	noColor = true
	defer func() { noColor = prev }()

	if got := dim("hello"); got != "hello" {
		t.Fatalf("dim(no-color) = %q, want %q", got, "hello")
	}
}
