package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/engine"
)

// TestPrintDoctorHumanHealthy pins the "healthy" branch: a report with
// no issues MUST close with "Overall: healthy" and MUST NOT list any
// bullet entries below the rollup line.
func TestPrintDoctorHumanHealthy(t *testing.T) {
	report := &engine.DoctorReport{
		Root: "/repo",
		VCS:  "git",
		Checks: engine.DoctorChecks{
			ConfigValid:      true,
			StorageSizeBytes: 1024,
		},
	}
	var buf bytes.Buffer
	if err := printDoctor(&buf, report); err != nil {
		t.Fatalf("printDoctor: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Overall: healthy") {
		t.Errorf("healthy output missing 'Overall: healthy':\n%s", out)
	}
	if strings.Contains(out, "issue(s)") {
		t.Errorf("healthy output should not mention issues:\n%s", out)
	}
	// The four fixed check lines MUST all appear so the human summary
	// is a stable table.
	for _, want := range []string{
		"Repository: /repo (git)",
		"Config:            valid",
		"Ghost workspaces:  none",
		"Bookkeeping cruft: none",
		"Storage size:      1 KB",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestPrintDoctorHumanWithIssues pins the "issues" branch: a report
// with a non-empty Issues slice MUST switch the rollup line to a
// count-suffixed "issue(s)" header and surface every issue as a bullet
// entry — plus the customary `wrk gc` hint the engine embeds.
func TestPrintDoctorHumanWithIssues(t *testing.T) {
	report := &engine.DoctorReport{
		Root: "/repo",
		VCS:  "git",
		Checks: engine.DoctorChecks{
			ConfigValid:     true,
			GhostWorkspaces: []string{"/ghost"},
		},
		Issues: []string{"1 ghost workspace(s) — run `wrk gc`"},
	}
	var buf bytes.Buffer
	if err := printDoctor(&buf, report); err != nil {
		t.Fatalf("printDoctor: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "issue(s)") {
		t.Errorf("issues output missing 'issue(s)':\n%s", out)
	}
	if !strings.Contains(out, "wrk gc") {
		t.Errorf("issues output missing `wrk gc` hint:\n%s", out)
	}
	if !strings.Contains(out, "- 1 ghost workspace(s)") {
		t.Errorf("issue bullet missing:\n%s", out)
	}
	if strings.Contains(out, "Overall: healthy") {
		t.Errorf("issues output should not claim 'healthy':\n%s", out)
	}
}

// TestPrintDoctorHumanInvalidConfig pins the config-invalid branch:
// a report whose config failed to load MUST surface the error message
// in the Config line — otherwise the user has no way to tell why the
// snapshot is unhealthy without re-running the failing command.
func TestPrintDoctorHumanInvalidConfig(t *testing.T) {
	report := &engine.DoctorReport{
		Root: "/repo",
		VCS:  "git",
		Checks: engine.DoctorChecks{
			ConfigValid: false,
			ConfigError: "missing field: name",
		},
		Issues: []string{"config invalid: missing field: name"},
	}
	var buf bytes.Buffer
	if err := printDoctor(&buf, report); err != nil {
		t.Fatalf("printDoctor: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Config:            invalid: missing field: name") {
		t.Errorf("invalid-config line missing:\n%s", out)
	}
}

// TestPrintDoctorHumanGhostsCollapse pins the listOrNone helper's
// multi-entry branch: two or more ghost workspaces collapse to a
// "<n> entries" summary so the check column stays narrow.
func TestPrintDoctorHumanGhostsCollapse(t *testing.T) {
	report := &engine.DoctorReport{
		Root: "/repo",
		VCS:  "git",
		Checks: engine.DoctorChecks{
			ConfigValid:     true,
			GhostWorkspaces: []string{"/ghost/a", "/ghost/b"},
		},
	}
	var buf bytes.Buffer
	if err := printDoctor(&buf, report); err != nil {
		t.Fatalf("printDoctor: %v", err)
	}
	if !strings.Contains(buf.String(), "Ghost workspaces:  2 entries") {
		t.Errorf("ghost workspaces did not collapse to '2 entries':\n%s", buf.String())
	}
}

// TestPrintDoctorJSONEmitsSchemaEnvelope pins the JSON path: the
// wrapper writes the schema/kind envelope and MUST terminate output
// with a trailing newline so shell pipelines don't have to fix it up.
func TestPrintDoctorJSONEmitsSchemaEnvelope(t *testing.T) {
	report := &engine.DoctorReport{
		Root: "/repo",
		VCS:  "git",
		Checks: engine.DoctorChecks{ConfigValid: true},
	}
	var buf bytes.Buffer
	if err := printDoctorJSON(&buf, report); err != nil {
		t.Fatalf("printDoctorJSON: %v", err)
	}
	if !bytes.HasSuffix(buf.Bytes(), []byte("\n")) {
		t.Errorf("missing trailing newline:\n%q", buf.String())
	}
	var out struct {
		Schema int    `json:"schema"`
		Kind   string `json:"kind"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if out.Schema != 1 || out.Kind != "doctor" {
		t.Errorf("envelope wrong: schema=%d kind=%q", out.Schema, out.Kind)
	}
}
