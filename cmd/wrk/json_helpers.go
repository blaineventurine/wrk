package main

import (
	"bufio"
	"bytes"
	"io"
	"strings"
)

// scanWarnings reads every non-empty line from buf and returns them
// as-is (leading/trailing whitespace trimmed). Under `--json`, every
// destructive command redirects engine.Options.Stdout to a
// bytes.Buffer so the executor's plain-text chatter never pollutes
// the machine-readable stream. The captured lines are re-emitted
// inside the JSON envelope's `warnings` array so a consumer sees
// what the human path would have seen — no data is lost.
//
// Progress-bar output does not surface here because the CLI does
// not create a bar under `--json` (Options.Progress is wired to a
// simple counter instead). Any residual line the executor emits is
// either a warning ("warning: could not complete mid-swap recovery
// for X") or informational — both belong in warnings for
// programmatic consumers.
func scanWarnings(buf *bytes.Buffer) []string {
	if buf == nil || buf.Len() == 0 {
		return nil
	}
	var warnings []string
	scanner := bufio.NewScanner(buf)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		warnings = append(warnings, line)
	}
	return warnings
}

// writeJSON writes payload to w followed by a trailing newline for
// shell-friendliness. All --json emitters converge here so trailing-
// newline behaviour stays consistent across every destructive
// command.
func writeJSON(w io.Writer, payload []byte) error {
	if _, err := w.Write(payload); err != nil {
		return err
	}
	_, err := w.Write([]byte("\n"))
	return err
}
