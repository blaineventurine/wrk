package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/blaineventurine/wrk/internal/engine"
)

// decodeErrorEnvelope decodes emitJSONError's payload from buf into a
// small anonymous struct — sharing the shape across the tests keeps
// each assertion tight and prevents drift between the tests and the
// wire format.
func decodeErrorEnvelope(t *testing.T, buf *bytes.Buffer) (code, message, hint string) {
	t.Helper()
	trimmed := bytes.TrimSpace(buf.Bytes())
	if len(trimmed) == 0 {
		t.Fatalf("emitJSONError wrote nothing")
	}
	var out struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Hint    string `json:"hint"`
		} `json:"error"`
	}
	if err := json.Unmarshal(trimmed, &out); err != nil {
		t.Fatalf("emitJSONError bad JSON: %v\nraw: %s", err, buf.String())
	}
	return out.Error.Code, out.Error.Message, out.Error.Hint
}

// TestEmitJSONErrorWithTypedError pins the primary contract: a
// *engine.Error surfaces its Code, Message, and Hint verbatim into
// the envelope so agents can route on the stable code string.
func TestEmitJSONErrorWithTypedError(t *testing.T) {
	err := engine.Newf(engine.ErrResourceNotConfigured,
		"run 'wrk list' to see configured resources",
		"resource %q not configured", "node")

	var buf bytes.Buffer
	emitJSONError(&buf, err)

	code, msg, hint := decodeErrorEnvelope(t, &buf)
	if code != "resource_not_configured" {
		t.Errorf("code = %q, want %q", code, "resource_not_configured")
	}
	if msg != `resource "node" not configured` {
		t.Errorf("message = %q", msg)
	}
	if hint != "run 'wrk list' to see configured resources" {
		t.Errorf("hint = %q", hint)
	}
}

// TestEmitJSONErrorWithGenericError pins the fallback: a plain
// error (not *engine.Error) surfaces as ErrUnknown so consumers
// still get a well-formed envelope with the original message body.
func TestEmitJSONErrorWithGenericError(t *testing.T) {
	err := fmt.Errorf("something else went wrong")

	var buf bytes.Buffer
	emitJSONError(&buf, err)

	code, msg, hint := decodeErrorEnvelope(t, &buf)
	if code != "unknown" {
		t.Errorf("code = %q, want %q", code, "unknown")
	}
	if msg != "something else went wrong" {
		t.Errorf("message = %q", msg)
	}
	if hint != "" {
		t.Errorf("hint = %q, want empty", hint)
	}
}

// TestEmitJSONErrorPreservesWrappedError pins the errors.As
// traversal: a *engine.Error wrapped inside a plain fmt.Errorf still
// surfaces its Code — critical because engine errors bubble up
// through cobra + local helpers that may add context via `%w`.
func TestEmitJSONErrorPreservesWrappedError(t *testing.T) {
	inner := engine.Newf(engine.ErrHookCommandFailed,
		"inspect the hook's stderr for details",
		"hook failed")
	wrapped := fmt.Errorf("context: %w", inner)

	var buf bytes.Buffer
	emitJSONError(&buf, wrapped)

	code, _, hint := decodeErrorEnvelope(t, &buf)
	if code != "hook_command_failed" {
		t.Errorf("code = %q, want %q", code, "hook_command_failed")
	}
	if hint != "inspect the hook's stderr for details" {
		t.Errorf("hint = %q", hint)
	}
}

// TestEmitJSONErrorOmitsEmptyHint pins the JSON tag contract: when
// Hint is empty, the marshalled envelope MUST NOT carry a `hint` key
// at all — the `omitempty` tag on the field promises callers a small,
// stable shape whose optional slots are absent rather than present-
// and-blank.
func TestEmitJSONErrorOmitsEmptyHint(t *testing.T) {
	err := engine.Newf(engine.ErrConfigInvalid, "", "config load failed")

	var buf bytes.Buffer
	emitJSONError(&buf, err)

	if bytes.Contains(buf.Bytes(), []byte(`"hint"`)) {
		t.Errorf("empty hint should be omitted, got:\n%s", buf.String())
	}
}

// TestEmitJSONErrorEndsWithNewline pins the shell-friendliness
// convention shared with writeJSON: the envelope terminates with
// "\n" so pipelines that treat the line as a record don't have to
// paper over it.
func TestEmitJSONErrorEndsWithNewline(t *testing.T) {
	err := engine.Newf(engine.ErrUnknown, "", "boom")
	var buf bytes.Buffer
	emitJSONError(&buf, err)

	if buf.Len() == 0 || buf.Bytes()[buf.Len()-1] != '\n' {
		t.Errorf("expected trailing newline, got:\n%q", buf.String())
	}
}

// TestEmitJSONErrorWithNilNoop pins that a nil error writes nothing
// — callers on the happy path can safely funnel every error through
// emitJSONError without a defensive nil check.
func TestEmitJSONErrorWithNilNoop(t *testing.T) {
	var buf bytes.Buffer
	emitJSONError(&buf, nil)

	if buf.Len() != 0 {
		t.Errorf("nil error wrote %q, want empty", buf.String())
	}
}
