package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/blaineventurine/wrk/internal/engine"
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

// emitJSONError writes a machine-readable error envelope to w. When
// err (or anything it wraps) is a *engine.Error, its Code, Message,
// and Hint are used verbatim; otherwise the envelope carries
// engine.ErrUnknown and the untyped Error() string.
//
// Under `wrk <cmd> --json`, callers redirect this to STDERR so the
// STDOUT stream (which may already carry a partial plan+result
// envelope) stays clean JSON. See each destructive command's RunE for
// the emitJSONError -> exitCode{code: 2} sequence.
func emitJSONError(w io.Writer, err error) {
	if err == nil {
		return
	}
	var wrkErr *engine.Error
	if !errors.As(err, &wrkErr) {
		wrkErr = &engine.Error{
			Code:    engine.ErrUnknown,
			Message: err.Error(),
		}
	}
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Hint    string `json:"hint,omitempty"`
		} `json:"error"`
	}
	payload.Error.Code = string(wrkErr.Code)
	payload.Error.Message = wrkErr.Message
	// Fallback: an untyped error might arrive with a *Error whose
	// Message is empty (e.g. someone built &Error{Wrapped: e} directly).
	// Preserve the visible cause so consumers see the reason.
	if payload.Error.Message == "" {
		payload.Error.Message = err.Error()
	}
	payload.Error.Hint = wrkErr.Hint
	data, jerr := json.Marshal(payload)
	if jerr != nil {
		// Marshalling a fixed-shape struct with string fields should
		// never fail, but if it did, fall back to a minimal literal
		// so consumers still see something parseable.
		data = []byte(`{"error":{"code":"unknown","message":"emit failed"}}`)
	}
	_, _ = w.Write(data)
	_, _ = w.Write([]byte("\n"))
}

// refuseJSONInteractive short-circuits the --json execution branch of
// destructive commands when the caller has NOT told wrk it's okay to
// proceed. Under --json the CLI redirects stdout to a bytes.Buffer so
// the plain-text stream never mixes with the machine-readable one;
// but that same redirect means the Confirm prompt writes into the
// buffer and then blocks on stdin waiting for a "yes" that will never
// come. The result on a non-TTY invocation is a silent hang.
//
// Returns nil (no refusal) when --json is off, when --dry-run is on
// (nothing gets written so the prompt would never trigger anyway), or
// when --yes / --force has explicitly consented to the destructive
// path. Otherwise emits a structured refusal to stderr and returns a
// non-nil exitCode so cobra surfaces exit 2.
//
// Callers pass every relevant flag literally (jsonMode, dryRun, yes,
// force). Commands without a --force flag (e.g. `wrk run` in some
// versions) may pass false for the force slot; the check still works.
func refuseJSONInteractive(jsonMode, dryRun, yes, force bool) error {
	if !jsonMode || dryRun || yes || force {
		return nil
	}
	err := engine.Newf(engine.ErrJSONRequiresYes,
		"combine --json with --yes to execute, or --dry-run to preview",
		"--json on a destructive command requires --yes to skip the confirmation prompt")
	emitJSONError(os.Stderr, err)
	return exitCode{code: 2}
}
