package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// TestConfirmRelinkYesSkipsPrompt pins S7: `--yes` short-circuits the
// entire prompt path, regardless of whether stdin is a terminal. The
// output side stays untouched — no banner, no prompt — so scripted
// callers get a clean stream.
func TestConfirmRelinkYesSkipsPrompt(t *testing.T) {
	// A closed pipe as stdin would explode if confirmRelink tried to
	// read; --yes must never touch it.
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = pw.Close()
	defer func() { _ = pr.Close() }()

	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = outR.Close() }()
	defer func() { _ = outW.Close() }()

	if err := confirmRelink(true, pr, outW); err != nil {
		t.Fatalf("confirmRelink(yes=true) = %v, want nil", err)
	}
	_ = outW.Close()
	written, _ := io.ReadAll(outR)
	if len(written) != 0 {
		t.Fatalf("--yes should produce no banner/prompt, got: %q", written)
	}
}

// TestConfirmRelinkNonTTYRefuses pins S7: without --yes and without a
// TTY, confirmRelink refuses immediately. The error must name --yes so
// pipe/CI users know exactly how to unblock themselves.
func TestConfirmRelinkNonTTYRefuses(t *testing.T) {
	// os.Pipe read-end is NOT a terminal, so isatty returns false —
	// this is the non-interactive branch.
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = pw.Close()
	defer func() { _ = pr.Close() }()

	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = outR.Close() }()
	defer func() { _ = outW.Close() }()

	err = confirmRelink(false, pr, outW)
	if err == nil {
		t.Fatal("confirmRelink(yes=false, non-tty) should error")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("error should reference --yes so users know the fix, got: %v", err)
	}
	if !strings.Contains(err.Error(), "refusing to run destructive relink") {
		t.Fatalf("error should identify itself as a destructive-action refusal, got: %v", err)
	}

	// A refusal must NOT print the interactive banner — that would
	// double-alarm scripts capturing stdout while parsing stderr for
	// the error.
	_ = outW.Close()
	written, _ := io.ReadAll(outR)
	if len(written) != 0 {
		t.Fatalf("non-tty refusal should not print the interactive banner, got: %q", written)
	}
}

// TestRelinkFlagsYesRegistered pins S7's flag wiring: both --yes and
// -y point at relinkYes. If the short form ever drifts, users typing
// `wrk relink -y` in a rush would silently trip the unknown-flag
// error, which is exactly the failure mode --yes is meant to prevent.
func TestRelinkFlagsYesRegistered(t *testing.T) {
	long := relinkCmd.Flags().Lookup("yes")
	if long == nil {
		t.Fatal("--yes flag not registered on relinkCmd")
	}
	short := relinkCmd.Flags().ShorthandLookup("y")
	if short == nil {
		t.Fatal("-y shorthand not registered on relinkCmd")
	}
	if long != short {
		t.Fatal("--yes and -y must be the same flag (bound to relinkYes)")
	}
}

// TestRelinkIsolateFlagRegistered pins that --isolate is wired on
// relinkCmd. If this drifts, the whole Task 3.5 CLI surface silently
// disappears — `wrk relink --isolate` would fail as "unknown flag"
// after already having survived through review.
func TestRelinkIsolateFlagRegistered(t *testing.T) {
	if relinkCmd.Flags().Lookup("isolate") == nil {
		t.Fatal("--isolate flag not registered on relinkCmd")
	}
}

// TestRelinkArgsRejectsPositionalWithoutIsolate pins the positional-
// args guard: `wrk relink node` without --isolate is a typo, not a
// scoped relink, and MUST be rejected before RunE ever touches the
// engine. The relinkIsolate package-global is snapshot/restored to
// avoid test-order dependence on other tests that flip the flag.
func TestRelinkArgsRejectsPositionalWithoutIsolate(t *testing.T) {
	old := relinkIsolate
	defer func() { relinkIsolate = old }()
	relinkIsolate = false

	err := relinkCmd.Args(relinkCmd, []string{"node"})
	if err == nil {
		t.Fatal("expected error for positional arg without --isolate")
	}
	if !strings.Contains(err.Error(), "--isolate") {
		t.Fatalf("error should reference --isolate so users know the fix, got: %v", err)
	}
}

// TestRelinkArgsAcceptsPositionalWithIsolate pins the other half of
// the guard: with --isolate set, one or many positional resource
// names are the whole point of the flag. Zero args are also legal
// (means "isolate every detached resource"), so we verify all three.
func TestRelinkArgsAcceptsPositionalWithIsolate(t *testing.T) {
	old := relinkIsolate
	defer func() { relinkIsolate = old }()
	relinkIsolate = true

	for _, args := range [][]string{
		{},
		{"node"},
		{"node", "env"},
	} {
		if err := relinkCmd.Args(relinkCmd, args); err != nil {
			t.Errorf("unexpected error for args %v with --isolate: %v", args, err)
		}
	}
}

// TestConfirmRelinkIsolateYesSkipsPrompt pins the --yes short-circuit:
// the prompt path must never touch stdin (a closed pipe would panic).
// This mirrors TestConfirmRelinkYesSkipsPrompt for the isolate flow.
func TestConfirmRelinkIsolateYesSkipsPrompt(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = pw.Close()
	defer func() { _ = pr.Close() }()

	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = outR.Close() }()
	defer func() { _ = outW.Close() }()

	if err := confirmRelinkIsolate(true, pr, outW); err != nil {
		t.Fatalf("confirmRelinkIsolate(yes=true) = %v, want nil", err)
	}
	_ = outW.Close()
	written, _ := io.ReadAll(outR)
	if len(written) != 0 {
		t.Fatalf("--yes should produce no banner/prompt, got: %q", written)
	}
}

// TestConfirmRelinkIsolateNonTTYRefuses pins the non-interactive
// safety gate: a pipe stdin without --yes is a script that didn't
// consent, so we refuse and name --yes in the error so the caller
// knows what to add. The banner must NOT print — that would confuse
// stderr-parsing scripts.
func TestConfirmRelinkIsolateNonTTYRefuses(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = pw.Close()
	defer func() { _ = pr.Close() }()

	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = outR.Close() }()
	defer func() { _ = outW.Close() }()

	err = confirmRelinkIsolate(false, pr, outW)
	if err == nil {
		t.Fatal("confirmRelinkIsolate(yes=false, non-tty) should error")
	}
	if !strings.Contains(err.Error(), "--yes required") {
		t.Fatalf("error should reference --yes required, got: %v", err)
	}

	_ = outW.Close()
	written, _ := io.ReadAll(outR)
	if len(written) != 0 {
		t.Fatalf("non-tty refusal should not print the interactive banner, got: %q", written)
	}
}
