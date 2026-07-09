package engine

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/location"
	"github.com/blaineventurine/wrk/internal/repository"
	"github.com/blaineventurine/wrk/internal/resolver"
)

// ============================================================
// Shared envelope
// ============================================================

// jsonSchema is the version tag stamped into every wrk `--json` payload.
// Bump this only when the on-the-wire shape changes in a way callers must
// notice (removed field, changed semantics). Additive changes keep the
// same schema.
const jsonSchema = 1

// jsonEnvelope is embedded into every command-specific JSON output struct.
// Keeping the envelope shared means all `--json` outputs share a common
// prefix (`schema`, `kind`) that callers can dispatch on.
type jsonEnvelope struct {
	Schema int    `json:"schema"`
	Kind   string `json:"kind"`
}

// ============================================================
// status
// ============================================================

// resourceStatusJSON is the JSON projection of a single ResourceStatus row.
type resourceStatusJSON struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	State       string `json:"state"`
	Origin      string `json:"origin"`
	Fingerprint string `json:"fingerprint,omitempty"`
	StoragePath string `json:"storagePath,omitempty"`
}

// workspaceStatusJSON groups all resources for one workspace under one root.
type workspaceStatusJSON struct {
	Root      string               `json:"root"`
	IsPrimary bool                 `json:"isPrimary"`
	State     string               `json:"state"`
	Resources []resourceStatusJSON `json:"resources"`
}

// statusJSON is the top-level shape emitted by `wrk status --json`.
type statusJSON struct {
	jsonEnvelope
	Sources    []string              `json:"sources"`
	Workspaces []workspaceStatusJSON `json:"workspaces"`
}

// MarshalStatusJSON renders a StatusReport as pretty-printed JSON. Rows
// are grouped by WorkspaceRoot preserving first-seen order; resources
// inside each workspace are sorted by Name so the output is deterministic
// across runs. primaryRoot flags exactly one workspace as the primary
// (the caller-supplied repo root).
//
// A nil report is treated as an empty report so callers get a stable
// envelope instead of a runtime panic.
//
// The returned bytes carry no trailing newline; callers add one if the
// stream needs it.
func MarshalStatusJSON(report *StatusReport, primaryRoot string) ([]byte, error) {
	if report == nil {
		report = &StatusReport{}
	}
	out := statusJSON{
		jsonEnvelope: jsonEnvelope{Schema: jsonSchema, Kind: "status"},
		// Defensive copy: never share backing storage with the caller's
		// report. Initialising to an empty slice keeps JSON `[]` rather
		// than `null` when there are no sources.
		Sources: append([]string{}, report.Sources...),
	}

	order := make([]string, 0)
	byRoot := make(map[string][]ResourceStatus)
	for _, row := range report.Rows {
		if _, seen := byRoot[row.WorkspaceRoot]; !seen {
			order = append(order, row.WorkspaceRoot)
		}
		byRoot[row.WorkspaceRoot] = append(byRoot[row.WorkspaceRoot], row)
	}

	out.Workspaces = make([]workspaceStatusJSON, 0, len(order))
	for _, root := range order {
		rows := byRoot[root]
		resources := make([]resourceStatusJSON, len(rows))
		states := make([]State, len(rows))
		for i, r := range rows {
			resources[i] = resourceStatusJSON{
				Name:        r.Resource,
				Path:        r.Path,
				State:       string(r.State),
				Origin:      string(r.Origin),
				Fingerprint: r.Fingerprint,
				StoragePath: r.SharedPath,
			}
			states[i] = r.State
		}
		sort.Slice(resources, func(i, j int) bool {
			return resources[i].Name < resources[j].Name
		})
		out.Workspaces = append(out.Workspaces, workspaceStatusJSON{
			Root:      root,
			IsPrimary: root == primaryRoot,
			State:     rollupState(states),
			Resources: resources,
		})
	}

	return json.MarshalIndent(out, "", "  ")
}

// rollupState collapses a workspace's per-resource states into a single
// coarse label used by dashboards and tooling. The rules, in priority
// order:
//
//   - no resources          → "empty"
//   - any conflict/stale/missing/not-linked/absent → "unhealthy"
//   - any pending           → "pending"
//   - every resource is linked or expected → "linked"
//   - every resource is detached          → "detached"
//   - otherwise             → "partial"
func rollupState(states []State) string {
	if len(states) == 0 {
		return "empty"
	}
	var hasUnhealthy, hasPending bool
	allLinkedOrExpected := true
	allDetached := true
	for _, s := range states {
		switch s {
		case StateConflict, StateStale, StateMissing, StateNotLinked, StateAbsent:
			hasUnhealthy = true
			allLinkedOrExpected = false
			allDetached = false
		case StatePending:
			hasPending = true
			allLinkedOrExpected = false
			allDetached = false
		case StateLinked, StateExpected:
			allDetached = false
		case StateDetached:
			allLinkedOrExpected = false
		default:
			// Unknown states err on the side of unhealthy so the
			// workspace never falsely rolls up as healthy.
			hasUnhealthy = true
			allLinkedOrExpected = false
			allDetached = false
		}
	}
	switch {
	case hasUnhealthy:
		return "unhealthy"
	case hasPending:
		return "pending"
	case allLinkedOrExpected:
		return "linked"
	case allDetached:
		return "detached"
	default:
		return "partial"
	}
}

// ============================================================
// list
// ============================================================

// variantJSON is the JSON projection of a single on-disk variant of a
// resource's shared storage. For a non-fingerprinted resource the sole
// variant carries an empty Fingerprint — consumers can distinguish `""`
// (no fingerprint configured) from a real digest cleanly.
type variantJSON struct {
	Fingerprint string   `json:"fingerprint"`
	StoragePath string   `json:"storagePath"`
	SizeBytes   int64    `json:"sizeBytes,omitempty"`
	InUseBy     []string `json:"inUseBy"`
}

// resourceListingJSON is the JSON projection of a single configured
// resource plus its enumerated on-disk variants.
type resourceListingJSON struct {
	Name              string        `json:"name"`
	Path              string        `json:"path"`
	Fingerprinted     bool          `json:"fingerprinted"`
	FingerprintInputs []string      `json:"fingerprintInputs,omitempty"`
	Origin            string        `json:"origin"`
	Variants          []variantJSON `json:"variants"`
}

// listJSON is the top-level shape emitted by `wrk list --json`.
type listJSON struct {
	jsonEnvelope
	Root      string                `json:"root"`
	Resources []resourceListingJSON `json:"resources"`
}

// MarshalListJSON renders the resources configured for repo as pretty-
// printed JSON. For each resource it walks the shared-storage subtree,
// enumerates on-disk variants (skipping bookkeeping siblings), and
// pins each variant to the sorted list of workspace roots whose
// resource-path symlink resolves into that variant. Per-variant size
// is included only when withSize is true.
//
// A nil repo is a programmer bug and yields an error rather than a
// runtime panic; a missing StorageRoot is tolerated as "nothing
// provisioned yet" and each resource lands with an empty variants
// slice. The RAW fingerprint config values (with `{root}` placeholders
// unresolved) are surfaced under fingerprintInputs so consumers can
// see exactly what the user configured.
//
// The returned bytes carry no trailing newline; callers add one if
// the stream needs it.
func MarshalListJSON(repo *repository.Repository, options Options, withSize bool) ([]byte, error) {
	if repo == nil {
		return nil, errors.New("MarshalListJSON: nil repo")
	}

	cfg, err := config.Load(repo.Root)
	if err != nil {
		return nil, err
	}

	workspaces, err := repo.Workspaces()
	if err != nil {
		return nil, err
	}

	out := listJSON{
		jsonEnvelope: jsonEnvelope{Schema: jsonSchema, Kind: "list"},
		Root:         repo.Root,
		Resources:    make([]resourceListingJSON, 0, len(cfg.Resources)),
	}

	for _, resource := range cfg.Resources {
		instances, err := resolver.Resolve(repo.Root, resource)
		if err != nil {
			return nil, err
		}

		for _, instance := range instances {
			loc, err := location.For(options.StorageRoot, repo.RepositoryID, instance)
			if err != nil {
				return nil, err
			}

			fingerprinted := len(instance.FingerprintInputs) > 0
			subtree := loc.Path
			if fingerprinted {
				subtree = filepath.Dir(loc.Path)
			}

			variants, err := enumerateVariants(subtree, fingerprinted, withSize)
			if err != nil {
				return nil, err
			}
			annotateInUseBy(variants, workspaces, instance.RelativePath, fingerprinted, subtree)

			// Defensive copy of the raw fingerprint config values —
			// never share backing storage with the caller's config
			// slice, and preserve empty vs nil for JSON omitempty.
			var fingerprintInputs []string
			if len(resource.Fingerprint) > 0 {
				fingerprintInputs = append([]string{}, resource.Fingerprint...)
			}

			out.Resources = append(out.Resources, resourceListingJSON{
				Name:              instance.Resource.Name,
				Path:              instance.RelativePath,
				Fingerprinted:     fingerprinted,
				FingerprintInputs: fingerprintInputs,
				Origin:            string(instance.Resource.Origin),
				Variants:          variants,
			})
		}
	}

	return json.MarshalIndent(out, "", "  ")
}

// enumerateVariants returns the on-disk variants for a resource. The
// returned slice is never nil so JSON emits `[]` rather than `null`
// when nothing is provisioned. For a non-fingerprinted resource the
// sole variant (if present) has an empty Fingerprint. Bookkeeping
// siblings (`.wrk-lock`, `.wrk-tmp`, `.wrk-deleting`, …) are filtered
// via isBookkeeping — they must never surface as user-visible
// variants.
func enumerateVariants(subtree string, fingerprinted, withSize bool) ([]variantJSON, error) {
	out := []variantJSON{}

	if !fingerprinted {
		if _, err := os.Lstat(subtree); err != nil {
			if os.IsNotExist(err) {
				return out, nil
			}
			return nil, err
		}
		v := variantJSON{StoragePath: subtree, InUseBy: []string{}}
		if withSize {
			size, err := treeSize(subtree)
			if err != nil {
				return nil, err
			}
			v.SizeBytes = size
		}
		return append(out, v), nil
	}

	entries, err := os.ReadDir(subtree)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	for _, e := range entries {
		name := e.Name()
		if isBookkeeping(name) || !e.IsDir() {
			continue
		}
		variantPath := filepath.Join(subtree, name)
		v := variantJSON{
			Fingerprint: name,
			StoragePath: variantPath,
			InUseBy:     []string{},
		}
		if withSize {
			size, err := treeSize(variantPath)
			if err != nil {
				return nil, err
			}
			v.SizeBytes = size
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Fingerprint < out[j].Fingerprint })
	return out, nil
}

// annotateInUseBy sets each variant's InUseBy to the sorted list of
// workspace roots whose resource-path symlink resolves under the
// variant's storage path. For a non-fingerprinted resource, any
// workspace symlink whose target matches subtree pins the sole
// variant.
//
// A workspace whose resource path isn't a symlink is silently skipped
// — it either hasn't been linked yet (state=missing/pending) or has
// been detached to a plain copy, and neither case should show up as
// "in use by" a shared variant.
func annotateInUseBy(variants []variantJSON, workspaces []string, resourceRelativePath string, fingerprinted bool, subtree string) {
	for i := range variants {
		var pins []string
		for _, ws := range workspaces {
			wsResource := filepath.Join(ws, resourceRelativePath)
			target, err := os.Readlink(wsResource)
			if err != nil {
				continue
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(wsResource), target)
			}
			target = filepath.Clean(target)
			if fingerprinted {
				if target == variants[i].StoragePath {
					pins = append(pins, ws)
				}
			} else if target == subtree {
				pins = append(pins, ws)
			}
		}
		sort.Strings(pins)
		if pins == nil {
			pins = []string{}
		}
		variants[i].InUseBy = pins
	}
}
