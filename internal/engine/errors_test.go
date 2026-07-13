package engine

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestNewfPopulatesFields pins the constructor's contract: every
// argument slot on *Error is populated with the corresponding input,
// and the format string is expanded via fmt.Sprintf.
func TestNewfPopulatesFields(t *testing.T) {
	err := Newf(ErrResourceNotConfigured,
		"run 'wrk list' to see configured resources",
		"resource %q not configured", "node")

	if err.Code != ErrResourceNotConfigured {
		t.Errorf("code = %q, want %q", err.Code, ErrResourceNotConfigured)
	}
	want := `resource "node" not configured`
	if err.Message != want {
		t.Errorf("message = %q, want %q", err.Message, want)
	}
	if err.Hint != "run 'wrk list' to see configured resources" {
		t.Errorf("hint = %q", err.Hint)
	}
	if err.Wrapped != nil {
		t.Errorf("wrapped = %v, want nil", err.Wrapped)
	}
}

// TestErrorImplementsErrorInterface pins that *Error satisfies the
// standard error interface AND that Error() returns Message verbatim
// when Wrapped is nil — the pre-typing wording MUST survive so tests
// that grep on error strings still pass.
func TestErrorImplementsErrorInterface(t *testing.T) {
	var e error = Newf(ErrUnknown, "", "boom")
	if e.Error() != "boom" {
		t.Errorf("Error() = %q, want %q", e.Error(), "boom")
	}
}

// TestErrorWithWrappedAppendsCause pins the joined-message shape:
// when Wrapped is non-nil, Error() joins Message and Wrapped.Error()
// with ": " so the underlying cause remains visible in %v output.
func TestErrorWithWrappedAppendsCause(t *testing.T) {
	inner := errors.New("underlying detail")
	e := &Error{Code: ErrUnknown, Message: "outer message", Wrapped: inner}
	got := e.Error()
	if !strings.Contains(got, "outer message") {
		t.Errorf("Error() = %q, missing outer message", got)
	}
	if !strings.Contains(got, "underlying detail") {
		t.Errorf("Error() = %q, missing wrapped cause", got)
	}
}

// TestErrorUnwrapEnablesErrorsIs pins the Unwrap plumbing: errors.Is
// traverses through *Error via Unwrap to reach whatever it wraps, so
// callers that keep sentinel errors on the Wrapped side stay
// reachable.
func TestErrorUnwrapEnablesErrorsIs(t *testing.T) {
	inner := errors.New("underlying")
	e := &Error{Code: ErrUnknown, Message: "wrapped", Wrapped: inner}
	if !errors.Is(e, inner) {
		t.Errorf("errors.Is should find inner via Unwrap")
	}
}

// TestErrorsAsFindsWrapped pins the primary agent-facing contract:
// after arbitrary wrapping via fmt.Errorf("...: %w", err), errors.As
// still recovers the original *Error so CLI code can route on Code.
func TestErrorsAsFindsWrapped(t *testing.T) {
	inner := Newf(ErrResourceNotConfigured, "hint", "specific message")
	wrapped := fmt.Errorf("outer: %w", inner)

	var target *Error
	if !errors.As(wrapped, &target) {
		t.Fatal("errors.As should find wrapped *Error")
	}
	if target.Code != ErrResourceNotConfigured {
		t.Errorf("code = %q, want %q", target.Code, ErrResourceNotConfigured)
	}
	if target.Hint != "hint" {
		t.Errorf("hint = %q, want %q", target.Hint, "hint")
	}
}

// TestErrorCodeStringForms pins the wire form of every declared
// ErrorCode — these strings ship in the machine-readable stderr
// envelope, so accidentally renaming one is a breaking API change we
// want a test to catch.
func TestErrorCodeStringForms(t *testing.T) {
	cases := []struct {
		got  ErrorCode
		want string
	}{
		{ErrResourceNotConfigured, "resource_not_configured"},
		{ErrResourceNotFingerprinted, "resource_not_fingerprinted"},
		{ErrResourceNoHook, "resource_no_hook"},
		{ErrResourceDetached, "resource_detached"},
		{ErrResourceNotDetached, "resource_not_detached"},
		{ErrPrimaryWorkspace, "primary_workspace"},
		{ErrCurrentWorkspace, "current_workspace"},
		{ErrNotLiveWorkspace, "not_live_workspace"},
		{ErrUncommittedChanges, "uncommitted_changes"},
		{ErrDetachedFiles, "detached_files"},
		{ErrPlanConflict, "plan_conflict"},
		{ErrHookCommandFailed, "hook_command_failed"},
		{ErrConfigInvalid, "config_invalid"},
		{ErrNotARepository, "not_a_repository"},
		{ErrConfirmDeclined, "confirm_declined"},
		{ErrUnknown, "unknown"},
	}
	for _, c := range cases {
		if string(c.got) != c.want {
			t.Errorf("ErrorCode string = %q, want %q", c.got, c.want)
		}
	}
}
