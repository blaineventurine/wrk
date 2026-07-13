package engine

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/location"
	"github.com/blaineventurine/wrk/internal/planner"
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
//   - every resource is isolated          → "isolated"
//   - otherwise             → "partial"
//
// `isolated` is a resting state — treated on par with linked/detached —
// but a workspace that mixes isolated with linked/detached is "partial"
// rather than any single resting label.
func rollupState(states []State) string {
	if len(states) == 0 {
		return "empty"
	}
	var hasUnhealthy, hasPending bool
	allLinkedOrExpected := true
	allDetached := true
	allIsolated := true
	for _, s := range states {
		switch s {
		case StateConflict, StateStale, StateMissing, StateNotLinked, StateAbsent:
			hasUnhealthy = true
			allLinkedOrExpected = false
			allDetached = false
			allIsolated = false
		case StatePending:
			hasPending = true
			allLinkedOrExpected = false
			allDetached = false
			allIsolated = false
		case StateLinked, StateExpected:
			allDetached = false
			allIsolated = false
		case StateDetached:
			allLinkedOrExpected = false
			allIsolated = false
		case StateIsolated:
			allLinkedOrExpected = false
			allDetached = false
		default:
			// Unknown states err on the side of unhealthy so the
			// workspace never falsely rolls up as healthy.
			hasUnhealthy = true
			allLinkedOrExpected = false
			allDetached = false
			allIsolated = false
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
	case allIsolated:
		return "isolated"
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
	// Isolated marks per-workspace private variants created by
	// `wrk relink --isolate`. Their directory name is a random
	// isolated-<hex> suffix, not a fingerprint, so Fingerprint is
	// empty for them. Always emitted — consumers switch on the bool
	// rather than sniffing the directory-name convention.
	Isolated bool `json:"isolated"`
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
		return nil, Wrapf(ErrConfigInvalid,
			"check .wrk.yml for syntax errors or invalid resource paths",
			err, "%s", err.Error())
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
		v := variantJSON{StoragePath: variantPath, InUseBy: []string{}}
		if isIsolatedVariantDir(name) {
			// Fingerprint stays "" — the dir name is a random
			// suffix, not a content-derived digest.
			v.Isolated = true
		} else {
			v.Fingerprint = name
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
	// Isolated variants all share Fingerprint == "", so break the tie
	// on StoragePath to keep output deterministic across runs.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Fingerprint != out[j].Fingerprint {
			return out[i].Fingerprint < out[j].Fingerprint
		}
		return out[i].StoragePath < out[j].StoragePath
	})
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

// ============================================================
// fingerprint
// ============================================================

// fingerprintInputJSON is the JSON projection of a single FingerprintInput.
// SizeBytes is elided with `omitempty` because a missing input carries a
// zero size that would otherwise be indistinguishable from a real empty
// file in the output — Exists is the authoritative "was it there?" bit.
type fingerprintInputJSON struct {
	Path      string `json:"path"`
	Exists    bool   `json:"exists"`
	SizeBytes int64  `json:"sizeBytes,omitempty"`
}

// fingerprintSnapshotJSON is the JSON projection of a FingerprintSnapshot.
// Fingerprint and StoragePath both carry `omitempty` because a Pinned
// side without a shared-storage symlink legitimately has neither, and
// callers infer "workspace path is not a symlink into shared storage"
// from their absence. Inputs is `omitempty` because Pinned never has
// inputs — the engine only records per-input state alongside Current.
type fingerprintSnapshotJSON struct {
	Fingerprint string                 `json:"fingerprint,omitempty"`
	StoragePath string                 `json:"storagePath,omitempty"`
	Inputs      []fingerprintInputJSON `json:"inputs,omitempty"`
}

// fingerprintResourceJSON identifies the resource this analysis targets.
// Only Name and Path surface — the raw fingerprint-input patterns live
// under `list --json` and would just duplicate here.
type fingerprintResourceJSON struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// fingerprintJSON is the top-level shape emitted by
// `wrk fingerprint --json`. Changed is never `omitempty`: a "not stale"
// result carries changed=false and consumers rely on the key being
// present to distinguish a real analysis from an incomplete envelope.
type fingerprintJSON struct {
	jsonEnvelope
	Resource fingerprintResourceJSON `json:"resource"`
	Current  fingerprintSnapshotJSON `json:"current"`
	Pinned   fingerprintSnapshotJSON `json:"pinned"`
	Changed  bool                    `json:"changed"`
	// Isolated is never `omitempty` either: consumers must be able to
	// distinguish "not isolated" from "field absent" without a schema
	// bump. When true, Pinned carries a storage path but no
	// fingerprint — the private variant's name is not a digest.
	Isolated bool `json:"isolated"`
}

// MarshalFingerprintJSON renders a FingerprintReport as pretty-printed
// JSON with the shared schema/kind envelope. A nil report yields a
// marshaling error rather than a zero-value envelope — there is no
// sensible "empty" fingerprint analysis, and silently emitting one
// would mask a programmer bug in the caller.
//
// The returned bytes carry no trailing newline; callers add one if the
// stream needs it.
func MarshalFingerprintJSON(report *FingerprintReport) ([]byte, error) {
	if report == nil {
		return nil, errors.New("MarshalFingerprintJSON: nil report")
	}
	out := fingerprintJSON{
		jsonEnvelope: jsonEnvelope{Schema: jsonSchema, Kind: "fingerprint"},
		Resource: fingerprintResourceJSON{
			Name: report.Resource.Name,
			Path: report.Resource.Path,
		},
		Current: fingerprintSnapshotJSON{
			Fingerprint: report.Current.Fingerprint,
			StoragePath: report.Current.StoragePath,
			Inputs:      inputProjection(report.Current.Inputs),
		},
		Pinned: fingerprintSnapshotJSON{
			Fingerprint: report.Pinned.Fingerprint,
			StoragePath: report.Pinned.StoragePath,
		},
		Changed:  report.Changed,
		Isolated: report.Isolated,
	}
	return json.MarshalIndent(out, "", "  ")
}

// inputProjection copies engine FingerprintInputs into their JSON
// projections. Returns nil for an empty input slice so `omitempty` on
// the snapshot's Inputs field can elide the key entirely rather than
// emitting `"inputs": []`.
func inputProjection(inputs []FingerprintInput) []fingerprintInputJSON {
	if len(inputs) == 0 {
		return nil
	}
	out := make([]fingerprintInputJSON, 0, len(inputs))
	for _, in := range inputs {
		out = append(out, fingerprintInputJSON(in))
	}
	return out
}

// ============================================================
// doctor
// ============================================================

// doctorChecksJSON is the JSON projection of DoctorChecks. Every
// string-slice field is emitted verbatim (never elided) so consumers
// can iterate without a nil check — see nilToEmpty below. ConfigError
// is the sole `omitempty` field: it's meaningful only when
// ConfigValid is false, and its presence in the output is itself the
// signal that config loading failed.
type doctorChecksJSON struct {
	ConfigValid       bool     `json:"configValid"`
	ConfigError       string   `json:"configError,omitempty"`
	GhostWorkspaces   []string `json:"ghostWorkspaces"`
	OrphanedLocks     []string `json:"orphanedLocks"`
	StaleProvisioning []string `json:"staleProvisioning"`
	StaleDeleting     []string `json:"staleDeleting"`
	StaleForgetting   []string `json:"staleForgetting"`
	StorageSizeBytes  int64    `json:"storageSizeBytes"`
}

// doctorJSON is the top-level shape emitted by `wrk doctor --json`.
// Issues is never `omitempty`: a healthy report carries `"issues": []`
// and consumers rely on the key being present to distinguish a real
// analysis from an incomplete envelope.
type doctorJSON struct {
	jsonEnvelope
	Root         string           `json:"root"`
	RepositoryID string           `json:"repositoryId"`
	VCS          string           `json:"vcs"`
	Checks       doctorChecksJSON `json:"checks"`
	Issues       []string         `json:"issues"`
}

// MarshalDoctorJSON renders a DoctorReport as pretty-printed JSON with
// the shared schema/kind envelope. A nil report yields a marshaling
// error rather than a zero-value envelope — there is no sensible
// "empty" health snapshot, and silently emitting one would mask a
// programmer bug in the caller.
//
// The returned bytes carry no trailing newline; callers add one if the
// stream needs it.
func MarshalDoctorJSON(report *DoctorReport) ([]byte, error) {
	if report == nil {
		return nil, errors.New("MarshalDoctorJSON: nil report")
	}
	out := doctorJSON{
		jsonEnvelope: jsonEnvelope{Schema: jsonSchema, Kind: "doctor"},
		Root:         report.Root,
		RepositoryID: report.RepositoryID,
		VCS:          report.VCS,
		Checks: doctorChecksJSON{
			ConfigValid:       report.Checks.ConfigValid,
			ConfigError:       report.Checks.ConfigError,
			GhostWorkspaces:   nilToEmpty(report.Checks.GhostWorkspaces),
			OrphanedLocks:     nilToEmpty(report.Checks.OrphanedLocks),
			StaleProvisioning: nilToEmpty(report.Checks.StaleProvisioning),
			StaleDeleting:     nilToEmpty(report.Checks.StaleDeleting),
			StaleForgetting:   nilToEmpty(report.Checks.StaleForgetting),
			StorageSizeBytes:  report.Checks.StorageSizeBytes,
		},
		Issues: nilToEmpty(report.Issues),
	}
	return json.MarshalIndent(out, "", "  ")
}

// nilToEmpty returns an empty []string when input is nil so JSON
// emits `[]` rather than `null`. This keeps every never-null slice
// field in the doctor projection safe to iterate without a nil check.
func nilToEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ============================================================
// destructive-command result envelope
// ============================================================

// resultEnvelopeJSON is the "what actually happened" projection
// attached to every destructive command's JSON output when the
// executor ran. In --dry-run mode, or when the confirmation prompt
// refused / previewed the plan, the pointer to this struct is nil
// (and the parent envelope's `result` key is omitted via omitempty).
//
// BytesFreed is the running total of bytes fed to Options.Progress
// during execution. Warnings collects any non-empty line the
// executor wrote to its Stdout — the CLI redirects Stdout to a
// bytes.Buffer under --json so the executor's chatter never
// pollutes the machine-readable stream.
type resultEnvelopeJSON struct {
	Attempted  bool     `json:"attempted"`
	BytesFreed int64    `json:"bytesFreed"`
	Warnings   []string `json:"warnings"`
}

// ============================================================
// workspaces
// ============================================================

// resourceCountJSON is the JSON projection of a WorkspaceSummary's
// per-state counts. Every field is emitted verbatim (never elided)
// so a consumer can read a coherent set of counters without a nil
// check — zeros are meaningful data, not noise.
type resourceCountJSON struct {
	Linked    int `json:"linked"`
	Detached  int `json:"detached"`
	Isolated  int `json:"isolated"`
	Pending   int `json:"pending"`
	Unhealthy int `json:"unhealthy"`
	Expected  int `json:"expected"`
}

// workspaceEntryJSON is the JSON projection of one WorkspaceSummary.
// State is the workspace-level rollup label (linked / detached /
// partial / pending / unhealthy / empty).
type workspaceEntryJSON struct {
	Root           string            `json:"root"`
	IsPrimary      bool              `json:"isPrimary"`
	State          string            `json:"state"`
	ResourceCounts resourceCountJSON `json:"resourceCounts"`
}

// workspacesJSON is the top-level shape emitted by
// `wrk workspaces --json`.
type workspacesJSON struct {
	jsonEnvelope
	Workspaces []workspaceEntryJSON `json:"workspaces"`
}

// MarshalWorkspacesJSON renders a slice of WorkspaceSummary values
// as pretty-printed JSON with the shared schema/kind envelope. Nil
// or empty input yields a stable envelope with `workspaces: []` so
// consumers can iterate without a nil check.
//
// Per-state counts are rolled up under resourceCounts. Unhealthy
// aggregates every state that would trigger the WorkspaceUnhealthy
// rollup (conflict, stale, missing, not-linked, absent) so a single
// counter reflects "resources needing attention", matching the
// human-readable `wrk workspaces` output.
//
// The returned bytes carry no trailing newline; callers add one if
// the stream needs it.
func MarshalWorkspacesJSON(summaries []WorkspaceSummary) ([]byte, error) {
	out := workspacesJSON{
		jsonEnvelope: jsonEnvelope{Schema: jsonSchema, Kind: "workspaces"},
		Workspaces:   make([]workspaceEntryJSON, 0, len(summaries)),
	}
	for _, s := range summaries {
		out.Workspaces = append(out.Workspaces, workspaceEntryJSON{
			Root:           s.Root,
			IsPrimary:      s.IsCurrent,
			State:          string(s.State),
			ResourceCounts: workspaceCounts(s.Counts),
		})
	}
	return json.MarshalIndent(out, "", "  ")
}

// workspaceCounts flattens a WorkspaceSummary.Counts map into the
// fixed-shape counter struct. Unhealthy sums the five states that
// drive the WorkspaceUnhealthy rollup so consumers get a single
// "problems" gauge rather than five near-empty fields.
func workspaceCounts(counts map[State]int) resourceCountJSON {
	return resourceCountJSON{
		Linked:   counts[StateLinked],
		Detached: counts[StateDetached],
		Isolated: counts[StateIsolated],
		Pending:  counts[StatePending],
		Unhealthy: counts[StateConflict] +
			counts[StateStale] +
			counts[StateMissing] +
			counts[StateNotLinked] +
			counts[StateAbsent],
		Expected: counts[StateExpected],
	}
}

// ============================================================
// gc
// ============================================================

// gcVariantJSON is the JSON projection of one on-disk variant slated
// for removal. Fingerprint is the empty string for un-fingerprinted
// resources (a single-variant resource has no digest to distinguish).
type gcVariantJSON struct {
	Resource    string `json:"resource"`
	Fingerprint string `json:"fingerprint"`
	StoragePath string `json:"storagePath"`
	SizeBytes   int64  `json:"sizeBytes"`
}

// gcPendingSwapJSON is the JSON projection of one mid-swap-crash
// recovery: the executor completes the Rename(Provisioning, Real)
// so on-disk state matches the last user-visible commit.
type gcPendingSwapJSON struct {
	Provisioning string `json:"provisioning"`
	Real         string `json:"real"`
}

// gcOrphanedIsolationJSON is the JSON projection of one isolation-
// registry entry whose workspace root is gone from disk. Cleared
// via clearIsolation.
type gcOrphanedIsolationJSON struct {
	WorkspaceRoot string `json:"workspaceRoot"`
	ResourcePath  string `json:"resourcePath"`
}

// gcPlanJSON is the JSON projection of a GCPlan. Every slice field is
// emitted verbatim (never elided) so consumers see `[]` instead of
// `null` on an empty sweep. The un-fingerprinted variants surface with
// an empty Fingerprint string.
type gcPlanJSON struct {
	VariantsToRemove         []gcVariantJSON           `json:"variantsToRemove"`
	GhostWorkspacesToPrune   []string                  `json:"ghostWorkspacesToPrune"`
	OrphanedLocks            []string                  `json:"orphanedLocks"`
	StaleProvisioning        []string                  `json:"staleProvisioning"`
	StaleDeleting            []string                  `json:"staleDeleting"`
	StaleForgetting          []string                  `json:"staleForgetting"`
	PendingSwaps             []gcPendingSwapJSON       `json:"pendingSwaps"`
	OrphanedIsolationEntries []gcOrphanedIsolationJSON `json:"orphanedIsolationEntries"`
	OrphanRegistry           []string                  `json:"orphanRegistry"`
	UnreachableWorkspaces    []string                  `json:"unreachableWorkspaces"`
	TotalBytesToFree         int64                     `json:"totalBytesToFree"`
}

// gcJSON is the top-level shape emitted by `wrk gc --json`. Result
// is a pointer so `omitempty` elides it entirely in --dry-run mode
// or when the operator refused / previewed the plan.
type gcJSON struct {
	jsonEnvelope
	DryRun bool                `json:"dryRun"`
	Plan   gcPlanJSON          `json:"plan"`
	Result *resultEnvelopeJSON `json:"result,omitempty"`
}

// GCJSONInput bundles the pieces MarshalGCJSON needs. Attempted flips
// the result envelope on: false means dry-run or refused (result key
// omitted); true means the executor ran and BytesFreed / Warnings
// carry the running tally.
type GCJSONInput struct {
	Plan       GCPlan
	DryRun     bool
	Attempted  bool
	BytesFreed int64
	Warnings   []string
}

// MarshalGCJSON renders a GCPlan and optional execution result as
// pretty-printed JSON with the shared schema/kind envelope.
//
// The returned bytes carry no trailing newline; callers add one if
// the stream needs it.
func MarshalGCJSON(in GCJSONInput) ([]byte, error) {
	out := gcJSON{
		jsonEnvelope: jsonEnvelope{Schema: jsonSchema, Kind: "gc"},
		DryRun:       in.DryRun,
		Plan:         projectGCPlan(in.Plan),
	}
	if in.Attempted {
		out.Result = &resultEnvelopeJSON{
			Attempted:  true,
			BytesFreed: in.BytesFreed,
			Warnings:   nilToEmpty(in.Warnings),
		}
	}
	return json.MarshalIndent(out, "", "  ")
}

// projectGCPlan flattens a GCPlan into its JSON shape, materialising
// empty slices in place of nil so the marshalled output emits `[]`
// rather than `null`.
func projectGCPlan(p GCPlan) gcPlanJSON {
	variants := make([]gcVariantJSON, 0, len(p.DeleteVariants))
	for _, v := range p.DeleteVariants {
		variants = append(variants, gcVariantJSON{
			Resource:    v.Resource,
			Fingerprint: v.Fingerprint,
			StoragePath: v.StoragePath,
			SizeBytes:   v.Size,
		})
	}

	swaps := make([]gcPendingSwapJSON, 0, len(p.PendingSwaps))
	for _, s := range p.PendingSwaps {
		swaps = append(swaps, gcPendingSwapJSON{
			Provisioning: s.Provisioning,
			Real:         s.Real,
		})
	}

	iso := make([]gcOrphanedIsolationJSON, 0, len(p.OrphanedIsolationEntries))
	for _, e := range p.OrphanedIsolationEntries {
		iso = append(iso, gcOrphanedIsolationJSON{
			WorkspaceRoot: e.WorkspaceRoot,
			ResourcePath:  e.ResourcePath,
		})
	}

	return gcPlanJSON{
		VariantsToRemove:         variants,
		GhostWorkspacesToPrune:   nilToEmpty(p.Ghosts),
		OrphanedLocks:            nilToEmpty(p.OrphanedLocks),
		StaleProvisioning:        nilToEmpty(p.StaleProvisioning),
		StaleDeleting:            nilToEmpty(p.StaleDeleting),
		StaleForgetting:          nilToEmpty(p.StaleForgetting),
		PendingSwaps:             swaps,
		OrphanedIsolationEntries: iso,
		OrphanRegistry:           nilToEmpty(p.OrphanRegistry),
		UnreachableWorkspaces:    nilToEmpty(p.UnreachableWorkspaces),
		TotalBytesToFree:         p.TotalBytesFreed,
	}
}

// ============================================================
// remove
// ============================================================

// removePlanJSON is the JSON projection of a RemovePlan. Refusal
// carries `omitempty` because it is meaningful only when the plan
// carries a soft refusal reason; the empty case would otherwise
// pollute the payload with an always-empty string.
type removePlanJSON struct {
	Target             string   `json:"target"`
	Backend            string   `json:"backend"`
	VCSCommand         string   `json:"vcsCommand"`
	UncommittedChanges int      `json:"uncommittedChanges"`
	DetachedPaths      []string `json:"detachedPaths"`
	TotalBytesToFree   int64    `json:"totalBytesToFree"`
	Refusal            string   `json:"refusal,omitempty"`
	IsGhost            bool     `json:"isGhost"`
}

// removeJSON is the top-level shape emitted by `wrk remove --json`.
type removeJSON struct {
	jsonEnvelope
	DryRun bool                `json:"dryRun"`
	Plan   removePlanJSON      `json:"plan"`
	Result *resultEnvelopeJSON `json:"result,omitempty"`
}

// RemoveJSONInput bundles the pieces MarshalRemoveJSON needs.
type RemoveJSONInput struct {
	Plan       RemovePlan
	DryRun     bool
	Attempted  bool
	BytesFreed int64
	Warnings   []string
}

// MarshalRemoveJSON renders a RemovePlan and optional execution
// result as pretty-printed JSON with the shared schema/kind
// envelope.
//
// The returned bytes carry no trailing newline; callers add one if
// the stream needs it.
func MarshalRemoveJSON(in RemoveJSONInput) ([]byte, error) {
	out := removeJSON{
		jsonEnvelope: jsonEnvelope{Schema: jsonSchema, Kind: "remove"},
		DryRun:       in.DryRun,
		Plan: removePlanJSON{
			Target:             in.Plan.Target,
			Backend:            in.Plan.Backend,
			VCSCommand:         in.Plan.VCSCommand,
			UncommittedChanges: in.Plan.UncommittedChanges,
			DetachedPaths:      nilToEmpty(in.Plan.DetachedPaths),
			TotalBytesToFree:   in.Plan.TotalBytes,
			Refusal:            in.Plan.Refusal,
			IsGhost:            in.Plan.IsGhost,
		},
	}
	if in.Attempted {
		out.Result = &resultEnvelopeJSON{
			Attempted:  true,
			BytesFreed: in.BytesFreed,
			Warnings:   nilToEmpty(in.Warnings),
		}
	}
	return json.MarshalIndent(out, "", "  ")
}

// ============================================================
// forget
// ============================================================

// forgetPlanJSON is the JSON projection of a ForgetPlan. RegistryEntries
// carries the sorted list of workspace roots that still hold detach
// entries — the underlying map is collapsed to a flat slice so consumers
// see stable ordering across runs.
type forgetPlanJSON struct {
	RepositoryID    string   `json:"repositoryId"`
	StoragePath     string   `json:"storagePath"`
	VariantCount    int      `json:"variantCount"`
	ResourceCount   int      `json:"resourceCount"`
	TotalSize       int64    `json:"totalSize"`
	RegistryEntries []string `json:"registryEntries"`
	Refusal         string   `json:"refusal,omitempty"`
}

// forgetJSON is the top-level shape emitted by `wrk forget --json`.
type forgetJSON struct {
	jsonEnvelope
	DryRun bool                `json:"dryRun"`
	Plan   forgetPlanJSON      `json:"plan"`
	Result *resultEnvelopeJSON `json:"result,omitempty"`
}

// ForgetJSONInput bundles the pieces MarshalForgetJSON needs.
type ForgetJSONInput struct {
	Plan       ForgetPlan
	DryRun     bool
	Attempted  bool
	BytesFreed int64
	Warnings   []string
}

// MarshalForgetJSON renders a ForgetPlan and optional execution
// result as pretty-printed JSON with the shared schema/kind
// envelope.
//
// The returned bytes carry no trailing newline; callers add one if
// the stream needs it.
func MarshalForgetJSON(in ForgetJSONInput) ([]byte, error) {
	roots := make([]string, 0, len(in.Plan.RegistryEntries))
	for root := range in.Plan.RegistryEntries {
		roots = append(roots, root)
	}
	sort.Strings(roots)

	out := forgetJSON{
		jsonEnvelope: jsonEnvelope{Schema: jsonSchema, Kind: "forget"},
		DryRun:       in.DryRun,
		Plan: forgetPlanJSON{
			RepositoryID:    in.Plan.RepositoryID,
			StoragePath:     in.Plan.StoragePath,
			VariantCount:    in.Plan.VariantCount,
			ResourceCount:   in.Plan.ResourceCount,
			TotalSize:       in.Plan.TotalSize,
			RegistryEntries: roots,
			Refusal:         in.Plan.Refusal,
		},
	}
	if in.Attempted {
		out.Result = &resultEnvelopeJSON{
			Attempted:  true,
			BytesFreed: in.BytesFreed,
			Warnings:   nilToEmpty(in.Warnings),
		}
	}
	return json.MarshalIndent(out, "", "  ")
}

// ============================================================
// run
// ============================================================

// runResourceJSON is the JSON projection of one resource-scoped
// destructive command target. Reused across `wrk run` and
// `wrk relink --isolate` — both surface a resource identity with
// nothing else worth transmitting.
type runResourceJSON struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// runPlanJSON is the JSON projection of a RunPlan. CommandCount is
// the hook's Commands length hoisted here so consumers can display
// "N commands" without inspecting a nested structure.
type runPlanJSON struct {
	Resource     runResourceJSON `json:"resource"`
	VariantPath  string          `json:"variantPath"`
	CommandCount int             `json:"commandCount"`
}

// runJSON is the top-level shape emitted by `wrk run --json`.
type runJSON struct {
	jsonEnvelope
	DryRun bool                `json:"dryRun"`
	Plan   runPlanJSON         `json:"plan"`
	Result *resultEnvelopeJSON `json:"result,omitempty"`
}

// RunJSONInput bundles the pieces MarshalRunJSON needs.
type RunJSONInput struct {
	Plan       RunPlan
	DryRun     bool
	Attempted  bool
	BytesFreed int64
	Warnings   []string
}

// MarshalRunJSON renders a RunPlan and optional execution result as
// pretty-printed JSON with the shared schema/kind envelope. The
// resolved variant path is best-effort: for a non-fingerprinted
// resource the plan's first action's Instance carries it; when no
// actions were built (empty resource set) VariantPath is empty.
//
// The returned bytes carry no trailing newline; callers add one if
// the stream needs it.
func MarshalRunJSON(in RunJSONInput) ([]byte, error) {
	out := runJSON{
		jsonEnvelope: jsonEnvelope{Schema: jsonSchema, Kind: "run"},
		DryRun:       in.DryRun,
		Plan: runPlanJSON{
			Resource: runResourceJSON{
				Name: in.Plan.Resource.Name,
				Path: in.Plan.Resource.Path,
			},
			VariantPath:  runVariantPath(in.Plan),
			CommandCount: len(in.Plan.Commands),
		},
	}
	if in.Attempted {
		out.Result = &resultEnvelopeJSON{
			Attempted:  true,
			BytesFreed: in.BytesFreed,
			Warnings:   nilToEmpty(in.Warnings),
		}
	}
	return json.MarshalIndent(out, "", "  ")
}

// runVariantPath extracts the resolved shared-storage path from a
// RunPlan's first action, or "" if the plan carries no actions or
// the first action is not an InitializeResource. The executor is
// the ultimate authority on which variant a run touches, but the
// plan-time resolved path (Context.Shared) is the same value and is
// safe to surface for preview purposes.
func runVariantPath(plan RunPlan) string {
	if len(plan.Actions) == 0 {
		return ""
	}
	init, ok := plan.Actions[0].Action.(planner.InitializeResource)
	if !ok {
		return ""
	}
	return init.Context.Shared
}

// ============================================================
// relink
// ============================================================

// relinkPlanJSON is the JSON projection of a plain relink plan. The
// planner.Action union is intentionally flattened to one-line
// descriptions — a machine consumer verifying "what would happen"
// gets the exact strings the human path prints, without coupling to
// the internal action shape.
type relinkPlanJSON struct {
	ActionCount  int      `json:"actionCount"`
	Descriptions []string `json:"descriptions"`
}

// relinkJSON is the top-level shape emitted by `wrk relink --json`.
type relinkJSON struct {
	jsonEnvelope
	DryRun bool                `json:"dryRun"`
	Plan   relinkPlanJSON      `json:"plan"`
	Result *resultEnvelopeJSON `json:"result,omitempty"`
}

// RelinkJSONInput bundles the pieces MarshalRelinkJSON needs.
type RelinkJSONInput struct {
	Plan       planner.Plan
	DryRun     bool
	Attempted  bool
	BytesFreed int64
	Warnings   []string
}

// MarshalRelinkJSON renders a relink planner.Plan and optional
// execution result as pretty-printed JSON with the shared
// schema/kind envelope.
//
// The returned bytes carry no trailing newline; callers add one if
// the stream needs it.
func MarshalRelinkJSON(in RelinkJSONInput) ([]byte, error) {
	out := relinkJSON{
		jsonEnvelope: jsonEnvelope{Schema: jsonSchema, Kind: "relink"},
		DryRun:       in.DryRun,
		Plan: relinkPlanJSON{
			ActionCount:  len(in.Plan.Actions),
			Descriptions: actionDescriptions(in.Plan.Actions),
		},
	}
	if in.Attempted {
		out.Result = &resultEnvelopeJSON{
			Attempted:  true,
			BytesFreed: in.BytesFreed,
			Warnings:   nilToEmpty(in.Warnings),
		}
	}
	return json.MarshalIndent(out, "", "  ")
}

// ============================================================
// relink --isolate
// ============================================================

// relinkIsolatePlanJSON is the JSON projection of an IsolatePlan.
// Reuses runResourceJSON so `wrk relink --isolate --json` and
// `wrk run --json` speak the same resource shape.
type relinkIsolatePlanJSON struct {
	Resources []runResourceJSON `json:"resources"`
}

// relinkIsolateJSON is the top-level shape emitted by
// `wrk relink --isolate --json`.
type relinkIsolateJSON struct {
	jsonEnvelope
	DryRun bool                  `json:"dryRun"`
	Plan   relinkIsolatePlanJSON `json:"plan"`
	Result *resultEnvelopeJSON   `json:"result,omitempty"`
}

// RelinkIsolateJSONInput bundles the pieces
// MarshalRelinkIsolateJSON needs.
type RelinkIsolateJSONInput struct {
	Plan       IsolatePlan
	DryRun     bool
	Attempted  bool
	BytesFreed int64
	Warnings   []string
}

// MarshalRelinkIsolateJSON renders an IsolatePlan and optional
// execution result as pretty-printed JSON with the shared
// schema/kind envelope.
//
// The returned bytes carry no trailing newline; callers add one if
// the stream needs it.
func MarshalRelinkIsolateJSON(in RelinkIsolateJSONInput) ([]byte, error) {
	resources := make([]runResourceJSON, 0, len(in.Plan.Resources))
	for _, r := range in.Plan.Resources {
		resources = append(resources, runResourceJSON{
			Name: r.Name,
			Path: r.Path,
		})
	}

	out := relinkIsolateJSON{
		jsonEnvelope: jsonEnvelope{Schema: jsonSchema, Kind: "relink-isolate"},
		DryRun:       in.DryRun,
		Plan:         relinkIsolatePlanJSON{Resources: resources},
	}
	if in.Attempted {
		out.Result = &resultEnvelopeJSON{
			Attempted:  true,
			BytesFreed: in.BytesFreed,
			Warnings:   nilToEmpty(in.Warnings),
		}
	}
	return json.MarshalIndent(out, "", "  ")
}

// ============================================================
// detach
// ============================================================

// detachPlanJSON is the JSON projection of a detach planner.Plan.
// Shape matches relinkPlanJSON so consumers can share a parser for
// the two default-flow destructive commands.
type detachPlanJSON struct {
	ActionCount  int      `json:"actionCount"`
	Descriptions []string `json:"descriptions"`
}

// detachJSON is the top-level shape emitted by `wrk detach --json`.
type detachJSON struct {
	jsonEnvelope
	DryRun bool                `json:"dryRun"`
	Plan   detachPlanJSON      `json:"plan"`
	Result *resultEnvelopeJSON `json:"result,omitempty"`
}

// DetachJSONInput bundles the pieces MarshalDetachJSON needs.
type DetachJSONInput struct {
	Plan       planner.Plan
	DryRun     bool
	Attempted  bool
	BytesFreed int64
	Warnings   []string
}

// MarshalDetachJSON renders a detach planner.Plan and optional
// execution result as pretty-printed JSON with the shared
// schema/kind envelope.
//
// The returned bytes carry no trailing newline; callers add one if
// the stream needs it.
func MarshalDetachJSON(in DetachJSONInput) ([]byte, error) {
	out := detachJSON{
		jsonEnvelope: jsonEnvelope{Schema: jsonSchema, Kind: "detach"},
		DryRun:       in.DryRun,
		Plan: detachPlanJSON{
			ActionCount:  len(in.Plan.Actions),
			Descriptions: actionDescriptions(in.Plan.Actions),
		},
	}
	if in.Attempted {
		out.Result = &resultEnvelopeJSON{
			Attempted:  true,
			BytesFreed: in.BytesFreed,
			Warnings:   nilToEmpty(in.Warnings),
		}
	}
	return json.MarshalIndent(out, "", "  ")
}

// actionDescriptions renders each PlannedAction's Action using the
// same describeAction helper the human printer uses, dropping the
// "warn" bit. Returns an empty (non-nil) slice for a nil / empty
// action list so the JSON output emits `[]` rather than `null`.
func actionDescriptions(actions []planner.PlannedAction) []string {
	descriptions := make([]string, 0, len(actions))
	for _, pa := range actions {
		desc, _ := describeAction(pa.Action)
		descriptions = append(descriptions, desc)
	}
	return descriptions
}
