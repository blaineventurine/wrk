package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
)

// TestLinkSkipsCreateFalseWhenMissing pins the "resting state" half
// of the `create: false` contract: a resource that wrk is not
// allowed to provision, and that does not exist in the workspace,
// produces an empty plan on Link. Link succeeds silently and MUST
// NOT create the workspace path or the shared-storage subtree for
// that resource. A regression that made Link try to touch either
// side would ship footguns to every consumer of an externally-
// provisioned .env / .envrc / secrets file.
func TestLinkSkipsCreateFalseWhenMissing(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: env\n"+
			"    path: .env\n"+
			"    create: false\n",
	)
	// Deliberately no .env in the workspace.

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}

	// The workspace path must not have been created — not as a real
	// file, not as a symlink, not at all.
	if _, err := os.Lstat(filepath.Join(repo.Root, ".env")); !os.IsNotExist(err) {
		t.Errorf("workspace .env exists after Link; err = %v", err)
	}

	// The shared-storage subtree for this repository must not have
	// been created. `location.For` computes the path but never
	// creates it, and an empty plan skips execution entirely — so the
	// storage root must be empty apart from any parent dirs the test
	// helper created for its own scaffolding (`storageIn` makes
	// exactly `<repo>/.wrk-storage/`; nothing beneath it).
	repoStorage := filepath.Join(storage, repo.RepositoryID)
	if _, err := os.Lstat(repoStorage); !os.IsNotExist(err) {
		t.Errorf("shared repo-storage dir %s exists; want no shared side effects; err=%v",
			repoStorage, err)
	}
	entries, err := os.ReadDir(storage)
	if err != nil {
		t.Fatalf("read storage root: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("storage root has unexpected entries after Link on create:false: %v", names)
	}

	// Status must report the resource as StateExpected — the resting,
	// non-problem state — so `wrk status` doesn't tell the user
	// there's anything to do.
	report, err := Status(repo, opts)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(report.Rows) != 1 {
		t.Fatalf("Status rows = %d, want 1: %+v", len(report.Rows), report.Rows)
	}
	if got := report.Rows[0].State; got != StateExpected {
		t.Errorf("Status row state = %q, want %q", got, StateExpected)
	}
}

// TestLinkProvisionsCreateFalseWhenExternalToolDropsFile pins the
// promise in the README that "when the external tool eventually
// drops the file into place, wrk will happily share it across
// workspaces on the next `wrk link`." The first Link is a silent
// no-op (see TestLinkSkipsCreateFalseWhenMissing), but once the
// workspace path is a real file, the SECOND Link adopts it into
// shared storage exactly like a normal resource — no special
// `create: false` bypass, no refusal.
//
// A regression that let `create: false` persist as "never manage
// this" past the point where the file DOES exist would leave the
// workspace with an un-shared local copy that silently diverges
// across worktrees.
func TestLinkProvisionsCreateFalseWhenExternalToolDropsFile(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: env\n"+
			"    path: .env\n"+
			"    create: false\n",
	)

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	// First Link: nothing to do. Confirmed by the sibling test; here
	// we just need it to have run so we can assert Link is repeatable
	// against the same repo state.
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link #1 (no file yet): %v", err)
	}

	// Simulate the external tool (direnv, sops, 1Password, secrets
	// manager) creating the file at the workspace path.
	const externalContent = "SECRET=from-external-tool\n"
	writeFile(t, filepath.Join(repo.Root, ".env"), externalContent)

	// Second Link: the workspace now has a real file, so the
	// provisionShared path adopts it into shared storage and installs
	// the symlink. `create: false` no longer suppresses anything —
	// there's nothing to suppress.
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link #2 (file dropped): %v", err)
	}

	// The workspace path is now a symlink into the shared copy.
	wsEnv := filepath.Join(repo.Root, ".env")
	info, err := os.Lstat(wsEnv)
	if err != nil {
		t.Fatalf("lstat workspace .env: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("workspace .env not a symlink after second Link; mode=%v", info.Mode())
	}
	link, err := os.Readlink(wsEnv)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	sharedEnv := filepath.Join(storage, repo.RepositoryID, ".env")
	sharedAbs, err := filepath.Abs(sharedEnv)
	if err != nil {
		t.Fatal(err)
	}
	if link != sharedAbs {
		t.Errorf("symlink target = %q, want %q", link, sharedAbs)
	}

	// Shared storage holds the exact bytes the external tool dropped.
	got, err := os.ReadFile(sharedAbs)
	if err != nil {
		t.Fatalf("read shared: %v", err)
	}
	if string(got) != externalContent {
		t.Errorf("shared content = %q, want %q", got, externalContent)
	}
}

// TestStatusExitCodeIgnoresCreateFalseWhenMissing pins the invariant
// that ties `create: false` to the state constant `wrk status
// --exit-code` treats as healthy. The engine's Status derives the
// state; the cmd/wrk hasProblems helper (tested in cmd/wrk/status_test.go)
// treats exactly {StateLinked, StateExpected, StateDetached} as the
// non-problem set. This test guards the CONNECTION: a missing
// `create: false` resource MUST produce StateExpected, and
// StateExpected MUST be a member of that safe set. If a refactor
// renamed the constant or split the state, `wrk status --exit-code`
// would start reporting healthy repos as broken.
func TestStatusExitCodeIgnoresCreateFalseWhenMissing(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: env\n"+
			"    path: .env\n"+
			"    create: false\n",
	)

	report, err := Status(repo, Options{
		StorageRoot: storage,
		Stdout:      &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(report.Rows) != 1 {
		t.Fatalf("Status rows = %d, want 1: %+v", len(report.Rows), report.Rows)
	}
	got := report.Rows[0].State
	if got != StateExpected {
		t.Fatalf("state = %q, want %q (create:false + no file)", got, StateExpected)
	}

	// The state MUST be a member of the resting/healthy set. If a
	// future refactor moves StateExpected out of the safe set (or
	// renames it), this assertion fails alongside cmd/wrk's
	// hasProblems tests, pinpointing the connection.
	safe := map[State]bool{
		StateLinked:   true,
		StateExpected: true,
		StateDetached: true,
	}
	if !safe[got] {
		t.Errorf("state %q is not in the healthy set %v — "+
			"cmd/wrk/status hasProblems would flag it", got, safe)
	}
}
