package engine

import (
	"bytes"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
)

// TestStatusReportsLinked pins the "everything's connected" outcome:
// after a successful Link, every resource shows up as `linked`. The
// StatusReport also reports the source config files it consulted.
func TestStatusReportsLinked(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: env\n"+
			"    path: .env\n"+
			"  - name: cfg\n"+
			"    path: cfg.toml\n",
	)
	writeFile(t, filepath.Join(repo.Root, ".env"), "e\n")
	writeFile(t, filepath.Join(repo.Root, "cfg.toml"), "c\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}

	report, err := Status(repo, opts)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got, want := len(report.Rows), 2; got != want {
		t.Fatalf("rows = %d, want %d: %+v", got, want, report.Rows)
	}
	for _, row := range report.Rows {
		if row.State != StateLinked {
			t.Errorf("row %q state = %q, want %q", row.Resource, row.State, StateLinked)
		}
	}
	// Sources: only .wrk.yml was loaded.
	if len(report.Sources) != 1 || report.Sources[0] != config.Filename {
		t.Errorf("sources = %v, want [%q]", report.Sources, config.Filename)
	}
}

// TestStatusReportsPending pins the "hook queued but not yet run" state:
// a resource with an initialize hook, but no workspace copy and no
// shared copy, must be reported as `pending` — the user hasn't run Link
// yet, but wrk knows what to do when they do.
func TestStatusReportsPending(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: env\n"+
			"    path: .env\n"+
			"    hooks:\n"+
			"      initialize:\n"+
			"        - run: true\n",
	)

	report, err := Status(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(report.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(report.Rows))
	}
	if got := report.Rows[0].State; got != StatePending {
		t.Errorf("state = %q, want %q", got, StatePending)
	}
}

// TestStatusReportsDetached pins the discrimination between an
// intentional detach and an accidental conflict: even though the raw
// filesystem state (workspace real file + shared file) looks like a
// conflict, the registry lookup upgrades it to `detached`. This is
// why the registry exists at all.
func TestStatusReportsDetached(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, "resources:\n  - name: env\n    path: .env\n")
	writeFile(t, filepath.Join(repo.Root, ".env"), "seed\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if err := Detach(repo, opts); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	report, err := Status(repo, opts)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(report.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(report.Rows))
	}
	if got := report.Rows[0].State; got != StateDetached {
		t.Errorf("state = %q, want %q (not %q — registry lookup should promote)",
			got, StateDetached, StateConflict)
	}
}

// TestStatusReportsConflict pins the fail-safe: workspace has a real
// copy, shared exists, and there's NO registry record — so wrk cannot
// assume the user meant to detach and reports `conflict`. This is the
// case that blocks a plain Link.
func TestStatusReportsConflict(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, "resources:\n  - name: env\n    path: .env\n")
	writeFile(t, filepath.Join(repo.Root, ".env"), "workspace-side\n")

	// Pre-create shared copy directly — no Detach happened, so no
	// registry entry. The dual existence is what Status must flag.
	sharedEnv := filepath.Join(storage, repo.RepositoryID, ".env")
	writeFile(t, sharedEnv, "shared-side\n")

	report, err := Status(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(report.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(report.Rows))
	}
	if got := report.Rows[0].State; got != StateConflict {
		t.Errorf("state = %q, want %q", got, StateConflict)
	}
}

// TestStatusReportsExpectedForCreateFalse pins the `create: false`
// contract: a resource that wrk isn't allowed to provision, and that
// isn't present anywhere, is reported as `expected` (i.e. the user is
// expected to provide it out-of-band). It is neither an error nor
// something wrk should try to link.
func TestStatusReportsExpectedForCreateFalse(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: env\n"+
			"    path: .env\n"+
			"    create: false\n",
	)
	// .env absent in workspace, absent in shared, no hook.

	report, err := Status(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(report.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(report.Rows))
	}
	if got := report.Rows[0].State; got != StateExpected {
		t.Errorf("state = %q, want %q", got, StateExpected)
	}
}

// TestStatusSurfacesConfigWarnings pins S15's warning plumbing: when
// .wrk.local.yml redirects a shared resource's path, Status prints the
// non-fatal advisory to stdout with the `!` prefix. A regression that
// dropped the printWarnings call would silently swallow the redirect.
func TestStatusSurfacesConfigWarnings(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n  - name: env\n    path: .env\n",
	)
	// Override redirects .env → env.dev — the merge should warn.
	writeConfig(t, repo.Root, config.LocalFilename,
		"resources:\n  - name: env\n    path: env.dev\n",
	)
	// Provide something so the resolver has a path to inspect.
	writeFile(t, filepath.Join(repo.Root, "env.dev"), "")

	var out bytes.Buffer
	if _, err := Status(repo, Options{StorageRoot: storage, Stdout: &out}); err != nil {
		t.Fatalf("Status: %v", err)
	}

	printed := out.String()
	if !strings.Contains(printed, "!") {
		t.Errorf("warning prefix `!` missing:\n%s", printed)
	}
	// Every warning names the resource, the source path, and the override —
	// keep those pinned so a regression that mangles the message is caught.
	for _, want := range []string{`"env"`, `".env"`, `"env.dev"`} {
		if !strings.Contains(printed, want) {
			t.Errorf("warning output missing %q:\n%s", want, printed)
		}
	}
}

// TestStatusAllAggregatesAcrossWorkspaces pins the multi-workspace
// aggregation: given two live worktrees each of which linked the same
// resource, StatusAll returns rows for both, and the Sources slice is
// deduped (both worktrees load the same .wrk.yml).
func TestStatusAllAggregatesAcrossWorkspaces(t *testing.T) {
	primary := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n",
	})
	storage := storageIn(t, primary.Root)

	// Workspace copy of .env for the primary — the second worktree
	// picks up the shared copy via a symlink because .env was not
	// tracked.
	writeFile(t, filepath.Join(primary.Root, ".env"), "primary\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(primary, opts); err != nil {
		t.Fatalf("Link primary: %v", err)
	}

	// Second worktree inherits the committed .wrk.yml but has no
	// .env — the Link on this side creates a symlink pointing at the
	// primary-provisioned shared copy.
	_, secondary := addGitWorktree(t, primary, "feature")

	if err := Link(secondary, opts); err != nil {
		t.Fatalf("Link secondary: %v", err)
	}

	report, err := StatusAll(primary, opts)
	if err != nil {
		t.Fatalf("StatusAll: %v", err)
	}

	// Two rows, one per workspace, both linked.
	rootsSeen := map[string]int{}
	for _, r := range report.Rows {
		rootsSeen[r.WorkspaceRoot]++
		if r.State != StateLinked {
			t.Errorf("row %q in %s: state = %q, want %q",
				r.Resource, r.WorkspaceRoot, r.State, StateLinked)
		}
	}
	if got := len(rootsSeen); got != 2 {
		t.Fatalf("rows spanned %d workspaces, want 2 (rows=%+v)", got, report.Rows)
	}
	if rootsSeen[primary.Root] != 1 {
		t.Errorf("primary workspace missing from rows; seen=%v", rootsSeen)
	}
	if rootsSeen[secondary.Root] != 1 {
		t.Errorf("secondary workspace missing from rows; seen=%v", rootsSeen)
	}

	// Sources dedup: both workspaces load the same .wrk.yml, so the
	// aggregate Sources must be [".wrk.yml"] exactly once.
	sort.Strings(report.Sources)
	if len(report.Sources) != 1 || report.Sources[0] != config.Filename {
		t.Errorf("sources = %v, want [%q] (dedup broken)", report.Sources, config.Filename)
	}
}
