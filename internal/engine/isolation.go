package engine

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/blaineventurine/wrk/internal/repository"
)

// isolationRegistry maps a workspace root to per-resource isolation records.
//
// Distinct from detachRegistry: detach means "real independent copy in the
// workspace"; isolate means "workspace symlink points at a private variant
// path that no fingerprint maps to." A workspace can hold both kinds of
// entries concurrently for different resources.
type isolationRegistry map[string]map[string]isolationEntry

// isolationEntry describes a single isolated resource in a workspace.
type isolationEntry struct {
	// StoragePath is the absolute path of the isolated variant. `wrk gc`
	// uses this to keep the variant pinned across sweeps; `wrk link` uses
	// it to skip the resource entirely for this workspace.
	StoragePath string `json:"storagePath"`
	// CreatedAt is a diagnostic RFC3339 timestamp. Not load-bearing.
	CreatedAt string `json:"createdAt"`
}

// isolationPath is the on-disk location of the isolation registry.
// It lives alongside detached.json under `<metadata>/wrk/`, so two
// workspaces of the same repo — which share that directory via
// `git --git-common-dir` or `jj`'s `.jj/repo` — see the same file.
func isolationPath(repo *repository.Repository) string {
	return filepath.Join(repo.MetadataDir(), "wrk", "isolated.json")
}

// loadIsolation returns the isolation registry for repo. A missing file
// yields an empty registry with no error; a corrupt file is logged to
// stderr and also treated as empty (matches detachRegistry's tolerance:
// a silent reset would hide a real problem, but aborting startup would
// strand users).
func loadIsolation(repo *repository.Repository) (isolationRegistry, error) {
	path := isolationPath(repo)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return isolationRegistry{}, nil
	}
	if err != nil {
		return nil, err
	}
	reg := isolationRegistry{}
	if err := json.Unmarshal(data, &reg); err != nil {
		fmt.Fprintf(os.Stderr,
			"wrk: isolation registry at %s is corrupt (%v), treating as empty\n",
			path, err)
		return isolationRegistry{}, nil
	}
	// json.Unmarshal on a literal `null` payload decodes without
	// error but leaves the target map nil. Downstream callers
	// (recordIsolation, isIsolated) index into the returned map, so
	// a nil registry would NPE on the very next access. Coerce to
	// an empty non-nil registry so the "always usable" contract
	// holds.
	if reg == nil {
		reg = isolationRegistry{}
	}
	return reg, nil
}

// saveIsolation writes reg to disk atomically. Callers MUST hold the
// registry flock (see withRegistryLock) so a concurrent sibling process
// cannot interleave load-modify-save with this write.
func saveIsolation(repo *repository.Repository, reg isolationRegistry) error {
	path := isolationPath(repo)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}

	// Atomic write via sibling tmp file — same discipline as saveRegistry.
	// A crash mid-write leaves the real path either untouched (old, valid)
	// or fully replaced (new, valid); loadIsolation never sees a half
	// truncated file.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// recordIsolation marks workspaceRoot's resourcePath as isolated at
// storagePath. Overwrites any prior entry for the same (workspace,
// resource) pair — the caller (RelinkIsolate) is authoritative about
// which variant is pinned.
//
// Serialized via the same withRegistryLock as the detach registry so a
// concurrent detach on a sibling workspace cannot race an isolate: both
// paths flock `<detached.json>.wrk-lock` before touching disk.
func recordIsolation(repo *repository.Repository, workspaceRoot, resourcePath, storagePath string) error {
	return withRegistryLock(repo, func() error {
		reg, err := loadIsolation(repo)
		if err != nil {
			return err
		}
		if reg[workspaceRoot] == nil {
			reg[workspaceRoot] = map[string]isolationEntry{}
		}
		reg[workspaceRoot][resourcePath] = isolationEntry{
			StoragePath: storagePath,
			CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		}
		return saveIsolation(repo, reg)
	})
}

// clearIsolation removes the isolation entry for one (workspace, resource)
// pair. Missing entries are no-ops so the caller (relink undo, gc) can
// call it unconditionally. When the workspace's last entry is dropped,
// its map key is removed so the registry does not grow empty stubs.
func clearIsolation(repo *repository.Repository, workspaceRoot, resourcePath string) error {
	return withRegistryLock(repo, func() error {
		reg, err := loadIsolation(repo)
		if err != nil {
			return err
		}
		entries, ok := reg[workspaceRoot]
		if !ok {
			return nil
		}
		if _, ok := entries[resourcePath]; !ok {
			return nil
		}
		delete(entries, resourcePath)
		if len(entries) == 0 {
			delete(reg, workspaceRoot)
		}
		return saveIsolation(repo, reg)
	})
}

// isIsolated reports whether resourcePath is isolated in workspaceRoot.
// Read-only accessor over an already-loaded registry so callers that
// need to check many resources don't reload the file each time.
func isIsolated(reg isolationRegistry, workspaceRoot, resourcePath string) (isolationEntry, bool) {
	entries, ok := reg[workspaceRoot]
	if !ok {
		return isolationEntry{}, false
	}
	entry, ok := entries[resourcePath]
	return entry, ok
}

// randomHex returns a lowercase hex string of length n. n MUST be even
// because each byte encodes two hex characters; an odd length is a
// programmer bug and returns an error.
//
// Consumed by RelinkIsolate to generate the `isolated-<hex>` suffix of a
// fresh per-workspace variant directory: each workspace's isolate lands
// in a distinct storage path, and repeated isolates against the same
// resource never collide.
func randomHex(n int) (string, error) {
	if n%2 != 0 {
		return "", fmt.Errorf("randomHex: odd length %d", n)
	}
	b := make([]byte, n/2)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
