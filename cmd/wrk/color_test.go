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

// TestUseColorHonorsCLICOLORForce pins M15: when CLICOLOR_FORCE is set
// to a non-empty value other than "0", useColor returns true even
// though stdout in the test process is not a TTY. The overrides for
// disabling color (--no-color, NO_COLOR) still take priority.
func TestUseColorHonorsCLICOLORForce(t *testing.T) {
	// Ensure the other env vars we care about are clean, then set
	// CLICOLOR_FORCE and confirm useColor returns true despite the
	// non-TTY test stdout.
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "")
	t.Setenv("CLICOLOR_FORCE", "1")

	prev := noColor
	noColor = false
	defer func() { noColor = prev }()

	if !useColor() {
		t.Fatalf("useColor() = false with CLICOLOR_FORCE=1, want true")
	}
}

// TestUseColorCLICOLORForceZeroDoesNotForce ensures the standard
// escape hatch — CLICOLOR_FORCE=0 — is respected: it must not enable
// color on non-TTYs.
func TestUseColorCLICOLORForceZeroDoesNotForce(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "")
	t.Setenv("CLICOLOR_FORCE", "0")

	prev := noColor
	noColor = false
	defer func() { noColor = prev }()

	// Test binaries run without a TTY, so the fallback path is exercised.
	if useColor() {
		t.Fatalf("useColor() = true with CLICOLOR_FORCE=0, want false")
	}
}

// TestUseColorNoColorBeatsCLICOLORForce pins the precedence: NO_COLOR
// wins over CLICOLOR_FORCE even when both are set. Users who set
// NO_COLOR globally should not have that opinion overridden by an
// upstream shell that also exports CLICOLOR_FORCE.
func TestUseColorNoColorBeatsCLICOLORForce(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("TERM", "")
	t.Setenv("CLICOLOR_FORCE", "1")

	prev := noColor
	noColor = false
	defer func() { noColor = prev }()

	if useColor() {
		t.Fatalf("useColor() = true with NO_COLOR=1 and CLICOLOR_FORCE=1, want false")
	}
}
