package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
)

// ConfirmOptions carries the flag matrix + IO for a destructive
// command's plan/prompt/execute lifecycle.
type ConfirmOptions struct {
	Yes    bool
	Force  bool
	DryRun bool
	// Refusal is non-empty when the command has a safety refusal reason
	// (e.g. "workspace has uncommitted changes"). --force overrides it.
	Refusal string
	Stdin   io.Reader
	Stdout  io.Writer
}

// Decision is what the caller does after Confirm returns.
type Decision int

const (
	Proceed Decision = iota // execute the plan
	Refuse                  // return the Refusal error to the user
	Preview                 // dry-run: return nil after the plan output
)

// Confirm applies the flag matrix documented on ConfirmOptions and
// returns a Decision. Errors returned always describe a user-facing
// safety condition:
//
//   - a set Refusal with no --force override,
//   - non-TTY invocation without --yes, or
//   - Ctrl-D / "no" at the interactive prompt.
//
// TTY detection: if opts.Stdin is a *os.File we honour isatty on its
// descriptor; anything else (tests, pipes, in-memory readers) is
// treated as non-TTY so a missing --yes fails loudly instead of hanging
// on a prompt no human will answer.
func Confirm(opts ConfirmOptions) (Decision, error) {
	treatAsTTY := false
	if f, ok := opts.Stdin.(*os.File); ok {
		treatAsTTY = isatty.IsTerminal(f.Fd())
	}
	return confirmWithReader(opts, treatAsTTY)
}

// confirmWithReader is the tty-detection-free core of Confirm. It exists
// so tests can drive the interactive branch without owning a real TTY.
func confirmWithReader(opts ConfirmOptions, treatAsTTY bool) (Decision, error) {
	// Dry-run wins over everything: no mutation, no refusal check, no
	// prompt. The caller has already printed (or is about to print) the
	// plan; Confirm stays silent.
	if opts.DryRun {
		return Preview, nil
	}

	// A safety refusal — e.g. "workspace has uncommitted changes" —
	// short-circuits normal flag handling. --force is the only way past.
	if opts.Refusal != "" {
		if !opts.Force {
			return Refuse, errors.New(opts.Refusal)
		}
		// Acknowledge the override loudly so the user sees, in the same
		// terminal scrollback, exactly which safety net they disabled.
		fmt.Fprintf(opts.Stdout,
			"\u26a0  refusal overridden by --force: %s\n", opts.Refusal)
		return Proceed, nil
	}

	// No refusal in play: --yes or --force is consent enough.
	if opts.Yes || opts.Force {
		return Proceed, nil
	}

	// No consent flag and no human to ask → refuse rather than block
	// forever on a prompt read from /dev/null.
	if !treatAsTTY {
		return Refuse, errors.New(
			"destructive command requires --yes when not attached to a terminal")
	}

	// Interactive TTY: ask, read one line, treat only y/yes as consent.
	// bufio.ReadString returns io.EOF (with any partial line) on Ctrl-D;
	// the trim+lowercase check below rejects empty answers naturally.
	fmt.Fprint(opts.Stdout, "Continue? [y/N] ")
	line, _ := bufio.NewReader(opts.Stdin).ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer == "y" || answer == "yes" {
		return Proceed, nil
	}
	return Refuse, errors.New("declined at prompt")
}
