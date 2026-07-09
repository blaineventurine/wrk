package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/engine"
)

// TestHasProblemsProblemStates confirms every state that `wrk link`
// would fix is treated as a problem. This includes both actionable
// failures (conflict, stale, absent) and the "fresh checkout" states
// (pending, missing, not-linked) that a first-run link resolves.
func TestHasProblemsProblemStates(t *testing.T) {
	for _, s := range []engine.State{
		engine.StateConflict,
		engine.StateStale,
		engine.StateAbsent,
		engine.StatePending,
		engine.StateMissing,
		engine.StateNotLinked,
	} {
		t.Run(string(s), func(t *testing.T) {
			rows := []engine.ResourceStatus{{State: s}}
			if !hasProblems(rows) {
				t.Fatalf("hasProblems(%s) = false, want true", s)
			}
		})
	}
}

// TestHasProblemsIntentionalStates confirms that intentional states
// (linked, detached, expected) are NOT reported as problems — they
// represent a workspace that is either healthy or deliberately opted
// out of shared storage.
func TestHasProblemsIntentionalStates(t *testing.T) {
	for _, s := range []engine.State{
		engine.StateLinked,
		engine.StateDetached,
		engine.StateExpected,
	} {
		t.Run(string(s), func(t *testing.T) {
			rows := []engine.ResourceStatus{{State: s}}
			if hasProblems(rows) {
				t.Fatalf("hasProblems(%s) = true, want false", s)
			}
		})
	}
}

// TestHasProblemsEmpty confirms an empty status report is not a
// problem — no resources means nothing needs attention.
func TestHasProblemsEmpty(t *testing.T) {
	if hasProblems(nil) {
		t.Fatal("hasProblems(nil) = true, want false")
	}
}

// TestHasProblemsMixedReportsFirstProblem confirms a mixed set of
// states triggers when any single row is problematic — the exit-code
// flag should fire on the first problem.
func TestHasProblemsMixedReportsFirstProblem(t *testing.T) {
	rows := []engine.ResourceStatus{
		{State: engine.StateLinked},
		{State: engine.StateExpected},
		{State: engine.StateMissing}, // this one is a problem
		{State: engine.StateLinked},
	}
	if !hasProblems(rows) {
		t.Fatal("hasProblems(mixed with missing) = false, want true")
	}
}

// TestExitCodeRoundTripsThroughErrorsAs pins U4: the exitCode sentinel
// MUST survive being wrapped in another error via fmt.Errorf("%w",…).
// If it doesn't, Execute() falls through to the generic error path
// (exit 2 + stderr) instead of surfacing the intended exit signal
// (silent exit 1). Wrappings happen every time a middle layer adds
// context, so this invariant is load-bearing for `wrk status
// --exit-code` in a real call graph.
func TestExitCodeRoundTripsThroughErrorsAs(t *testing.T) {
	original := exitCode{code: 1}

	wrapped := fmt.Errorf("something happened: %w", original)

	var got exitCode
	if !errors.As(wrapped, &got) {
		t.Fatalf("errors.As failed to recover exitCode from %v", wrapped)
	}
	if got.code != 1 {
		t.Fatalf("recovered code = %d, want 1", got.code)
	}
}

// TestExitCodeDoubleWrapStillRoundTrips confirms the invariant holds
// through nested wrapping too — real call graphs stack multiple
// wrapContext levels. errors.As must still unwrap the sentinel and
// yield the exit code intact.
func TestExitCodeDoubleWrapStillRoundTrips(t *testing.T) {
	inner := exitCode{code: 1}
	wrapped := fmt.Errorf("first: %w", fmt.Errorf("second: %w", inner))

	var got exitCode
	if !errors.As(wrapped, &got) {
		t.Fatalf("errors.As failed to recover exitCode through two wrappers: %v", wrapped)
	}
	if got.code != 1 {
		t.Fatalf("code through double wrap = %d, want 1", got.code)
	}
}

// TestExitCodeIsSilentByDesign pins the "no message on stderr" side
// of the sentinel: Error() must return "" so the top-level Execute
// can distinguish the exit-signal path (silent) from a real error
// (loud). If someone gives exitCode a non-empty Error() the
// --exit-code contract breaks — status would print junk to stderr
// on every pre-commit hook.
func TestExitCodeIsSilentByDesign(t *testing.T) {
	if msg := (exitCode{code: 1}).Error(); msg != "" {
		t.Fatalf("exitCode.Error() = %q, want empty string", msg)
	}
}

// TestShortFingerprintTruncation pins the boundary behaviour of
// short() — it is what the FINGERPRINT column depends on. An empty
// input yields "-", a short-enough input passes through, and a long
// input is truncated to exactly 12 characters (the width the columns
// are laid out for).
func TestShortFingerprintTruncation(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "-"},
		{"abc", "abc"},
		{strings.Repeat("f", 12), strings.Repeat("f", 12)},
		{"abcdef012345Z", "abcdef012345"},
		{strings.Repeat("f", 64), strings.Repeat("f", 12)},
	}
	for _, c := range cases {
		if got := short(c.in); got != c.want {
			t.Errorf("short(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestHasNonSharedOriginDetection confirms the ORIGIN-column gate
// used by printStatus fires only when at least one row is non-shared.
// This helper drives whether the status table gains an extra column,
// so its truth table is worth pinning explicitly.
func TestHasNonSharedOriginDetection(t *testing.T) {
	cases := []struct {
		name string
		rows []engine.ResourceStatus
		want bool
	}{
		{"empty", nil, false},
		{"all shared", []engine.ResourceStatus{{Origin: "shared"}, {Origin: "shared"}}, false},
		{"one local", []engine.ResourceStatus{{Origin: "shared"}, {Origin: "local"}}, true},
		{"one override", []engine.ResourceStatus{{Origin: "shared"}, {Origin: "local-override"}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasNonSharedOrigin(c.rows); got != c.want {
				t.Errorf("hasNonSharedOrigin = %v, want %v", got, c.want)
			}
		})
	}
}

// TestPrintStatusJSONEmitsSchemaEnvelope confirms the JSON output
// carries the shared envelope (schema=1, kind="status"), lists the
// configured workspace as primary, and terminates with a newline for
// shell-friendliness.
func TestPrintStatusJSONEmitsSchemaEnvelope(t *testing.T) {
	report := &engine.StatusReport{
		Sources: []string{"/repo/.wrk.yml"},
		Rows: []engine.ResourceStatus{{
			WorkspaceRoot: "/repo",
			Resource:      "node",
			Path:          "node_modules",
			SharedPath:    "/storage/repo/node_modules/5fd1d0d610ba6c17",
			Fingerprint:   "5fd1d0d610ba6c17",
			State:         engine.StateLinked,
			Origin:        config.OriginShared,
		}},
	}
	var buf bytes.Buffer
	if err := printStatusJSON(&buf, report, "/repo"); err != nil {
		t.Fatalf("printStatusJSON: %v", err)
	}
	// Trailing newline for shell-friendliness.
	if !bytes.HasSuffix(buf.Bytes(), []byte("\n")) {
		t.Errorf("output missing trailing newline:\n%s", buf.String())
	}
	var out struct {
		Schema     int      `json:"schema"`
		Kind       string   `json:"kind"`
		Sources    []string `json:"sources"`
		Workspaces []struct {
			Root      string `json:"root"`
			IsPrimary bool   `json:"isPrimary"`
		} `json:"workspaces"`
	}
	// Trim trailing newline before parsing.
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &out); err != nil {
		t.Fatalf("invalid JSON:\n%s\n%v", buf.String(), err)
	}
	if out.Schema != 1 || out.Kind != "status" {
		t.Errorf("envelope wrong: schema=%d kind=%q", out.Schema, out.Kind)
	}
	if len(out.Workspaces) != 1 || !out.Workspaces[0].IsPrimary {
		t.Errorf("expected 1 primary workspace, got %+v", out.Workspaces)
	}
}

// TestPrintStatusJSONNilReport confirms a nil report yields a stable
// envelope, not a panic or a JSON payload full of nulls.
func TestPrintStatusJSONNilReport(t *testing.T) {
	var buf bytes.Buffer
	if err := printStatusJSON(&buf, nil, "/repo"); err != nil {
		t.Fatalf("printStatusJSON(nil): %v", err)
	}
	if bytes.Contains(buf.Bytes(), []byte("null")) {
		t.Errorf("expected [] not null in output:\n%s", buf.String())
	}
}

// TestPrintStatusJSONPropagatesWriterError pins the failure path: a
// downstream write error must surface to the caller so the CLI can
// exit 2 rather than silently succeeding on a broken stdout.
func TestPrintStatusJSONPropagatesWriterError(t *testing.T) {
	report := &engine.StatusReport{}
	err := printStatusJSON(&failingWriter{}, report, "/repo")
	if err == nil {
		t.Fatal("expected error from failing writer, got nil")
	}
}

type failingWriter struct{}

func (failingWriter) Write(_ []byte) (int, error) {
	return 0, errors.New("disk full")
}
