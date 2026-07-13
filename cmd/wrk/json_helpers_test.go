package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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

// TestRefuseJSONInteractiveNilCases pins the "no refusal" contract:
// every combination that either turns --json off, opts into a preview
// (--dry-run), or explicitly consents (--yes / --force) MUST return
// nil so the caller falls through to the real RunE body.
//
// Regression bait: an off-by-one on the guard would either silently
// hang a `wrk gc --json --yes` invocation (blocking on Confirm) or
// force every non-JSON caller through the refusal path.
func TestRefuseJSONInteractiveNilCases(t *testing.T) {
	cases := []struct {
		name                        string
		jsonMode, dryRun, yes, force bool
	}{
		{"json off", false, false, false, false},
		{"json off with yes", false, false, true, false},
		{"json off with force", false, false, false, true},
		{"json + dry-run", true, true, false, false},
		{"json + yes", true, false, true, false},
		{"json + force", true, false, false, true},
		{"json + dry-run + yes + force", true, true, true, true},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			err := refuseJSONInteractive(c.jsonMode, c.dryRun, c.yes, c.force)
			if err != nil {
				t.Errorf("got %v, want nil", err)
			}
		})
	}
}

// TestRefuseJSONInteractiveRefusalCarriesExitCodeAndJSONEnvelope
// pins the primary refusal contract: `--json` alone (no consent flag,
// not --dry-run) returns exitCode{2} AND writes a structured error
// envelope to stderr whose code is `json_requires_yes`. Under a
// stderr-capturing redirect, the payload must be parseable JSON so
// downstream agent tooling sees the same shape production does.
func TestRefuseJSONInteractiveRefusalCarriesExitCodeAndJSONEnvelope(t *testing.T) {
	// Redirect os.Stderr to a pipe so we can capture the payload. The
	// helper writes directly to os.Stderr to match every other JSON
	// error emitter in the CLI package.
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = origStderr })

	got := refuseJSONInteractive(true, false, false, false)
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}

	if got == nil {
		t.Fatal("got nil, want exitCode")
	}
	var ec exitCode
	if !errors.As(got, &ec) {
		t.Fatalf("errors.As should recover exitCode, got %T: %v", got, got)
	}
	if ec.code != 2 {
		t.Errorf("exit code = %d, want 2", ec.code)
	}

	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("read stderr: %v", err)
	}

	code, message, hint := decodeErrorEnvelope(t, buf)
	if code != string(engine.ErrJSONRequiresYes) {
		t.Errorf("code = %q, want %q", code, engine.ErrJSONRequiresYes)
	}
	if message == "" {
		t.Error("message is empty")
	}
	if hint == "" {
		t.Error("hint is empty")
	}
}
