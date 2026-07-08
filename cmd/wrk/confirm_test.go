package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestConfirmDryRunAlwaysPreviews(t *testing.T) {
	// DryRun wins regardless of Yes, Force, or Refusal.
	cases := []struct {
		name string
		opts ConfirmOptions
	}{
		{"bare", ConfirmOptions{DryRun: true}},
		{"with refusal", ConfirmOptions{DryRun: true, Refusal: "whatever"}},
		{"with force", ConfirmOptions{DryRun: true, Force: true, Refusal: "whatever"}},
		{"with yes", ConfirmOptions{DryRun: true, Yes: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.Stdin = strings.NewReader("")
			var buf bytes.Buffer
			tc.opts.Stdout = &buf
			dec, err := Confirm(tc.opts)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if dec != Preview {
				t.Fatalf("decision = %v, want Preview", dec)
			}
			if buf.Len() != 0 {
				t.Fatalf("dry-run wrote %q, want no output", buf.String())
			}
		})
	}
}

func TestConfirmRefusalWithoutForce(t *testing.T) {
	opts := ConfirmOptions{
		Refusal: "workspace has uncommitted changes",
		Stdin:   strings.NewReader("y\n"),
		Stdout:  &bytes.Buffer{},
	}
	dec, err := Confirm(opts)
	if dec != Refuse {
		t.Fatalf("decision = %v, want Refuse", dec)
	}
	if err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("err = %v, want to mention refusal reason", err)
	}
}

func TestConfirmRefusalOverriddenByForce(t *testing.T) {
	var buf bytes.Buffer
	opts := ConfirmOptions{
		Refusal: "workspace has uncommitted changes",
		Force:   true,
		Stdin:   strings.NewReader(""),
		Stdout:  &buf,
	}
	dec, err := Confirm(opts)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if dec != Proceed {
		t.Fatalf("decision = %v, want Proceed", dec)
	}
	if !strings.Contains(buf.String(), "--force") {
		t.Errorf("stdout %q missing --force acknowledgement", buf.String())
	}
	if !strings.Contains(buf.String(), "uncommitted") {
		t.Errorf("stdout %q missing refusal reason", buf.String())
	}
}

func TestConfirmNonTTYRequiresYes(t *testing.T) {
	// Stdin is a strings.Reader (not *os.File) so isatty returns false.
	opts := ConfirmOptions{
		Stdin:  strings.NewReader(""),
		Stdout: &bytes.Buffer{},
	}
	dec, err := Confirm(opts)
	if dec != Refuse {
		t.Fatalf("decision = %v, want Refuse", dec)
	}
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("err = %v, want mention of --yes", err)
	}
}

func TestConfirmYesProceeds(t *testing.T) {
	opts := ConfirmOptions{
		Yes:    true,
		Stdin:  strings.NewReader(""),
		Stdout: &bytes.Buffer{},
	}
	dec, err := Confirm(opts)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if dec != Proceed {
		t.Fatalf("decision = %v, want Proceed", dec)
	}
}

func TestConfirmForceProceeds(t *testing.T) {
	opts := ConfirmOptions{
		Force:  true,
		Stdin:  strings.NewReader(""),
		Stdout: &bytes.Buffer{},
	}
	dec, err := Confirm(opts)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if dec != Proceed {
		t.Fatalf("decision = %v, want Proceed", dec)
	}
}

func TestConfirmInteractivePromptYes(t *testing.T) {
	var buf bytes.Buffer
	dec, err := confirmWithReader(ConfirmOptions{
		Stdin:  strings.NewReader("y\n"),
		Stdout: &buf,
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if dec != Proceed {
		t.Fatalf("decision = %v, want Proceed", dec)
	}
	if !strings.Contains(buf.String(), "Continue?") {
		t.Errorf("stdout %q missing prompt", buf.String())
	}
}

func TestConfirmInteractivePromptYesLong(t *testing.T) {
	// "yes" (any case) should also proceed.
	var buf bytes.Buffer
	dec, err := confirmWithReader(ConfirmOptions{
		Stdin:  strings.NewReader("YES\n"),
		Stdout: &buf,
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if dec != Proceed {
		t.Fatalf("decision = %v, want Proceed", dec)
	}
}

func TestConfirmInteractivePromptNo(t *testing.T) {
	dec, err := confirmWithReader(ConfirmOptions{
		Stdin:  strings.NewReader("n\n"),
		Stdout: &bytes.Buffer{},
	}, true)
	if dec != Refuse {
		t.Fatalf("decision = %v, want Refuse", dec)
	}
	if err == nil || !strings.Contains(err.Error(), "declined") {
		t.Fatalf("err = %v, want mention of declined", err)
	}
}

func TestConfirmInteractivePromptEmpty(t *testing.T) {
	// Bare newline is the default "N".
	dec, err := confirmWithReader(ConfirmOptions{
		Stdin:  strings.NewReader("\n"),
		Stdout: &bytes.Buffer{},
	}, true)
	if dec != Refuse {
		t.Fatalf("decision = %v, want Refuse", dec)
	}
	if err == nil || !strings.Contains(err.Error(), "declined") {
		t.Fatalf("err = %v, want mention of declined", err)
	}
}

func TestConfirmInteractivePromptEOF(t *testing.T) {
	dec, err := confirmWithReader(ConfirmOptions{
		Stdin:  strings.NewReader(""),
		Stdout: &bytes.Buffer{},
	}, true)
	if dec != Refuse {
		t.Fatalf("decision = %v, want Refuse", dec)
	}
	if err == nil {
		t.Fatalf("expected error at EOF")
	}
}

func TestConfirmInteractiveRefusalStillRefuses(t *testing.T) {
	// A refusal with no --force must not fall into the prompt path.
	dec, err := confirmWithReader(ConfirmOptions{
		Refusal: "workspace has uncommitted changes",
		Stdin:   strings.NewReader("y\n"),
		Stdout:  &bytes.Buffer{},
	}, true)
	if dec != Refuse {
		t.Fatalf("decision = %v, want Refuse", dec)
	}
	if err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("err = %v, want refusal reason", err)
	}
}
