package progress

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

// TestBarNonTTYIsSilent pins the primary suppression path: a plain
// bytes.Buffer is not a *os.File, so defaultIsTTY returns false and
// every Bar method MUST become a no-op. Piped / captured output MUST
// stay clean of control characters.
func TestBarNonTTYIsSilent(t *testing.T) {
	var buf bytes.Buffer
	b := New(&buf, 1<<30, "Removing")
	b.Add(1 << 29)
	b.Finish()
	if buf.Len() != 0 {
		t.Errorf("expected no output, got %q", buf.String())
	}
}

// TestBarBelowThresholdIsSilent pins the second suppression path: a
// simulated TTY writer with total <= Threshold MUST stay silent.
// Small operations complete faster than a paint cycle; painting them
// would just flash the terminal.
func TestBarBelowThresholdIsSilent(t *testing.T) {
	withFakeTTY(t, func(w io.Writer) bool { return w == fakeSink })
	b := New(fakeSink, Threshold, "Removing")
	b.Add(Threshold)
	b.Finish()
	if fakeSink.Len() != 0 {
		t.Errorf("expected no output for total==Threshold, got %q", fakeSink.String())
	}
}

// TestBarAboveThresholdRenders confirms the active branch: a
// simulated TTY writer with total > Threshold paints on Finish and
// the final line contains "100%" plus a trailing newline. The
// exact byte pattern is stable enough for a substring check across
// bar-width tweaks.
func TestBarAboveThresholdRenders(t *testing.T) {
	withFakeTTY(t, func(w io.Writer) bool { return w == fakeSink })
	b := New(fakeSink, Threshold+1, "Removing")
	b.Add(Threshold + 1)
	b.Finish()
	out := fakeSink.String()
	if !strings.Contains(out, "100%") {
		t.Errorf("output missing 100%%: %q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("output missing trailing newline: %q", out)
	}
	if !strings.Contains(out, "Removing:") {
		t.Errorf("output missing label: %q", out)
	}
	if !strings.Contains(out, "\r") {
		t.Errorf("output missing in-place carriage return: %q", out)
	}
}

// TestBarThrottlesRepaints pins the throttling guarantee: two Add
// calls within one repaint window MUST produce exactly one paint.
// Uses a fake clock so the assertion is deterministic and does not
// race real wall-time.
func TestBarThrottlesRepaints(t *testing.T) {
	withFakeTTY(t, func(w io.Writer) bool { return w == fakeSink })

	var virtual time.Time
	b := New(fakeSink, Threshold+1, "R")
	b.now = func() time.Time { return virtual }
	b.started = virtual

	// First Add paints (lastPaint is zero).
	virtual = virtual.Add(1 * time.Millisecond)
	b.Add(1)
	firstLen := fakeSink.Len()
	if firstLen == 0 {
		t.Fatal("first Add did not paint")
	}

	// Second Add within the throttle window MUST NOT paint again.
	virtual = virtual.Add(10 * time.Millisecond)
	b.Add(1)
	if fakeSink.Len() != firstLen {
		t.Errorf("second Add painted within throttle window: len went %d -> %d",
			firstLen, fakeSink.Len())
	}

	// Advance past the window; the next Add MUST paint.
	virtual = virtual.Add(100 * time.Millisecond)
	b.Add(1)
	if fakeSink.Len() == firstLen {
		t.Errorf("Add past throttle window did not paint")
	}
}

// TestBarFinishIdempotent pins that repeated Finish calls after
// the first are safe — active clears so no extra newline is
// emitted. Guards against a CLI defer-Finish that races with an
// explicit Finish in a code path that grows one later.
func TestBarFinishIdempotent(t *testing.T) {
	withFakeTTY(t, func(w io.Writer) bool { return w == fakeSink })
	b := New(fakeSink, Threshold+1, "R")
	b.Add(1)
	b.Finish()
	firstOut := fakeSink.String()
	b.Finish()
	if fakeSink.String() != firstOut {
		t.Errorf("second Finish appended output: before=%q after=%q", firstOut, fakeSink.String())
	}
}

// TestBarZeroTotalNoDivideByZero pins the defensive branch in
// paint(): total==0 with active==true would panic on int-division if
// unguarded. active is normally false at total==0 (Threshold >= 0),
// but Finish forces a paint that must not crash.
func TestBarZeroTotalNoDivideByZero(t *testing.T) {
	withFakeTTY(t, func(w io.Writer) bool { return w == fakeSink })
	b := New(fakeSink, Threshold+1, "R")
	b.total = 0
	b.paint() // MUST NOT panic
}

// TestBarNilCallbackSafe is a compile-time sanity check that Add
// tolerates zero — mirrors a common CLI wiring shape where Progress
// is a nil-safe callback.
func TestBarNilCallbackSafe(t *testing.T) {
	withFakeTTY(t, func(w io.Writer) bool { return w == fakeSink })
	b := New(fakeSink, Threshold+1, "R")
	b.Add(0)
	b.Add(-1)
	// No assertion beyond "did not panic"; the counter stays at 0.
	if b.current != 0 {
		t.Errorf("negative Add mutated counter: %d", b.current)
	}
}

// TestHumanBytes pins the byte formatter across unit boundaries.
// Duplicated locally rather than imported from engine.HumanSize to
// avoid an import cycle; this asserts the shape stays consistent
// with engine.HumanSize's contract (see internal/engine/humansize.go).
func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1 MB"},
		{-1, "-"},
	}
	for _, c := range cases {
		if got := humanBytes(c.in); got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFormatDuration pins the ETA formatter across boundaries.
func TestFormatDuration(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{-1, "--"},
		{0, "0s"},
		{59, "59s"},
		{60, "1m00s"},
		{125, "2m05s"},
		{3599, "59m59s"},
		{3600, ">1h"},
	}
	for _, c := range cases {
		if got := formatDuration(c.in); got != c.want {
			t.Errorf("formatDuration(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// fakeSink is the shared write target for tests that need to
// exercise the active branch. Reset by withFakeTTY before every use.
var fakeSink = &bytes.Buffer{}

// withFakeTTY replaces the package's TTY-detection hook for the
// duration of the test and resets the shared fakeSink. The
// predicate is called with each candidate writer so tests that
// need to distinguish multiple writers can do so.
func withFakeTTY(t *testing.T, predicate func(io.Writer) bool) {
	t.Helper()
	fakeSink.Reset()
	orig := isTTY
	isTTY = predicate
	t.Cleanup(func() { isTTY = orig })
}
