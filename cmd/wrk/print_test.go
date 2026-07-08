package main

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/engine"
)

// stripTabEsc removes any tabwriter.Escape (\xff) sentinel bytes from
// s. Historically colorWrap wrapped ANSI codes with these; that
// approach was abandoned when we realized it neither hid ANSI from
// tabwriter's width calculation NOR stripped the sentinels without
// the StripEscape flag. Alignment now lives in writeAligned
// (table.go). Kept as a defensive no-op so tests that call it stay
// green regardless — if a stray 0xff ever regresses in, several tests
// will fail loudly via the direct ContainsRune(0xff) checks.
func stripTabEsc(s string) string {
	return strings.ReplaceAll(s, "\xff", "")
}

// TestPrintStatusHeadersRowsAndStates pins the contract of
// printStatus for the default (single-workspace) view: one header line
// with the expected columns, one row per resource, each row carries
// the resource name, workspace-relative path, and the state string.
//
// A mutation that dropped a row, misordered the columns, or emitted a
// stale header would flip this test red — that's the point.
func TestPrintStatusHeadersRowsAndStates(t *testing.T) {
	// Force color off so the state cell is a plain "linked"/"detached"
	// string and substring assertions aren't fighting ANSI sequences.
	prev := noColor
	noColor = true
	defer func() { noColor = prev }()

	report := &engine.StatusReport{
		Rows: []engine.ResourceStatus{
			{Resource: "env", Path: ".env", State: engine.StateLinked, Origin: config.OriginShared},
			{Resource: "node_modules", Path: "node_modules", State: engine.StateDetached, Origin: config.OriginShared},
			{Resource: "cache", Path: ".cache", State: engine.StateConflict, Origin: config.OriginShared},
		},
	}

	var buf bytes.Buffer
	if err := printStatus(&buf, report, false); err != nil {
		t.Fatalf("printStatus: %v", err)
	}
	out := buf.String()

	// Header: single-workspace, all-shared origins → no WORKSPACE, no
	// ORIGIN. Anything else is a regression.
	firstLine := strings.SplitN(out, "\n", 2)[0]
	wantCols := []string{"RESOURCE", "PATH", "STATE", "FINGERPRINT"}
	for _, col := range wantCols {
		if !strings.Contains(firstLine, col) {
			t.Errorf("header missing column %q; got %q", col, firstLine)
		}
	}
	if strings.Contains(firstLine, "WORKSPACE") {
		t.Errorf("single-workspace header should not include WORKSPACE, got %q", firstLine)
	}
	if strings.Contains(firstLine, "ORIGIN") {
		t.Errorf("all-shared rows should not surface ORIGIN column, got %q", firstLine)
	}

	// One row per resource, each mentioning its name, path, and state.
	for _, want := range []struct{ name, path, state string }{
		{"env", ".env", "linked"},
		{"node_modules", "node_modules", "detached"},
		{"cache", ".cache", "conflict"},
	} {
		found := false
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, want.name) &&
				strings.Contains(line, want.path) &&
				strings.Contains(line, want.state) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no row for %q with path %q and state %q; output:\n%s",
				want.name, want.path, want.state, out)
		}
	}
}

// TestPrintStatusMultipleSourcesShowsConfigHeader pins the "Config: "
// preamble: it appears only when a local override is in play (len(Sources)
// > 1), so the default terse output is preserved for the common case.
func TestPrintStatusMultipleSourcesShowsConfigHeader(t *testing.T) {
	prev := noColor
	noColor = true
	defer func() { noColor = prev }()

	cases := []struct {
		name    string
		sources []string
		wantHdr bool
	}{
		{"only shared", []string{".wrk.yml"}, false},
		{"shared+local", []string{".wrk.yml", ".wrk.local.yml"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			report := &engine.StatusReport{
				Sources: c.sources,
				Rows: []engine.ResourceStatus{
					{Resource: "env", Path: ".env", State: engine.StateLinked, Origin: config.OriginShared},
				},
			}
			var buf bytes.Buffer
			if err := printStatus(&buf, report, false); err != nil {
				t.Fatalf("printStatus: %v", err)
			}
			got := stripTabEsc(buf.String())
			hasHdr := strings.Contains(got, "Config:")
			if hasHdr != c.wantHdr {
				t.Fatalf(
					"Config: header present=%v, want=%v; output:\n%s",
					hasHdr, c.wantHdr, got,
				)
			}
			if c.wantHdr {
				for _, src := range c.sources {
					if !strings.Contains(got, src) {
						t.Errorf("Config: header missing source %q; output:\n%s", src, got)
					}
				}
			}
		})
	}
}

// TestPrintStatusOriginColumnGatedByNonSharedRow pins the S9 UX
// promise: the ORIGIN column is only rendered when at least one row
// carries a non-shared origin (local or local-override). All-shared
// reports keep the table narrower.
func TestPrintStatusOriginColumnGatedByNonSharedRow(t *testing.T) {
	prev := noColor
	noColor = true
	defer func() { noColor = prev }()

	cases := []struct {
		name       string
		origins    []config.Origin
		wantColumn bool
	}{
		{"all shared", []config.Origin{config.OriginShared, config.OriginShared}, false},
		{"one local", []config.Origin{config.OriginShared, config.OriginLocal}, true},
		{"one override", []config.Origin{config.OriginShared, config.OriginLocalOverride}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rows := make([]engine.ResourceStatus, len(c.origins))
			for i, o := range c.origins {
				rows[i] = engine.ResourceStatus{
					Resource: "r",
					Path:     "p",
					State:    engine.StateLinked,
					Origin:   o,
				}
			}
			report := &engine.StatusReport{Rows: rows}
			var buf bytes.Buffer
			if err := printStatus(&buf, report, false); err != nil {
				t.Fatalf("printStatus: %v", err)
			}
			header := strings.SplitN(buf.String(), "\n", 2)[0]
			hasCol := strings.Contains(header, "ORIGIN")
			if hasCol != c.wantColumn {
				t.Fatalf(
					"ORIGIN column present=%v, want=%v; header=%q",
					hasCol, c.wantColumn, header,
				)
			}
		})
	}
}

// TestPrintStatusFingerprintTruncated confirms `short()` is applied
// to fingerprint cells: a long hex fingerprint renders as its first
// 12 characters, an empty fingerprint renders as "-". This is the
// wire between status.short and the tabwriter row.
func TestPrintStatusFingerprintTruncated(t *testing.T) {
	prev := noColor
	noColor = true
	defer func() { noColor = prev }()

	longFP := "abcdef0123456789deadbeefcafebabe"
	report := &engine.StatusReport{
		Rows: []engine.ResourceStatus{
			{Resource: "a", Path: "pa", State: engine.StateLinked, Fingerprint: longFP, Origin: config.OriginShared},
			{Resource: "b", Path: "pb", State: engine.StateLinked, Fingerprint: "", Origin: config.OriginShared},
		},
	}
	var buf bytes.Buffer
	if err := printStatus(&buf, report, false); err != nil {
		t.Fatalf("printStatus: %v", err)
	}
	out := buf.String()

	// The truncated prefix must appear; the untruncated suffix must not.
	prefix := longFP[:12]
	if !strings.Contains(out, prefix) {
		t.Errorf("expected fingerprint truncated to %q; output:\n%s", prefix, out)
	}
	if strings.Contains(out, longFP) {
		t.Errorf("fingerprint should not appear untruncated in row output; output:\n%s", out)
	}

	// Empty-fingerprint row renders "-", not blank.
	found := false
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "b") && strings.Contains(line, "pb") {
			if !strings.Contains(line, "-") {
				t.Errorf("empty fingerprint row missing '-' placeholder: %q", line)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("row for resource b not found; output:\n%s", out)
	}
}

// TestPrintStatusAllModeHasWorkspaceColumn pins that the --all
// variant swaps the header prefix to include WORKSPACE, and each row
// carries the workspace root as its first cell.
func TestPrintStatusAllModeHasWorkspaceColumn(t *testing.T) {
	prev := noColor
	noColor = true
	defer func() { noColor = prev }()

	report := &engine.StatusReport{
		Rows: []engine.ResourceStatus{
			{WorkspaceRoot: "/repos/main", Resource: "env", Path: ".env", State: engine.StateLinked, Origin: config.OriginShared},
			{WorkspaceRoot: "/repos/feature", Resource: "env", Path: ".env", State: engine.StateDetached, Origin: config.OriginShared},
		},
	}
	var buf bytes.Buffer
	if err := printStatus(&buf, report, true); err != nil {
		t.Fatalf("printStatus: %v", err)
	}
	out := buf.String()
	header := strings.SplitN(out, "\n", 2)[0]
	if !strings.HasPrefix(header, "WORKSPACE") {
		t.Errorf("--all header should start with WORKSPACE, got %q", header)
	}
	if !strings.Contains(out, "/repos/main") || !strings.Contains(out, "/repos/feature") {
		t.Errorf("--all rows should carry the workspace root; output:\n%s", out)
	}
}

// TestPrintWorkspacesFormatsCountsAndMarksCurrent pins the workspaces
// table shape: a header line, one row per workspace, the current one
// tagged with `*`, and the RESOURCES cell formatted by formatCounts.
func TestPrintWorkspacesFormatsCountsAndMarksCurrent(t *testing.T) {
	prev := noColor
	noColor = true
	defer func() { noColor = prev }()

	summaries := []engine.WorkspaceSummary{
		{
			Root:      "/proj/main",
			IsCurrent: true,
			State:     engine.WorkspaceLinked,
			Counts:    map[engine.State]int{engine.StateLinked: 2},
		},
		{
			Root:      "/proj/feature",
			IsCurrent: false,
			State:     engine.WorkspacePartial,
			Counts: map[engine.State]int{
				engine.StateLinked:   1,
				engine.StateDetached: 1,
			},
		},
	}

	var buf bytes.Buffer
	if err := printWorkspaces(&buf, summaries); err != nil {
		t.Fatalf("printWorkspaces: %v", err)
	}
	out := buf.String()

	header := strings.SplitN(out, "\n", 2)[0]
	for _, col := range []string{"WORKSPACE", "STATE", "RESOURCES"} {
		if !strings.Contains(header, col) {
			t.Errorf("header missing column %q; got %q", col, header)
		}
	}

	// Find the /proj/main and /proj/feature rows and check the marker
	// column: main starts with "* ", feature starts with "  ".
	var mainLine, featLine string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "/proj/main") {
			mainLine = line
		}
		if strings.Contains(line, "/proj/feature") {
			featLine = line
		}
	}
	if mainLine == "" || featLine == "" {
		t.Fatalf("missing rows; output:\n%s", out)
	}
	if !strings.HasPrefix(strings.TrimLeft(mainLine, " "), "*") {
		t.Errorf("current workspace row should begin with '*'; got %q", mainLine)
	}
	if strings.HasPrefix(strings.TrimLeft(featLine, " "), "*") {
		t.Errorf("non-current workspace row should not carry '*'; got %q", featLine)
	}

	// formatCounts: main → "2 linked"; feature → "1 linked, 1 detached".
	if !strings.Contains(mainLine, "2 linked") {
		t.Errorf("main row missing '2 linked'; got %q", mainLine)
	}
	if !strings.Contains(featLine, "1 linked, 1 detached") {
		t.Errorf("feature row missing '1 linked, 1 detached'; got %q", featLine)
	}
}

// TestFormatCountsStableOrder pins the healthy-first stable ordering
// so the workspaces table never flips the phrasing between runs even
// when the underlying map iterates in a different order.
func TestFormatCountsStableOrder(t *testing.T) {
	counts := map[engine.State]int{
		engine.StateConflict: 3,
		engine.StateLinked:   2,
		engine.StateDetached: 1,
		engine.StatePending:  4,
	}
	got := formatCounts(counts)
	// Order in the code: Linked, Expected, Detached, Pending, Missing,
	// NotLinked, Stale, Conflict, Absent.
	want := "2 linked, 1 detached, 4 pending, 3 conflict"
	if got != want {
		t.Errorf("formatCounts order mismatch\n got=%q\nwant=%q", got, want)
	}
}

// TestFormatCountsEmpty confirms an empty workspace renders "-", not
// a bare empty string: the RESOURCES column would otherwise look
// like a dropped cell.
func TestFormatCountsEmpty(t *testing.T) {
	if got := formatCounts(map[engine.State]int{}); got != "-" {
		t.Errorf("formatCounts(empty) = %q, want %q", got, "-")
	}
	if got := formatCounts(nil); got != "-" {
		t.Errorf("formatCounts(nil) = %q, want %q", got, "-")
	}
}

// TestPrintListSizeColumnToggle pins the --size flag's effect on the
// list header: off → no SIZE column, on → SIZE column present with
// humanSize-rendered values. Fingerprint status renders yes/no.
func TestPrintListSizeColumnToggle(t *testing.T) {
	rows := []engine.ResourceListing{
		{
			Resource:      "env",
			Path:          ".env",
			Fingerprinted: false,
			SharedPath:    "/store/env",
			Variants:      1,
			Size:          2048,
			Origin:        config.OriginShared,
		},
		{
			Resource:      "node_modules",
			Path:          "node_modules",
			Fingerprinted: true,
			SharedPath:    "/store/node_modules/abc",
			Variants:      3,
			Size:          10 * 1024,
			Origin:        config.OriginShared,
		},
	}

	t.Run("size off", func(t *testing.T) {
		var buf bytes.Buffer
		if err := printList(&buf, rows, false); err != nil {
			t.Fatalf("printList: %v", err)
		}
		out := buf.String()
		if strings.Contains(strings.SplitN(out, "\n", 2)[0], "SIZE") {
			t.Errorf("SIZE column should be absent when --size is off; header %q", out)
		}
		if !strings.Contains(out, "no") || !strings.Contains(out, "yes") {
			t.Errorf("FINGERPRINTED column should render yes/no; output:\n%s", out)
		}
	})

	t.Run("size on", func(t *testing.T) {
		var buf bytes.Buffer
		if err := printList(&buf, rows, true); err != nil {
			t.Fatalf("printList: %v", err)
		}
		out := buf.String()
		header := strings.SplitN(out, "\n", 2)[0]
		if !strings.Contains(header, "SIZE") {
			t.Errorf("SIZE column should be present when --size is on; header %q", header)
		}
		// humanSize(2048) → "2 KB", humanSize(10*1024) → "10 KB".
		if !strings.Contains(out, "2 KB") {
			t.Errorf("expected humanSize(2048)='2 KB' in output:\n%s", out)
		}
		if !strings.Contains(out, "10 KB") {
			t.Errorf("expected humanSize(10240)='10 KB' in output:\n%s", out)
		}
	})
}

// TestPrintListOriginColumnGated mirrors the status test: ORIGIN is
// only rendered when at least one listing carries a non-shared origin.
func TestPrintListOriginColumnGated(t *testing.T) {
	cases := []struct {
		name       string
		origins    []config.Origin
		wantColumn bool
	}{
		{"all shared", []config.Origin{config.OriginShared}, false},
		{"local mixed in", []config.Origin{config.OriginShared, config.OriginLocal}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rows := make([]engine.ResourceListing, len(c.origins))
			for i, o := range c.origins {
				rows[i] = engine.ResourceListing{
					Resource: "r", Path: "p", SharedPath: "s",
					Variants: 1, Origin: o,
				}
			}
			var buf bytes.Buffer
			if err := printList(&buf, rows, false); err != nil {
				t.Fatalf("printList: %v", err)
			}
			header := strings.SplitN(buf.String(), "\n", 2)[0]
			hasCol := strings.Contains(header, "ORIGIN")
			if hasCol != c.wantColumn {
				t.Fatalf("ORIGIN column present=%v, want=%v; header=%q",
					hasCol, c.wantColumn, header)
			}
		})
	}
}

// TestPrintStatusColoredAlignmentMatchesUncolored is the load-bearing
// regression test for the colored-STATE-column misalignment bug.
//
// Historically the code passed a raw ANSI-wrapped state cell to
// text/tabwriter. tabwriter counts ANSI escape bytes as visible width,
// so the STATE column measured 9 characters wider on colored rows
// than on the plain header row. On a real TTY, the header ORIGIN
// jumped 9 columns to the right of the data ORIGIN.
//
// The fix bypasses tabwriter for colored tables (writeAligned in
// table.go pairs each cell with a plain-text width). This test
// exercises the colored path and asserts every row's non-final
// column boundaries land at the SAME visible byte offset — the only
// way for columns to align on a terminal that renders ANSI escapes
// as zero-width control sequences.
func TestPrintStatusColoredAlignmentMatchesUncolored(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "")
	t.Setenv("CLICOLOR_FORCE", "1")

	prev := noColor
	noColor = false
	defer func() { noColor = prev }()

	report := &engine.StatusReport{
		Rows: []engine.ResourceStatus{
			{Resource: "env", Path: ".env", State: engine.StateLinked, Origin: config.OriginShared},
			{Resource: "node", Path: "node_modules", State: engine.StateNotLinked, Origin: config.OriginShared},
			{Resource: "envrc", Path: ".envrc", State: engine.StateExpected, Origin: config.OriginLocal},
		},
	}
	var buf bytes.Buffer
	if err := printStatus(&buf, report, false); err != nil {
		t.Fatalf("printStatus: %v", err)
	}

	if strings.ContainsRune(buf.String(), 0xff) {
		t.Fatalf("printStatus emitted a 0xff sentinel byte; those render as garbage on the terminal:\n%q", buf.String())
	}

	// Pin the expected stripped output byte-for-byte. Cells:
	//   RESOURCE=8, PATH=12 (node_modules), STATE=10 (not-linked),
	//   ORIGIN=6 (shared), FINGERPRINT=1 ("-").
	// Two-space separators between columns.
	ansi := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	got := ansi.ReplaceAllString(strings.TrimRight(buf.String(), "\n"), "")
	want := strings.Join([]string{
		"RESOURCE  PATH          STATE       ORIGIN  FINGERPRINT",
		"env       .env          linked      shared  -",
		"node      node_modules  not-linked  shared  -",
		"envrc     .envrc        expected    local   -",
	}, "\n")
	if got != want {
		t.Fatalf("stripped output mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestPrintWorkspacesColoredAlignmentMatchesUncolored is the workspaces
// twin: same class of bug, same test shape. `printWorkspaces` colors
// the STATE cell of every data row; the header STATE is plain.
func TestPrintWorkspacesColoredAlignmentMatchesUncolored(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "")
	t.Setenv("CLICOLOR_FORCE", "1")

	prev := noColor
	noColor = false
	defer func() { noColor = prev }()

	summaries := []engine.WorkspaceSummary{
		{Root: "/proj/main", State: engine.WorkspaceLinked, IsCurrent: true, Counts: map[engine.State]int{engine.StateLinked: 2}},
		{Root: "/proj/feature", State: engine.WorkspaceUnhealthy, Counts: map[engine.State]int{engine.StateConflict: 1, engine.StateLinked: 1}},
		{Root: "/proj/wip", State: engine.WorkspaceDetached, Counts: map[engine.State]int{engine.StateDetached: 2}},
	}
	var buf bytes.Buffer
	if err := printWorkspaces(&buf, summaries); err != nil {
		t.Fatalf("printWorkspaces: %v", err)
	}

	if strings.ContainsRune(buf.String(), 0xff) {
		t.Fatalf("printWorkspaces emitted a 0xff sentinel byte:\n%q", buf.String())
	}

	// Verify column boundaries land at consistent visible offsets by
	// checking that the STATE cell in every row starts at the same
	// column position after ANSI stripping. Column 0 max cell:
	//   "  /proj/feature" (15 chars); + 2 separator = STATE at 17.
	ansi := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 1+len(summaries) {
		t.Fatalf("expected %d lines, got %d:\n%s", 1+len(summaries), len(lines), buf.String())
	}

	stateStarts := []int{}
	stateCells := []string{"STATE", "linked", "unhealthy", "detached"}
	for i, line := range lines {
		plain := ansi.ReplaceAllString(line, "")
		idx := strings.Index(plain, stateCells[i])
		if idx < 0 {
			t.Fatalf("row %d (%q) does not contain expected state cell %q", i, plain, stateCells[i])
		}
		stateStarts = append(stateStarts, idx)
	}
	for i := 1; i < len(stateStarts); i++ {
		if stateStarts[i] != stateStarts[0] {
			stripped := make([]string, len(lines))
			for j, l := range lines {
				stripped[j] = ansi.ReplaceAllString(l, "")
			}
			t.Fatalf("STATE column starts at offset %d in header but %d in row %d\n%s",
				stateStarts[0], stateStarts[i], i, strings.Join(stripped, "\n"))
		}
	}
}
