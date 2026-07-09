package engine

import (
	"fmt"
	"io"
	"strings"
)

// PrintGCPlan writes a human-readable summary of plan to w. When the
// plan mutates nothing, the entire output is "Nothing to do.\n".
// Empty categories are omitted. Kept variants are listed under their
// resource so the user sees WHAT survives, not just what disappears.
func PrintGCPlan(w io.Writer, plan GCPlan) {
	if plan.HasNothing() {
		fmt.Fprint(w, "Nothing to do.\n")
		return
	}

	var sb strings.Builder

	// Ghost workspaces
	if len(plan.Ghosts) > 0 {
		sb.WriteString("\nGhost workspaces (")
		sb.WriteString(pluralInt(len(plan.Ghosts)))
		sb.WriteString(", VCS metadata will be pruned):\n")
		for _, path := range plan.Ghosts {
			sb.WriteString("  ✗ ")
			sb.WriteString(path)
			sb.WriteString("\n")
		}
	}

	// Registry entries
	if len(plan.OrphanRegistry) > 0 {
		sb.WriteString("\nRegistry entries (")
		sb.WriteString(pluralInt(len(plan.OrphanRegistry)))
		sb.WriteString(" orphaned):\n")
		for _, root := range plan.OrphanRegistry {
			sb.WriteString("  ✗ ")
			sb.WriteString(root)
			sb.WriteString("\n")
		}
	}

	// Unreachable workspaces
	if len(plan.UnreachableWorkspaces) > 0 {
		sb.WriteString("\nUnreachable workspaces (")
		sb.WriteString(pluralInt(len(plan.UnreachableWorkspaces)))
		sb.WriteString(", kept everything to be safe):\n")
		for _, root := range plan.UnreachableWorkspaces {
			sb.WriteString("  ? ")
			sb.WriteString(root)
			sb.WriteString("\n")
		}
	}

	// Variant tables grouped by resource
	variantsByResource := groupVariantsByResource(plan.DeleteVariants, plan.KeepVariants)
	if len(variantsByResource) > 0 {
		for _, resourceGroup := range variantsByResource {
			sb.WriteString("\n")
			sb.WriteString(resourceGroup.Path)
			sb.WriteString(":\n")

			// Combine and sort: delete first, then keep
			var allVariants []variantLine
			for _, v := range resourceGroup.DeleteVariants {
				allVariants = append(allVariants, variantLine{
					marker:      "✗",
					v:           v,
					isKept:      false,
				})
			}
			for _, v := range resourceGroup.KeepVariants {
				allVariants = append(allVariants, variantLine{
					marker:      "✓",
					v:           v,
					isKept:      true,
				})
			}

			for _, vl := range allVariants {
				sb.WriteString("  ")
				sb.WriteString(vl.marker)
				sb.WriteString(" ")

				// Fingerprint or <unversioned>
				if vl.v.Fingerprint == "" {
					sb.WriteString("<unversioned>")
				} else {
					sb.WriteString(vl.v.Fingerprint)
				}

				sb.WriteString("\t")
				sb.WriteString(HumanSize(vl.v.Size))
				sb.WriteString("   ")

				// Last used date
				sb.WriteString("last used ")
				sb.WriteString(vl.v.LastUsed.Format("2006-01-02"))

				// Kept marker
				if vl.isKept {
					sb.WriteString("  (kept)")
				}

				sb.WriteString("\n")
			}
		}
	}

	// Totals line
	sb.WriteString("\nTotal:")
	totalParts := []string{}

	// Count DeleteVariants only (these are being removed)
	if len(plan.DeleteVariants) > 0 {
		totalParts = append(totalParts, pluralInt(len(plan.DeleteVariants))+" variant")
	}

	// Bytes freed
	if plan.TotalBytesFreed > 0 {
		totalParts = append(totalParts, HumanSize(plan.TotalBytesFreed)+" reclaimed")
	}

	// Ghosts pruned
	if len(plan.Ghosts) > 0 {
		totalParts = append(totalParts, pluralInt(len(plan.Ghosts))+" ghost")
	}

	// Registry entries removed
	if len(plan.OrphanRegistry) > 0 {
		totalParts = append(totalParts, pluralInt(len(plan.OrphanRegistry))+" registry entr")
	}

	// Bookkeeping cleaned
	totalBookkeeping := len(plan.OrphanedLocks) + len(plan.StaleProvisioning) + len(plan.StaleDeleting) + len(plan.StaleForgetting)
	if totalBookkeeping > 0 {
		totalParts = append(totalParts, pluralInt(totalBookkeeping)+" bookkeeping")
	}

	if len(totalParts) > 0 {
		sb.WriteString(" ")
		sb.WriteString(strings.Join(totalParts, "; "))
	}
	sb.WriteString(".\n")

	fmt.Fprint(w, strings.TrimLeft(sb.String(), "\n"))
}

type variantLine struct {
	marker string
	v      variant
	isKept bool
}

type resourceGroup struct {
	Path            string
	DeleteVariants  []variant
	KeepVariants    []variant
}

// groupVariantsByResource organizes variants by resource name + path.
func groupVariantsByResource(deleteVariants, keepVariants []variant) []resourceGroup {
	// Map resource path to group
	groupMap := make(map[string]*resourceGroup)
	groupOrder := []string{}

	// Process delete variants first to maintain order
	for _, v := range deleteVariants {
		key := v.Path
		if _, exists := groupMap[key]; !exists {
			groupMap[key] = &resourceGroup{Path: v.Path}
			groupOrder = append(groupOrder, key)
		}
		groupMap[key].DeleteVariants = append(groupMap[key].DeleteVariants, v)
	}

	// Process keep variants
	for _, v := range keepVariants {
		key := v.Path
		if _, exists := groupMap[key]; !exists {
			groupMap[key] = &resourceGroup{Path: v.Path}
			groupOrder = append(groupOrder, key)
		}
		groupMap[key].KeepVariants = append(groupMap[key].KeepVariants, v)
	}

	// Build result maintaining order
	var result []resourceGroup
	for _, key := range groupOrder {
		result = append(result, *groupMap[key])
	}
	return result
}

// pluralInt returns a string representation of an integer with proper
// pluralization suffix when needed.
func pluralInt(n int) string {
	if n == 1 {
		return "1"
	}
	return fmt.Sprintf("%d", n)
}
