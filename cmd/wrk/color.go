package main

import (
	"os"

	"github.com/mattn/go-isatty"

	"github.com/blaineventurine/wrk/internal/engine"
)

// noColor is set by the --no-color persistent flag.
var noColor bool

const (
	// tabEsc is text/tabwriter.Escape (\xff). Tabwriter treats any
	// bytes between two Escape bytes as zero-width, so wrapping ANSI
	// escapes lets colored cells stay aligned in the output table.
	tabEsc = "\xff"

	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiDim    = "\x1b[2m"
)

// useColor reports whether ANSI color should be emitted to stdout.
//
// Honors the NO_COLOR standard (https://no-color.org/) and the
// --no-color flag, and skips coloring when stdout is not a terminal
// (pipes, redirects, dumb terminals). CLICOLOR_FORCE (any non-empty
// value other than "0") overrides only the TTY check — NO_COLOR and
// --no-color still win.
func useColor() bool {
	if noColor {
		return false
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	if v := os.Getenv("CLICOLOR_FORCE"); v != "" && v != "0" {
		return true
	}
	return isatty.IsTerminal(os.Stdout.Fd())
}

func colorState(s engine.State) string {
	if !useColor() {
		return string(s)
	}
	return colorWrap(stateColor(s), string(s))
}

func colorWorkspaceState(s engine.WorkspaceState) string {
	if !useColor() {
		return string(s)
	}
	return colorWrap(workspaceStateColor(s), string(s))
}

// colorWrap wraps text with the given ANSI color code and a reset.
// Each escape sequence is bracketed with text/tabwriter.Escape (0xff)
// bytes so that a tabwriter measuring column widths counts only the
// printable text and not the invisible escape bytes.
func colorWrap(color, text string) string {
	if color == "" {
		return text
	}
	return tabEsc + color + tabEsc + text + tabEsc + ansiReset + tabEsc
}

func stateColor(s engine.State) string {
	switch s {
	case engine.StateLinked, engine.StateExpected:
		return ansiGreen
	case engine.StateDetached, engine.StatePending:
		return ansiYellow
	case engine.StateStale,
		engine.StateConflict,
		engine.StateNotLinked,
		engine.StateAbsent,
		engine.StateMissing:
		return ansiRed
	}
	return ""
}

func workspaceStateColor(s engine.WorkspaceState) string {
	switch s {
	case engine.WorkspaceLinked:
		return ansiGreen
	case engine.WorkspaceDetached, engine.WorkspacePending, engine.WorkspacePartial:
		return ansiYellow
	case engine.WorkspaceUnhealthy:
		return ansiRed
	}
	return ""
}

// dim wraps text with a dim ANSI code when color is enabled. Unlike
// colorWrap, dim is emitted directly to stdout (never through a
// tabwriter) so its ANSI escapes are not bracketed with tabwriter
// zero-width sentinels — those would render as garbage bytes on the
// terminal.
func dim(text string) string {
	if !useColor() {
		return text
	}
	return ansiDim + text + ansiReset
}
