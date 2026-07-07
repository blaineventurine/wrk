package main

import (
	"fmt"
	"runtime/debug"
)

// These are populated by GoReleaser at release time via -X ldflags.
// For non-release builds (go install, go build), they stay at their
// defaults and we fall back to Go's embedded VCS info.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// versionString returns a human-readable version identifier, preferring
// values injected at release time and otherwise falling back to Go's
// embedded VCS info (available since Go 1.18 for any module-aware build).
func versionString() string {
	v, c, d := version, commit, date

	if info, ok := debug.ReadBuildInfo(); ok {
		// go install @v0.1.2 populates info.Main.Version; local
		// `go build` populates it as "(devel)".
		if v == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
			v = info.Main.Version
		}

		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				if c == "none" && s.Value != "" {
					c = s.Value
				}
			case "vcs.time":
				if d == "unknown" && s.Value != "" {
					d = s.Value
				}
			}
		}
	}

	if len(c) > 12 {
		c = c[:12]
	}

	return fmt.Sprintf("%s (commit %s, built %s)", v, c, d)
}
