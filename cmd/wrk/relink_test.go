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
