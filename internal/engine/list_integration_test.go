package engine

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
)

// TestListReportsAllResources pins the reporting contract: every
// configured resource shows up in the listing with the shared-storage
// path wrk would use — regardless of whether it has a fingerprint.
// The fingerprinted resource's path lands under its fingerprint
// subdir; the plain resource's path lands directly under the repo
// storage dir.
func TestListReportsAllResources(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	// One plain resource; one fingerprinted. The fingerprinted one's
	// input file must exist so location.For can compute the digest.
	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: env\n"+
			"    path: .env\n"+
			"  - name: cache\n"+
			"    path: .cache\n"+
			"    fingerprint:\n"+
			"      - lock.txt\n",
	)
	writeFile(t, filepath.Join(repo.Root, "lock.txt"), "v1\n")

	listings, err := List(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}, false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got, want := len(listings), 2; got != want {
		t.Fatalf("listings = %d, want %d", got, want)
	}

	byName := map[string]ResourceListing{}
	for _, l := range listings {
		byName[l.Resource] = l
	}

	envListing, ok := byName["env"]
	if !ok {
		t.Fatalf("missing env listing; got %+v", listings)
	}
	wantEnvShared, _ := filepath.Abs(filepath.Join(storage, repo.RepositoryID, ".env"))
	if envListing.SharedPath != wantEnvShared {
		t.Errorf("env SharedPath = %q, want %q", envListing.SharedPath, wantEnvShared)
	}
	if envListing.Fingerprinted {
		t.Errorf("env should not be reported fingerprinted")
	}
	if envListing.Size != -1 {
		t.Errorf("env Size = %d, want -1 (withSize=false)", envListing.Size)
	}

	cacheListing, ok := byName["cache"]
	if !ok {
		t.Fatalf("missing cache listing; got %+v", listings)
	}
	if !cacheListing.Fingerprinted {
		t.Errorf("cache should be reported fingerprinted")
	}
	// Fingerprinted shared path sits under a <base>/<fp> dir; asserting
	// the base prefix keeps the test decoupled from the exact digest.
	wantCacheBase, _ := filepath.Abs(filepath.Join(storage, repo.RepositoryID, ".cache"))
	if got := filepath.Dir(cacheListing.SharedPath); got != wantCacheBase {
		t.Errorf("cache SharedPath parent = %q, want %q (fingerprint subdir missing?)",
			got, wantCacheBase)
	}
}

// TestListWithSizeIncludesUsage pins the withSize side of List: after
// a Link that materializes a resource in shared storage, List(..., true)
// reports a non-zero Size that reflects the written bytes.
func TestListWithSizeIncludesUsage(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, "resources:\n  - name: env\n    path: .env\n")
	// Non-empty content so treeSize returns > 0.
	writeFile(t, filepath.Join(repo.Root, ".env"), "some content that has bytes\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}

	listings, err := List(repo, opts, true)
	if err != nil {
		t.Fatalf("List(withSize): %v", err)
	}
	if len(listings) != 1 {
		t.Fatalf("listings = %d, want 1", len(listings))
	}
	if listings[0].Size <= 0 {
		t.Errorf("Size = %d, want > 0 (shared copy exists after Link)", listings[0].Size)
	}
	if listings[0].Variants != 1 {
		t.Errorf("Variants = %d, want 1 (single non-fingerprinted shared copy)",
			listings[0].Variants)
	}
}
