package engine

import (
	"fmt"
	"path/filepath"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/repository"
)

// DoctorReport is a health snapshot for one repository.
type DoctorReport struct {
	// Root is the absolute path of the repository root the report covers.
	Root string
	// RepositoryID is the storage-side identifier used to key shared
	// storage. Included for cross-referencing wrk output and disk state.
	RepositoryID string
	// VCS is the string form of the detected VCS ("git" | "jj").
	VCS string

	Checks DoctorChecks

	// Issues is a human-readable list of things needing attention.
	// Empty means clean.
	Issues []string
}

// DoctorChecks groups the individual health probes so callers can
// consume them structured (e.g. for the JSON marshaler) rather than
// re-parsing the free-form Issues slice.
type DoctorChecks struct {
	// ConfigValid is true when config.Load succeeded.
	ConfigValid bool
	// ConfigError is the config.Load error's Error() string when
	// ConfigValid is false; empty otherwise.
	ConfigError string

	// GhostWorkspaces are absolute workspace roots that VCS metadata
	// still references but whose working directory is missing.
	GhostWorkspaces []string

	// OrphanedLocks / StaleProvisioning / StaleDeleting / StaleForgetting
	// are absolute paths of bookkeeping cruft that `wrk gc` would sweep.
	OrphanedLocks     []string
	StaleProvisioning []string
	StaleDeleting     []string
	StaleForgetting   []string

	// StorageSizeBytes is the total on-disk size under
	// <StorageRoot>/<RepositoryID>/, i.e. everything wrk stores for
	// this repository. Zero when storage doesn't exist yet or the walk
	// fails silently.
	StorageSizeBytes int64
}

// Doctor produces a health snapshot for the given repository. It never
// mutates anything on disk.
//
// Errors from underlying subsystems that indicate a real repository or
// filesystem problem are recorded as Issues and surfaced in the report;
// only wholly-broken preconditions (nil repo) return an error directly.
func Doctor(repo *repository.Repository, options Options) (*DoctorReport, error) {
	if repo == nil {
		return nil, fmt.Errorf("Doctor: nil repo")
	}
	report := &DoctorReport{
		Root:         repo.Root,
		RepositoryID: repo.RepositoryID,
		VCS:          string(repo.VCS()),
	}

	// Config check. A parse/validation failure is the single most
	// actionable thing to surface first, so it heads the Issues list.
	if _, cfgErr := config.Load(repo.Root); cfgErr != nil {
		report.Checks.ConfigValid = false
		report.Checks.ConfigError = cfgErr.Error()
		report.Issues = append(report.Issues, "config invalid: "+cfgErr.Error())
	} else {
		report.Checks.ConfigValid = true
	}

	// Ghost workspaces — a backend probe error (e.g. corrupted VCS
	// metadata) is tolerated: the rest of the report is still useful.
	// Missing ghosts is not a report failure.
	if ghosts, err := repo.DetectGhosts(); err == nil {
		report.Checks.GhostWorkspaces = ghosts
		if len(ghosts) > 0 {
			report.Issues = append(report.Issues, fmt.Sprintf(
				"%d ghost workspace(s) — run `wrk gc`", len(ghosts)))
		}
	}

	// Stale bookkeeping via the existing gc detector — read-only,
	// same probe wrk gc would run.
	if bookkeeping, err := cleanBookkeepingDetect(repo, options); err == nil {
		report.Checks.OrphanedLocks = bookkeeping.OrphanedLocks
		report.Checks.StaleProvisioning = bookkeeping.StaleProvisioning
		report.Checks.StaleDeleting = bookkeeping.StaleDeleting
		report.Checks.StaleForgetting = bookkeeping.StaleForgetting
		if n := bookkeepingCount(bookkeeping); n > 0 {
			report.Issues = append(report.Issues, fmt.Sprintf(
				"%d stale bookkeeping item(s) — run `wrk gc`", n))
		}
	}

	// Storage size. treeSize tolerates a missing root (returns 0 with
	// no error), so an unlinked repo simply reports zero bytes.
	storagePath := filepath.Join(options.StorageRoot, repo.RepositoryID)
	if size, err := treeSize(storagePath); err == nil {
		report.Checks.StorageSizeBytes = size
	}

	return report, nil
}

// bookkeepingCount returns the total number of cruft entries the gc
// detector found — used to word the Issues hint in one line rather
// than four.
func bookkeepingCount(b bookkeepingCleanup) int {
	return len(b.OrphanedLocks) +
		len(b.StaleProvisioning) +
		len(b.StaleDeleting) +
		len(b.StaleForgetting)
}
