package engine

import "fmt"

// ErrorCode is the stable identifier for a class of user-facing
// failure. Agent parsers switch on these to route recovery logic;
// changing an existing code (or its string form) is a breaking API
// change. New codes are additive.
//
// The canonical rendering — via emitJSONError under `wrk <cmd> --json`
// — puts the code's string form in the `error.code` field of the
// structured stderr envelope. The wire form is the constant's untyped
// string literal ("resource_not_configured", etc.), never the Go
// identifier.
type ErrorCode string

const (
	// ErrResourceNotConfigured: the CLI asked for a resource name that
	// does not appear in .wrk.yml. Recovery: `wrk list`.
	ErrResourceNotConfigured ErrorCode = "resource_not_configured"

	// ErrResourceNotFingerprinted: the resource exists but has no
	// fingerprint block. `wrk fingerprint` has nothing to compare.
	ErrResourceNotFingerprinted ErrorCode = "resource_not_fingerprinted"

	// ErrResourceNoHook: the resource has no initialize hook.
	// `wrk run` has nothing to execute.
	ErrResourceNoHook ErrorCode = "resource_no_hook"

	// ErrResourceDetached: the resource is currently detached in this
	// workspace; a command that only affects the shared variant would
	// have no visible effect on the workspace's independent copy.
	ErrResourceDetached ErrorCode = "resource_detached"

	// ErrResourceNotDetached: the resource is currently linked, not
	// detached; a command that only makes sense against a detached
	// resource (e.g. `wrk relink --isolate`) refuses cleanly.
	ErrResourceNotDetached ErrorCode = "resource_not_detached"

	// ErrPrimaryWorkspace: the target is the primary workspace of this
	// repository — the anchor everything else hangs off. `wrk remove`
	// refuses it hard; --force cannot override.
	ErrPrimaryWorkspace ErrorCode = "primary_workspace"

	// ErrCurrentWorkspace: the target is the workspace the caller is
	// currently inside. Pulling the ground out from under a running
	// process is refused hard.
	ErrCurrentWorkspace ErrorCode = "current_workspace"

	// ErrNotLiveWorkspace: the target is neither a live workspace of
	// this repo nor a ghost with a stale registry entry. Almost
	// certainly a typo.
	ErrNotLiveWorkspace ErrorCode = "not_live_workspace"

	// ErrUncommittedChanges: `wrk remove` refused because the target
	// workspace still has uncommitted VCS changes. Soft refusal —
	// --force overrides. Reserved: current sites surface this via
	// plan.Refusal, not a typed error.
	ErrUncommittedChanges ErrorCode = "uncommitted_changes"

	// ErrDetachedFiles: `wrk remove` or `wrk forget` refused because
	// detached-file registry entries still exist. Soft refusal —
	// --force overrides. Reserved: current sites surface this via
	// plan.Refusal, not a typed error.
	ErrDetachedFiles ErrorCode = "detached_files"

	// ErrPlanConflict: a plan's actions collide (e.g. two variants
	// racing to occupy the same storage subdirectory).
	ErrPlanConflict ErrorCode = "plan_conflict"

	// ErrHookCommandFailed: a user-configured hook command exited
	// non-zero. Details land in the wrapped error's message.
	ErrHookCommandFailed ErrorCode = "hook_command_failed"

	// ErrConfigInvalid: `.wrk.yml` (or its `.wrk.local.yml` overlay)
	// failed to load or validate.
	ErrConfigInvalid ErrorCode = "config_invalid"

	// ErrNotARepository: the current working directory is not inside
	// a supported VCS repository.
	ErrNotARepository ErrorCode = "not_a_repository"

	// ErrConfirmDeclined: the interactive prompt got a "no" (or
	// EOF on stdin without --yes / --force).
	ErrConfirmDeclined ErrorCode = "confirm_declined"

	// ErrJSONRequiresYes: a destructive command refused to enter its
	// --json execution branch because no consent flag was set. The
	// human path would have opened a Confirm prompt; under --json the
	// CLI redirects stdout to a bytes.Buffer so the prompt would write
	// into that buffer and then block on stdin waiting for a "yes"
	// that never comes. Fix: pass --yes (or --force) to skip the
	// prompt, or --dry-run to preview the plan without touching
	// anything.
	ErrJSONRequiresYes ErrorCode = "json_requires_yes"

	// ErrUnknown is the fallback code for any error that is not a
	// typed *Error. emitJSONError attaches it when errors.As fails.
	ErrUnknown ErrorCode = "unknown"
)

// Error carries an ErrorCode alongside a formatted user-facing
// message and an optional recovery hint. It implements the standard
// error interface AND exposes Unwrap so both `errors.Is` and
// `errors.As` work through arbitrary `fmt.Errorf("...: %w", err)`
// wrappers.
//
// Usage:
//
//	var wrkErr *engine.Error
//	if errors.As(err, &wrkErr) {
//	    switch wrkErr.Code {
//	    case engine.ErrResourceNotConfigured:
//	        ...
//	    }
//	}
//
// Message is what Error() returns when Wrapped is nil — preserving
// the pre-typing wording so tests that grep on the message still
// pass. When Wrapped is non-nil, Error() joins the two with ": " so
// the underlying cause remains visible in human output.
type Error struct {
	Code    ErrorCode
	Message string
	Hint    string
	Wrapped error
}

// Error returns the user-facing message. When Wrapped is non-nil the
// wrapped error's own Error() is appended after ": " so no context is
// lost when the value is stringified (typical of a `%v` in log or
// stderr output).
func (e *Error) Error() string {
	if e.Wrapped == nil {
		return e.Message
	}
	wrapped := e.Wrapped.Error()
	// When Message is empty or already restates the wrapped cause
	// (typical of Wrapf callers passing "%s", err.Error()), skip the
	// duplicate join — the wire form must never double the underlying
	// text.
	if e.Message == "" || e.Message == wrapped {
		return wrapped
	}
	return e.Message + ": " + wrapped
}

// Unwrap exposes the wrapped cause so `errors.Is` and `errors.As`
// traverse through this Error to reach whatever it wraps. Returns
// nil when nothing is wrapped.
func (e *Error) Unwrap() error { return e.Wrapped }

// Newf constructs a typed *Error with a formatted Message. The
// format string and args match fmt.Errorf's contract: use %q for
// quoted names, %s for freeform strings, %w to wrap an underlying
// error (in which case populate Wrapped separately since Newf does
// not scan args for %w).
//
// hint is optional — pass "" to omit. When non-empty, agents see it
// in the structured stderr envelope's `error.hint` field.
func Newf(code ErrorCode, hint, format string, args ...any) *Error {
	return &Error{
		Code:    code,
		Message: fmt.Sprintf(format, args...),
		Hint:    hint,
	}
}

// Wrapf constructs a typed *Error that both surfaces a formatted
// message and preserves an underlying error in the Unwrap chain so
// errors.Is and errors.As traverse into whatever caller-side sentinel
// the wrapped error carries. Callers use this at engine-layer error
// sites (e.g. wrapping config.Load failures) to route the machine-
// readable envelope onto a stable code without losing the wrapped
// cause.
//
// The format+args pair populates Message via fmt.Sprintf (same
// contract as Newf); pass "%s", err.Error() to mirror the wrapped
// error's text verbatim — Error() collapses that mirror case so the
// human-visible form never doubles the underlying string.
//
// hint is optional — pass "" to omit.
func Wrapf(code ErrorCode, hint string, err error, format string, args ...any) *Error {
	return &Error{
		Code:    code,
		Message: fmt.Sprintf(format, args...),
		Hint:    hint,
		Wrapped: err,
	}
}
