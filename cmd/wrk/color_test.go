package main

import (
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/engine"
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

// TestColorWrapEmitsAnsiCodesAroundText verifies that a colored cell
// carries the given ANSI code, the text unchanged, and a reset code.
// The output MUST NOT contain any tabwriter Escape (\xff) bytes —
// those were a previous cargo-culted attempt at alignment that
// leaked garbage bytes onto the terminal (StripEscape flag was
// missing) and did not fix alignment anyway because tabwriter counts
// ANSI codes as visible width regardless of bracketing. Alignment
// now lives in writeAligned via table.go, which pairs each cell with
// a plain-text width.
func TestColorWrapEmitsAnsiCodesAroundText(t *testing.T) {
	got := colorWrap(ansiGreen, "linked")
	want := ansiGreen + "linked" + ansiReset
	if got != want {
		t.Fatalf("colorWrap(green, linked)\n got=%q\nwant=%q", got, want)
	}
	if strings.ContainsRune(got, 0xff) {
		t.Fatalf("colorWrap must not emit 0xff sentinels (they render as garbage on the terminal); got %q", got)
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

// TestStateColorAllEnums pins the exact ANSI code returned for every
// engine.State the CLI knows about, and confirms unknown states fall
// through to an empty string (which colorWrap turns into an un-wrapped
// pass-through — the desired failure mode).
//
// A drift here — a state moved between severity buckets by mistake —
// would recolor the status table without any other signal.
func TestStateColorAllEnums(t *testing.T) {
	cases := []struct {
		state engine.State
		want  string
	}{
		{engine.StateLinked, ansiGreen},
		{engine.StateExpected, ansiGreen},
		{engine.StateDetached, ansiYellow},
		{engine.StatePending, ansiYellow},
		{engine.StateStale, ansiRed},
		{engine.StateConflict, ansiRed},
		{engine.StateNotLinked, ansiRed},
		{engine.StateAbsent, ansiRed},
		{engine.StateMissing, ansiRed},
		{engine.State("bogus"), ""},
	}
	for _, c := range cases {
		t.Run(string(c.state), func(t *testing.T) {
			if got := stateColor(c.state); got != c.want {
				t.Errorf("stateColor(%q) = %q, want %q", c.state, got, c.want)
			}
		})
	}
}

// TestWorkspaceStateColorAllEnums does the same for the workspace-
// level rollup states used by `wrk workspaces`. WorkspaceEmpty and any
// unknown value collapse to "" so an unstyled cell appears — safer
// than fabricating a color.
func TestWorkspaceStateColorAllEnums(t *testing.T) {
	cases := []struct {
		state engine.WorkspaceState
		want  string
	}{
		{engine.WorkspaceLinked, ansiGreen},
		{engine.WorkspaceDetached, ansiYellow},
		{engine.WorkspacePending, ansiYellow},
		{engine.WorkspacePartial, ansiYellow},
		{engine.WorkspaceUnhealthy, ansiRed},
		{engine.WorkspaceEmpty, ""},
		{engine.WorkspaceState("bogus"), ""},
	}
	for _, c := range cases {
		t.Run(string(c.state), func(t *testing.T) {
			if got := workspaceStateColor(c.state); got != c.want {
				t.Errorf("workspaceStateColor(%q) = %q, want %q", c.state, got, c.want)
			}
		})
	}
}

// TestColorStateNoColorReturnsPlainString pins the "colors off" path:
// with useColor==false, colorState must return the raw state string
// with no wrapping, no tabwriter escapes, no ANSI. Piped output has to
// stay pipe-friendly.
func TestColorStateNoColorReturnsPlainString(t *testing.T) {
	// Force color off (no TTY in tests anyway; this pins it explicitly).
	prev := noColor
	noColor = true
	defer func() { noColor = prev }()

	for _, s := range []engine.State{
		engine.StateLinked,
		engine.StateConflict,
		engine.State("bogus"),
	} {
		got := colorState(s)
		if got != string(s) {
			t.Errorf("colorState(%q, no-color) = %q, want %q", s, got, string(s))
		}
		if strings.Contains(got, "\x1b") || strings.Contains(got, "\xff") {
			t.Errorf("colorState(%q, no-color) leaked escape bytes: %q", s, got)
		}
	}
}

// TestColorStateWithColorWrapsMatchingBucket pins the on-color path:
// with color enabled, the returned string carries the SAME ANSI code
// that stateColor reports for that state, the state text is intact,
// AND no 0xff sentinel bytes leak into the output. This is the join
// between the two helpers.
func TestColorStateWithColorWrapsMatchingBucket(t *testing.T) {
	// Force color on via CLICOLOR_FORCE — test binaries have no TTY.
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "")
	t.Setenv("CLICOLOR_FORCE", "1")

	prev := noColor
	noColor = false
	defer func() { noColor = prev }()

	for _, s := range []engine.State{
		engine.StateLinked,
		engine.StateDetached,
		engine.StateConflict,
	} {
		code := stateColor(s)
		if code == "" {
			t.Fatalf("stateColor(%q) unexpectedly empty; can't check colored path", s)
		}
		got := colorState(s)
		if !strings.Contains(got, code) {
			t.Errorf("colorState(%q) missing bucket color %q; got %q", s, code, got)
		}
		if !strings.Contains(got, string(s)) {
			t.Errorf("colorState(%q) dropped the state text; got %q", s, got)
		}
		if strings.ContainsRune(got, 0xff) {
			t.Errorf("colorState(%q) leaked 0xff sentinel bytes into %q", s, got)
		}
	}
}

// TestColorWorkspaceStateNoColorPasthrough is the workspace-summary
// twin: no-color mode returns the plain state string, no escapes.
func TestColorWorkspaceStateNoColorPassthrough(t *testing.T) {
	prev := noColor
	noColor = true
	defer func() { noColor = prev }()

	for _, s := range []engine.WorkspaceState{
		engine.WorkspaceLinked,
		engine.WorkspacePartial,
		engine.WorkspaceUnhealthy,
		engine.WorkspaceEmpty,
	} {
		got := colorWorkspaceState(s)
		if got != string(s) {
			t.Errorf("colorWorkspaceState(%q, no-color) = %q, want %q", s, got, string(s))
		}
	}
}
