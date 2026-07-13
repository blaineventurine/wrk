package engine

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
)

// TestLinkErrorsOnMissingConfig pins that Link on a repo with no
// .wrk.yml AND no .wrk.local.yml surfaces config.ErrConfigNotFound
// through the wrapped message config.Load produces. Without this,
// a fresh checkout of an unrelated repo would silently no-op
// through `wrk link` and hide the real error ("this isn't a wrk
// repo yet — run `wrk init`").
func TestLinkErrorsOnMissingConfig(t *testing.T) {
	repo := newTestRepo(t)

	err := Link(repo, Options{
		StorageRoot: storageIn(t, repo.Root),
		Stdout:      &bytes.Buffer{},
	})
	if err == nil {
		t.Fatalf("Link with no config: got nil error, want failure")
	}

	// The wrapped message from config.Load is
	//   "configuration file not found: neither .wrk.yml nor .wrk.local.yml in <root>"
	// (see internal/config/errors.go ErrConfigNotFound and load.go).
	// Pin the sentinel phrase so a wording drift is caught, and pin
	// both filenames so a regression that mentioned only one leaves
	// the user hunting.
	msg := err.Error()
	for _, want := range []string{
		"configuration file not found",
		config.Filename,
		config.LocalFilename,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q\nfull error: %s", want, msg)
		}
	}
}

// TestLinkErrorsOnMalformedYAML pins that a syntactically broken
// .wrk.yml fails Link with a diagnostic that identifies the file
// and the parse layer. The user needs to know it wasn't wrk logic
// that rejected their config — it was YAML syntax — so the message
// must surface enough context to point them at the fix.
//
// loadFile wraps the yaml.Unmarshal error under
// config.ErrInvalidConfig ("invalid configuration: <yaml.err>"), so
// we pin the sentinel prefix rather than the underlying library's
// exact wording (which is not our API).
func TestLinkErrorsOnMalformedYAML(t *testing.T) {
	repo := newTestRepo(t)

	// An open `[` with no closing bracket is a hard YAML parse error —
	// the tokenizer refuses to emit any document. This is a stable
	// failure mode across the go-yaml versions the module uses.
	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: env\n"+
			"    path: [\n",
	)

	err := Link(repo, Options{
		StorageRoot: storageIn(t, repo.Root),
		Stdout:      &bytes.Buffer{},
	})
	if err == nil {
		t.Fatalf("Link on malformed YAML: got nil error, want failure")
	}

	msg := err.Error()
	if !strings.Contains(msg, "invalid configuration") {
		t.Errorf("error missing the %q sentinel; full error: %s",
			"invalid configuration", msg)
	}
}

// TestLinkErrorsOnAbsolutePath pins that a resource whose Path is
// absolute (e.g. /etc/passwd — the canonical footgun) is rejected
// by validate() before any planning or executor code runs. The
// error MUST identify the offending resource by name so the user
// knows which entry to fix, and mention "absolute" so they know
// what fixed it.
func TestLinkErrorsOnAbsolutePath(t *testing.T) {
	repo := newTestRepo(t)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: rogue\n"+
			"    path: /etc/passwd\n",
	)

	err := Link(repo, Options{
		StorageRoot: storageIn(t, repo.Root),
		Stdout:      &bytes.Buffer{},
	})
	if err == nil {
		t.Fatalf("Link on absolute path: got nil error, want failure")
	}

	msg := err.Error()
	// The name is quoted in validate()'s `resource %q` context prefix.
	if !strings.Contains(msg, `"rogue"`) {
		t.Errorf("error missing resource name %q; full error: %s", `"rogue"`, msg)
	}
	// validate() renders absolute-path violations as
	//   `path %q must be repository-relative, not absolute`
	if !strings.Contains(msg, "absolute") {
		t.Errorf("error missing %q; full error: %s", "absolute", msg)
	}
}

// TestLinkErrorsOnDotDotPath pins that a resource whose Path
// escapes the repo (a leading `../`) is rejected by validate() with
// the "escapes" wording. Together with the absolute-path test,
// this guards the two ways a config could aim wrk at paths outside
// its authority.
func TestLinkErrorsOnDotDotPath(t *testing.T) {
	repo := newTestRepo(t)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: escape\n"+
			"    path: ../outside\n",
	)

	err := Link(repo, Options{
		StorageRoot: storageIn(t, repo.Root),
		Stdout:      &bytes.Buffer{},
	})
	if err == nil {
		t.Fatalf("Link on ../ path: got nil error, want failure")
	}

	msg := err.Error()
	if !strings.Contains(msg, `"escape"`) {
		t.Errorf("error missing resource name %q; full error: %s", `"escape"`, msg)
	}
	// validate() renders `..` violations as
	//   `path %q escapes the repository root`
	if !strings.Contains(msg, "escapes") {
		t.Errorf("error missing %q; full error: %s", "escapes", msg)
	}
}

// TestLinkErrorsOnConfigWithDuplicateResourceNames pins that
// validate() refuses a config with two resources sharing a Name.
// Names are the identity wrk uses everywhere (registry keys,
// planner output, `wrk status` rows); duplicates would corrupt the
// registry and produce ambiguous status output. The error MUST
// name both offenders — well, it MUST at least name the duplicated
// value — so the user can find them.
func TestLinkErrorsOnConfigWithDuplicateResourceNames(t *testing.T) {
	repo := newTestRepo(t)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: env\n"+
			"    path: .env\n"+
			"  - name: env\n"+
			"    path: .env.other\n",
	)
	// Make both paths present so a regression that reached the planner
	// would materialize both and thus not error — forcing this test to
	// depend on validate() firing first.
	writeFile(t, filepath.Join(repo.Root, ".env"), "a\n")
	writeFile(t, filepath.Join(repo.Root, ".env.other"), "b\n")

	err := Link(repo, Options{
		StorageRoot: storageIn(t, repo.Root),
		Stdout:      &bytes.Buffer{},
	})
	if err == nil {
		t.Fatalf("Link on duplicate names: got nil error, want failure")
	}

	msg := err.Error()
	if !strings.Contains(msg, "duplicate") {
		t.Errorf("error missing %q; full error: %s", "duplicate", msg)
	}
	if !strings.Contains(msg, `"env"`) {
		t.Errorf("error missing duplicated name %q; full error: %s", `"env"`, msg)
	}

	// Also guarantee no side effects: a duplicate-name reject must
	// happen before planning, so neither .env nor .env.other should
	// have been converted to a symlink or moved into shared storage.
	for _, rel := range []string{".env", ".env.other"} {
		info, err := os.Lstat(filepath.Join(repo.Root, rel))
		if err != nil {
			t.Fatalf("lstat %s: %v", rel, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			t.Errorf("%s became a symlink despite duplicate-name reject; mode=%v",
				rel, info.Mode())
		}
	}
}

// TestConfigLoadErrorSurfacesAsTypedErrorForJSON pins the H4 wrap:
// engine functions that call config.Load MUST wrap the failure into
// a typed *Error carrying ErrConfigInvalid so `wrk <cmd> --json`
// emits `code: "config_invalid"` instead of the fallback
// `code: "unknown"`. errors.As is the agent-facing entry point;
// exercising it here proves the CLI's emitJSONError will route the
// error correctly.
//
// Also asserts the wrapped cause remains reachable so downstream
// callers keep their errors.Is checks against config-layer
// sentinels working.
func TestConfigLoadErrorSurfacesAsTypedErrorForJSON(t *testing.T) {
	repo := newTestRepo(t)
	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: env\n"+
			"    path: [\n",
	)

	// Any engine function that reads config would do — Status is a
	// pure read that exercises the same code path we care about.
	_, err := Status(repo, Options{
		StorageRoot: storageIn(t, repo.Root),
		Stdout:      &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("Status on malformed YAML: got nil error, want failure")
	}

	var wrkErr *Error
	if !errors.As(err, &wrkErr) {
		t.Fatalf("errors.As should recover *Error, got %T: %v", err, err)
	}
	if wrkErr.Code != ErrConfigInvalid {
		t.Errorf("code = %q, want %q", wrkErr.Code, ErrConfigInvalid)
	}
	// Human-facing message preservation: the wrapped cause is still
	// visible so existing string-grepping tests (and stderr) work.
	if !strings.Contains(err.Error(), "invalid configuration") {
		t.Errorf("Error() = %q, missing wrapped cause", err.Error())
	}
	// Unwrap chain reachable: caller-side errors.Is on the config
	// package's sentinels still finds them.
	if wrkErr.Wrapped == nil {
		t.Error("Wrapped is nil; Wrapf must preserve the underlying error")
	}
}
