package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/blaineventurine/wrk/internal/engine"
)

// TestWorkspacesJSONFlagRegistered pins the --json flag wiring for
// `wrk workspaces`. If the flag drifts, agents pairing with wrk lose
// their machine-readable path silently.
func TestWorkspacesJSONFlagRegistered(t *testing.T) {
	if workspacesCmd.Flags().Lookup("json") == nil {
		t.Fatal("--json flag not registered on workspacesCmd")
	}
}

// TestPrintWorkspacesJSONEmitsEnvelope confirms the JSON output
// carries the shared envelope (schema=1, kind="workspaces"), marks
// the workspace as primary, and terminates with a newline for
// shell-friendliness.
func TestPrintWorkspacesJSONEmitsEnvelope(t *testing.T) {
	summaries := []engine.WorkspaceSummary{
		{
			Root:      "/repo",
			IsCurrent: true,
			State:     engine.WorkspaceLinked,
			Counts:    map[engine.State]int{engine.StateLinked: 2},
		},
	}
	var buf bytes.Buffer
	if err := printWorkspacesJSON(&buf, summaries); err != nil {
		t.Fatalf("printWorkspacesJSON: %v", err)
	}
	if !bytes.HasSuffix(buf.Bytes(), []byte("\n")) {
		t.Errorf("output missing trailing newline:\n%s", buf.String())
	}

	var out struct {
		Schema     int    `json:"schema"`
		Kind       string `json:"kind"`
		Workspaces []struct {
			Root           string `json:"root"`
			IsPrimary      bool   `json:"isPrimary"`
			State          string `json:"state"`
			ResourceCounts struct {
				Linked int `json:"linked"`
			} `json:"resourceCounts"`
		} `json:"workspaces"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &out); err != nil {
		t.Fatalf("invalid JSON:\n%s\n%v", buf.String(), err)
	}
	if out.Schema != 1 || out.Kind != "workspaces" {
		t.Errorf("envelope wrong: schema=%d kind=%q", out.Schema, out.Kind)
	}
	if len(out.Workspaces) != 1 {
		t.Fatalf("expected 1 workspace, got %d", len(out.Workspaces))
	}
	w := out.Workspaces[0]
	if !w.IsPrimary {
		t.Errorf("workspace should be primary (IsCurrent=true)")
	}
	if w.State != "linked" {
		t.Errorf("state: got %q want linked", w.State)
	}
	if w.ResourceCounts.Linked != 2 {
		t.Errorf("linked: got %d want 2", w.ResourceCounts.Linked)
	}
}

// TestPrintWorkspacesJSONEmptySummaryEmitsEmptyArray pins the
// null-safety contract: an empty summary list still carries the
// envelope AND emits `workspaces: []` (not null) so consumers can
// iterate without a nil check.
func TestPrintWorkspacesJSONEmptySummaryEmitsEmptyArray(t *testing.T) {
	var buf bytes.Buffer
	if err := printWorkspacesJSON(&buf, nil); err != nil {
		t.Fatalf("printWorkspacesJSON(nil): %v", err)
	}
	if bytes.Contains(buf.Bytes(), []byte("null")) {
		t.Errorf("expected [] not null in output:\n%s", buf.String())
	}
}
