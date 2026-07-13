package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
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
