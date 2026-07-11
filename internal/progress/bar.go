// Package progress renders a byte-count progress line for
// long-running destructive commands (wrk remove / gc / forget) and
// stays silent everywhere else.
//
// The Bar type is TTY-aware: when the writer is not a *os.File
// pointing at a real terminal, or when the total byte count is below
// Threshold, every method is a no-op. This lets callers thread the
// Bar unconditionally through the CLI-to-executor plumbing without
// polluting piped/scripted output with control characters or spamming
// the terminal for trivially-small operations.
//
// A Bar is single-goroutine: callers MUST NOT share one across
// concurrent producers. The engine layer drives each destructive
// operation serially, so this matches the existing plumbing without
// added locking.
package progress

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
)

// Threshold is the byte-count below which a Bar stays silent even on
// a TTY. Small operations complete in well under 50 ms; painting a
// bar that flashes to 100% and disappears is worse than no bar.
const Threshold = 50 * 1024 * 1024

// barWidth is the fixed width in cells of the filled/empty gauge.
// Together with the surrounding text the whole line fits in ~72
// columns so an 80-column terminal never wraps mid-repaint.
const barWidth = 20

// isTTY is the TTY-detection hook. Tests swap it out to exercise
// the active-bar branch without owning a real terminal.
var isTTY = defaultIsTTY

// Bar renders a single-line byte-count progress indicator that
// overwrites itself in place via `\r`. Zero-value Bars are unsafe;
// construct through New.
type Bar struct {
	active     bool
	w          io.Writer
	total      int64
	current    int64
	label      string
	started    time.Time
	lastPaint  time.Time
	minRepaint time.Duration

	// now is the time source. Injected so tests can pin repaint
	// timing without a real clock.
	now func() time.Time
}

// New returns a Bar that renders to w. label prefixes each paint
// (e.g. "Removing"). The Bar is silent when w is not a TTY, or when
// total is at or below Threshold; every method call becomes a no-op
// in that case, so callers do not need to guard.
func New(w io.Writer, total int64, label string) *Bar {
	b := &Bar{
		w:          w,
		total:      total,
		label:      label,
		minRepaint: 50 * time.Millisecond,
		now:        time.Now,
	}
	b.started = b.now()
	if total > Threshold && isTTY(w) {
		b.active = true
	}
	return b
}

// Add increments the current byte count by n and repaints if enough
// time has passed since the last paint. Safe to call on an inactive
// Bar (no-op). n <= 0 is tolerated but does not force a repaint.
func (b *Bar) Add(n int64) {
	if !b.active {
		return
	}
	if n > 0 {
		b.current += n
	}
	now := b.now()
	if b.lastPaint.IsZero() || now.Sub(b.lastPaint) >= b.minRepaint {
		b.paint()
		b.lastPaint = now
	}
}

// Finish paints the final 100% state and terminates the line with
// a newline so subsequent output starts on a fresh row. Safe to call
// on an inactive Bar. Idempotent: repeated Finish calls after the
// first are no-ops on active bars too (active is cleared on first
// call).
func (b *Bar) Finish() {
	if !b.active {
		return
	}
	b.current = b.total
	b.paint()
	fmt.Fprintln(b.w)
	b.active = false
}

// paint renders the current state to b.w. Callers are responsible
// for the active check; paint itself is unconditional so Finish can
// force a final render regardless of throttling.
func (b *Bar) paint() {
	total := b.total
	if total < 1 {
		total = 1
	}
	pct := int(100 * b.current / total)
	if pct > 100 {
		pct = 100
	}
	if pct < 0 {
		pct = 0
	}
	filled := (pct * barWidth) / 100
	if filled > barWidth {
		filled = barWidth
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)

	elapsed := b.now().Sub(b.started).Seconds()
	var rate int64
	if elapsed > 0.1 {
		rate = int64(float64(b.current) / elapsed)
	}

	eta := "--"
	switch {
	case b.current >= b.total:
		eta = "0s"
	case rate > 0:
		secsLeft := int64(float64(b.total-b.current) / float64(rate))
		eta = formatDuration(secsLeft)
	}

	fmt.Fprintf(b.w, "\r%s: [%s] %3d%% (%s / %s) %s/s ETA %s",
		b.label, bar, pct,
		humanBytes(b.current), humanBytes(b.total),
		humanBytes(rate), eta,
	)
}

// defaultIsTTY reports whether w is a *os.File pointing at a real
// terminal. bytes.Buffer, in-memory readers, and pipes all return
// false, which is exactly what suppresses the bar under redirection.
func defaultIsTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return isatty.IsTerminal(f.Fd())
}

// humanBytes renders a byte count with a short unit suffix. Kept
// local rather than reaching into engine.HumanSize to avoid an
// import cycle: internal/engine imports internal/progress via the
// wired-in Options.Progress callback.
func humanBytes(n int64) string {
	if n < 0 {
		return "-"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	if exp >= len("KMGTPE") {
		exp = len("KMGTPE") - 1
	}
	val := float64(n) / float64(div)
	if val == float64(int64(val)) {
		return fmt.Sprintf("%d %cB", int64(val), "KMGTPE"[exp])
	}
	return fmt.Sprintf("%.1f %cB", val, "KMGTPE"[exp])
}

// formatDuration renders a seconds count as a short human string.
// Anything past an hour clamps to ">1h" so an early-noise ETA on a
// giant sweep never spikes to nonsense like "43m".
func formatDuration(secs int64) string {
	switch {
	case secs < 0:
		return "--"
	case secs < 60:
		return fmt.Sprintf("%ds", secs)
	case secs < 3600:
		return fmt.Sprintf("%dm%02ds", secs/60, secs%60)
	default:
		return ">1h"
	}
}
