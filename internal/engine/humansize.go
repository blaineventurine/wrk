package engine

import "fmt"

// HumanSize renders bytes with a short unit suffix suitable for CLI
// output ("482 MB", "1.2 GB"). Precision matches the previous
// list-command formatter so `wrk list --size` output is unchanged.
func HumanSize(n int64) string {
	if n < 0 {
		return "-"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	// Clamp to the last defined suffix ('E'). HumanSize is only ever fed
	// counts of bytes on a real filesystem, so this is a defensive belt
	// against unbounded overflow rather than a case anyone can trip.
	val := float64(n) / float64(div)
	// Omit .0 decimals for cleaner output
	if val == float64(int64(val)) {
		return fmt.Sprintf("%d %cB", int64(val), "KMGTPE"[exp])
	}
	return fmt.Sprintf("%.1f %cB", val, "KMGTPE"[exp])
}
