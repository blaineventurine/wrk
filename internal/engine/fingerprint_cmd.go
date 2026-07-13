package engine

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/fingerprint"
	"github.com/blaineventurine/wrk/internal/location"
	"github.com/blaineventurine/wrk/internal/repository"
	"github.com/blaineventurine/wrk/internal/resolver"
)

// FingerprintReport is the analysis result for one configured resource.
//
// Current is derived from the resource's fingerprint inputs on disk right
// now. Pinned is whatever the workspace symlink currently points at.
// Comparing the two answers the question: "would the next `wrk link`
// swap this workspace to a different shared variant?"
type FingerprintReport struct {
	// Resource is a value copy of the config entry so callers can
	// reference the resource's Name, Path, and raw Fingerprint inputs
	// without loading the config again.
	Resource config.Resource
	// Current is the freshly-computed snapshot from inputs on disk.
	Current FingerprintSnapshot
	// Pinned is what the workspace symlink currently targets. When the
	// workspace path is not a symlink (missing, real file, real
	// directory, detached), both of Pinned's fields are empty.
	Pinned FingerprintSnapshot
	// Changed reports whether Current.Fingerprint differs from
	// Pinned.Fingerprint. An empty Pinned always differs from a
	// populated Current, so a detached or never-linked workspace
	// reports Changed=true.
	Changed bool
}

// FingerprintSnapshot captures a single fingerprint identity: the hex
// digest, the storage path it maps to, and (for Current only) the
// per-input details that produced it.
type FingerprintSnapshot struct {
	// Fingerprint is the 16-char hex digest. Empty when not computable
	// (Pinned side of a non-symlinked workspace).
	Fingerprint string
	// StoragePath is the absolute path in shared storage that this
	// fingerprint identifies. Empty when the fingerprint itself is
	// empty.
	StoragePath string
	// Inputs holds the per-input details that fed the fingerprint.
	// Populated only for Current; Pinned.Inputs is always nil because
	// wrk does not (yet) record per-input state alongside a variant.
	Inputs []FingerprintInput
}

// FingerprintInput is one file that participates in a resource's
// fingerprint.
type FingerprintInput struct {
	// Path is the repository-relative form (after {root} expansion).
	// When the absolute input somehow lies outside repo.Root, Path
	// falls back to the absolute path so the caller still sees which
	// file was consulted.
	Path string
	// Exists is true iff os.Stat succeeded on the input at the moment
	// FingerprintOne ran. A missing input still participates in the
	// fingerprint (via a domain-separator tag inside fingerprint.go).
	Exists bool
	// SizeBytes is the file size when Exists is true, and zero
	// otherwise. This is diagnostic sugar for CLI output, not part of
	// the fingerprint identity.
	SizeBytes int64
}

// FingerprintOne analyzes a single configured resource: it computes
// the current fingerprint from inputs on disk, reads the fingerprint
// currently pinned by the workspace symlink, and reports whether they
// differ.
//
// It errors when the resource name is not configured, when the
// resource has no fingerprint block (nothing to compare), or when
// config.Load / resolver.Resolve fail. A resource that resolves to
// no instances is treated as a configuration error too — callers ask
// for a specific name and expect an answer.
//
// For a glob resource that resolves to more than one instance,
// FingerprintOne reports on the first instance; the CLI layer is
// responsible for surfacing that limitation.
func FingerprintOne(
	repo *repository.Repository,
	resourceName string,
	options Options,
) (*FingerprintReport, error) {
	if repo == nil {
		return nil, fmt.Errorf("FingerprintOne: nil repo")
	}

	cfg, err := config.Load(repo.Root)
	if err != nil {
		return nil, Wrapf(ErrConfigInvalid,
			"check .wrk.yml for syntax errors or invalid resource paths",
			err, "%s", err.Error())
	}

	var target *config.Resource
	for i := range cfg.Resources {
		if cfg.Resources[i].Name == resourceName {
			target = &cfg.Resources[i]
			break
		}
	}
	if target == nil {
		return nil, Newf(ErrResourceNotConfigured,
			"run 'wrk list' to see configured resources",
			"resource %q not configured", resourceName)
	}
	if len(target.Fingerprint) == 0 {
		return nil, Newf(ErrResourceNotFingerprinted,
			"add a fingerprint block to this resource in .wrk.yml",
			"resource %q is not fingerprinted", resourceName)
	}

	instances, err := resolver.Resolve(repo.Root, *target)
	if err != nil {
		return nil, err
	}
	if len(instances) == 0 {
		return nil, fmt.Errorf("resource %q resolved to no instances", resourceName)
	}
	instance := instances[0]

	// Freshly compute the fingerprint from inputs on disk.
	currentFP, err := fingerprint.Fingerprint(repo.Root, instance.FingerprintInputs...)
	if err != nil {
		return nil, err
	}

	// Per-input details: repo-relative path, existence, size.
	currentInputs := make([]FingerprintInput, 0, len(instance.FingerprintInputs))
	for _, abs := range instance.FingerprintInputs {
		rel, err := filepath.Rel(repo.Root, abs)
		if err != nil {
			// Rel only fails for the "different volumes" and
			// "cannot-express relatively" cases; fall back to the
			// absolute path so the caller still sees which file
			// participated.
			rel = abs
		} else {
			// filepath.Rel returns platform-native separators; the
			// exposed Path is meant for display alongside YAML and
			// CLI output, so keep forward slashes.
			rel = filepath.ToSlash(rel)
		}
		input := FingerprintInput{Path: rel}
		if info, err := os.Stat(abs); err == nil {
			input.Exists = true
			input.SizeBytes = info.Size()
		}
		currentInputs = append(currentInputs, input)
	}

	// Current storage path is what a fresh link would target.
	loc, err := location.For(options.StorageRoot, repo.RepositoryID, instance)
	if err != nil {
		return nil, err
	}

	// Pinned side comes from the workspace symlink.
	pinnedFP, pinnedPath := readPinnedVariant(instance.WorkspacePath)

	return &FingerprintReport{
		Resource: *target,
		Current: FingerprintSnapshot{
			Fingerprint: currentFP,
			StoragePath: loc.Path,
			Inputs:      currentInputs,
		},
		Pinned: FingerprintSnapshot{
			Fingerprint: pinnedFP,
			StoragePath: pinnedPath,
		},
		Changed: currentFP != pinnedFP,
	}, nil
}

// readPinnedVariant returns the fingerprint hex and absolute storage
// path currently pinned by the workspace symlink at wsPath.
//
// When wsPath is not a symlink (missing, real directory, real file,
// detached copy), both returned values are empty. When it is a symlink
// into shared storage, the fingerprint is filepath.Base of the
// cleaned absolute target. The caller judges whether that base value
// looks like a fingerprint: for a fingerprinted resource the last
// segment IS the fingerprint; for an un-fingerprinted resource the
// last segment is the resource name.
func readPinnedVariant(wsPath string) (string, string) {
	target, err := os.Readlink(wsPath)
	if err != nil {
		return "", ""
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(wsPath), target)
	}
	target = filepath.Clean(target)
	return filepath.Base(target), target
}
