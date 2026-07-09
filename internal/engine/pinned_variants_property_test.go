package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/repository"
	"pgregory.net/rapid"
)

// TestPinnedVariantsProperty guards the canonicalization invariant of
// pinnedVariants: a workspace whose configured resource is a symlink
// resolving under a variant's shared storage MUST be counted as
// pinning that variant, regardless of how the workspace symlink target
// and the scanned variant's StoragePath spell their common ancestor.
//
// The concrete regression the test defends against surfaced on macOS,
// where t.TempDir() returns "/var/folders/..." — itself a symlink into
// "/private/var/folders/...". If pinnedVariants forgets to run
// filepath.EvalSymlinks on the variant base, the two spellings of the
// same real path compare unequal via filepath.Rel and `wrk gc` would
// delete an in-use variant. See internal/engine/gc.go
// pinnedVariantsForRoots.
//
// Rapid drives structural knobs — the workspace-symlink target form,
// the storage-path spelling handed to pinnedVariants, and the resource
// sub-path shape — and each iteration builds a real filesystem
// fixture, calls pinnedVariants, and asserts three invariants:
// (1) the live workspace pins the freshly-linked variant,
// (2) pinnedVariants is idempotent, and
// (3) pinnedVariants does not mutate the on-disk variant set.
//
// The nonCanonStorage knob is the one that actually exercises the
// original bug: when Options.StorageRoot is the non-canonical
// /var/folders/... spelling, scanVariants records variants under
// /var/... while EvalSymlinks on the workspace symlink canonicalizes
// to /private/var/... . Without EvalSymlinks-on-base the pin check
// would compare unequal strings for the same real path and gc would
// delete an in-use variant. On non-macOS hosts the /var vs /private/var
// rewrite is skipped (the alternate spelling doesn't resolve) so those
// hosts exercise only the trivial spelling. The property is asserted
// unconditionally.
//
// Note on t plumbing: rapid.TB is a strict subset of testing.TB (no
// TempDir/Setenv), so the outer *testing.T is captured here and used
// for helpers that need those methods. Property-check failures go
// through *rapid.T so rapid can log the drawn seed and shrink.
func TestPinnedVariantsProperty(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Draw structural knobs. Keep the space small — each iteration
		// builds a real repo + storage tree, so a handful of
		// variations is plenty. `resourcePath` is a stand-in for
		// real-world config churn; the resource is un-fingerprinted so
		// this only affects the RelativePath component of StoragePath,
		// not variant subdir names.
		usePrivatePrefix := rapid.Bool().Draw(rt, "usePrivatePrefix")
		nonCanonStorage := rapid.Bool().Draw(rt, "nonCanonStorage")
		resourcePath := rapid.SampledFrom([]string{".env", ".envrc", "config.local"}).Draw(rt, "resourcePath")

		repo := newTestRepoWithHead(t, map[string]string{
			".wrk.yml": "resources:\n" +
				"  - name: env\n" +
				"    path: " + resourcePath + "\n",
		})
		storage := storageIn(t, repo.Root)
		writeFile(t, filepath.Join(repo.Root, resourcePath), "seed\n")

		if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
			rt.Fatalf("Link: %v", err)
		}

		// Optionally re-express the workspace symlink target via the
		// alternate canonical form. On macOS this exercises the /var
		// vs /private/var mismatch that was the actual bug: the
		// fixture canonicalizes repo.Root through /private/var, so
		// Link writes a symlink whose bytes start with "/private/var/…";
		// rewriting them to "/var/…" forces filepath.EvalSymlinks to
		// walk the /var → private/var chain and canonicalize back to
		// "/private/var". Skipped on Linux and any host where the
		// alternate spelling doesn't resolve — the invariants below
		// still fire against the untouched layout.
		if usePrivatePrefix {
			linkPath := filepath.Join(repo.Root, resourcePath)
			oldTarget, err := os.Readlink(linkPath)
			if err != nil {
				rt.Fatalf("Readlink pre-rewrite: %v", err)
			}
			newTarget, ok := altPrivateForm(oldTarget)
			if ok {
				if _, err := os.Stat(newTarget); err == nil {
					if err := os.Remove(linkPath); err != nil {
						rt.Fatalf("remove symlink: %v", err)
					}
					if err := os.Symlink(newTarget, linkPath); err != nil {
						rt.Fatalf("resymlink: %v", err)
					}
				}
				// If the alternate target doesn't stat (Linux, or a
				// pathological chroot), fall through and assert on
				// the layout Link produced.
			}
		}

		// Compute the storage spelling the pin walk will see. When
		// nonCanonStorage is drawn true and the alternate spelling
		// stats on this host, we hand pinnedVariants the /var/… form
		// so scanVariants records variants under a non-canonical
		// StoragePath — the exact input shape the EvalSymlinks-on-base
		// fix in gc.go was added to handle.
		pinStorage := storage
		if nonCanonStorage {
			if alt, ok := altPrivateForm(storage); ok {
				if _, err := os.Stat(alt); err == nil {
					pinStorage = alt
				}
			}
		}

		assertPinInvariants(rt, repo, pinStorage, usePrivatePrefix, nonCanonStorage, resourcePath)
	})
}

// altPrivateForm returns the alternate spelling of oldTarget under the
// macOS /var ↔ /private/var symlink chain, or ("", false) if the path
// doesn't sit inside either root. Split out so the test's control flow
// stays readable.
func altPrivateForm(oldTarget string) (string, bool) {
	switch {
	case strings.HasPrefix(oldTarget, "/private/var/"):
		return strings.TrimPrefix(oldTarget, "/private"), true
	case strings.HasPrefix(oldTarget, "/var/"):
		return "/private" + oldTarget, true
	default:
		return "", false
	}
}

// assertPinInvariants runs pinnedVariants and checks the three
// properties the test defends: (1) the freshly-Linked variant is
// pinned, (2) pinnedVariants is idempotent, (3) pinnedVariants does
// not mutate the on-disk variant set. Every failure fatals through
// *rapid.T so rapid reports the drawn knobs on the offending iteration.
func assertPinInvariants(
	rt *rapid.T,
	repo *repository.Repository,
	storage string,
	usePrivatePrefix bool,
	nonCanonStorage bool,
	resourcePath string,
) {
	rt.Helper()

	before, err := scanVariants(repo, Options{StorageRoot: storage})
	if err != nil {
		rt.Fatalf("scanVariants (before): %v", err)
	}
	if len(before) == 0 {
		rt.Fatalf("scanVariants returned no variants; fixture never provisioned storage (nonCanonStorage=%v, resource=%q)", nonCanonStorage, resourcePath)
	}

	pinned, unreachable, err := pinnedVariants(repo, Options{StorageRoot: storage})
	if err != nil {
		rt.Fatalf("pinnedVariants: %v", err)
	}
	if len(unreachable) != 0 {
		rt.Fatalf("unreachable = %v, want empty (usePrivatePrefix=%v, nonCanonStorage=%v)", unreachable, usePrivatePrefix, nonCanonStorage)
	}

	// (1) The workspace symlink must pin at least one variant — anything
	// less means gc would delete the in-use shared copy.
	if len(pinned) == 0 {
		rt.Fatalf("pinned = empty; workspace symlink not recognized under variant (usePrivatePrefix=%v, nonCanonStorage=%v, resource=%q)", usePrivatePrefix, nonCanonStorage, resourcePath)
	}

	// (2) Idempotence: repeated calls return the same set. A stale
	// EvalSymlinks or cached lstat would show up here as flakiness.
	pinnedAgain, _, err := pinnedVariants(repo, Options{StorageRoot: storage})
	if err != nil {
		rt.Fatalf("pinnedVariants (second call): %v", err)
	}
	if !reflect.DeepEqual(pinned, pinnedAgain) {
		rt.Fatalf("pinnedVariants not idempotent:\n first: %v\nsecond: %v", pinned, pinnedAgain)
	}

	// (3) Non-mutation: pinnedVariants is documented read-only; compare
	// the storage-path set produced by scanVariants before and after
	// the pin walk to catch any accidental writes.
	after, err := scanVariants(repo, Options{StorageRoot: storage})
	if err != nil {
		rt.Fatalf("scanVariants (after): %v", err)
	}
	if !sameStoragePaths(before, after) {
		rt.Fatalf("pinnedVariants mutated storage:\n before: %v\n  after: %v", storagePathList(before), storagePathList(after))
	}
}

func storagePathList(vs []variant) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.StoragePath)
	}
	sort.Strings(out)
	return out
}

func sameStoragePaths(a, b []variant) bool {
	return reflect.DeepEqual(storagePathList(a), storagePathList(b))
}
