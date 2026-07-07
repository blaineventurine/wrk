package main

import (
	"os"

	"github.com/mattn/go-isatty"

	"github.com/blaineventurine/wrk/internal/engine"
)

// noColor is set by the --no-color persistent flag.
var noColor bool

const (
	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiDim    = "\x1b[2m"
	ansiBold   = "\x1b[1m"
)

// useColor reports whether ANSI color should be emitted to stdout.
//
// Honors the NO_COLOR standard (https://no-color.org/) and the
// --no-color flag, and skips coloring when stdout is not a terminal
// (pipes, redirects, dumb terminals).
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

func colorWrap(color, text string) string {
	if color == "" {
		return text
	}
	return color + text + ansiReset
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

// dim wraps text with a dim ANSI code when color is enabled.
func dim(text string) string {
	if !useColor() {
		return text
	}
	return ansiDim + text + ansiReset
}
