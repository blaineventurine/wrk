package engine

import (
	"strings"
	"testing"
	"time"
)

func TestPrintGCPlanEmpty(t *testing.T) {
	var buf strings.Builder
	PrintGCPlan(&buf, GCPlan{})
	if got := buf.String(); got != "Nothing to do.\n" {
		t.Fatalf("empty plan output = %q, want %q", got, "Nothing to do.\n")
	}
}

func TestPrintGCPlanFull(t *testing.T) {
	now := time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC)
	plan := GCPlan{
		Ghosts:         []string{"/ws/feature-auth"},
		OrphanRegistry: []string{"/ws/feature-auth"},
		KeepVariants: []variant{{
			Resource:    "node",
			Path:        "node_modules",
			Fingerprint: "b3d8a60c",
			StoragePath: "/s/node/b3d8a60c",
			Size:        478 * 1024 * 1024,
			LastUsed:    now,
		}},
		DeleteVariants: []variant{{
			Resource:    "node",
			Path:        "node_modules",
			Fingerprint: "5fd1d0d6",
			StoragePath: "/s/node/5fd1d0d6",
			Size:        482 * 1024 * 1024,
			LastUsed:    now,
		}},
		OrphanedLocks:   []string{"/s/node/dead.wrk-lock"},
		TotalBytesFreed: 482 * 1024 * 1024,
	}

	var buf strings.Builder
	PrintGCPlan(&buf, plan)
	out := buf.String()

	for _, want := range []string{
		"Ghost workspaces (1",
		"/ws/feature-auth",
		"Registry entries (1",
		"node_modules:",
		"5fd1d0d6",
		"482 MB",
		"b3d8a60c",
		"(kept)",
		"last used 2026-07-03",
		"Total:",
		"1 variant",
		"1 ghost",
		"1 registry entr",
		"1 bookkeeping",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s\n---", want, out)
		}
	}
}

func TestPrintGCPlanOnlyGhosts(t *testing.T) {
	var buf strings.Builder
	PrintGCPlan(&buf, GCPlan{
		Ghosts: []string{"/ws/foo"},
	})
	out := buf.String()
	if !strings.Contains(out, "Ghost workspaces (1") {
		t.Errorf("missing ghost header: %q", out)
	}
	if strings.Contains(out, "node_modules") {
		t.Errorf("ghost-only output shouldn't reference resource paths: %q", out)
	}
	if !strings.Contains(out, "Total:") {
		t.Errorf("totals line missing: %q", out)
	}
}
