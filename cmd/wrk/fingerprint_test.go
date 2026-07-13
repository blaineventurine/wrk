package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/engine"
)

// TestPrintFingerprintHumanShowsMatch pins the "healthy" branch: when
// current and pinned digests agree, printFingerprint emits "matches
// current" and MUST NOT suggest `wrk link`.
func TestPrintFingerprintHumanShowsMatch(t *testing.T) {
	report := &engine.FingerprintReport{
		Resource: config.Resource{Name: "node", Path: "node_modules"},
		Current: engine.FingerprintSnapshot{
			Fingerprint: "abcd1234abcd1234",
			Inputs: []engine.FingerprintInput{
				{Path: "package.json", Exists: true, SizeBytes: 234},
			},
		},
		Pinned:  engine.FingerprintSnapshot{Fingerprint: "abcd1234abcd1234"},
		Changed: false,
	}
	var buf bytes.Buffer
	if err := printFingerprint(&buf, report); err != nil {
		t.Fatalf("printFingerprint: %v", err)
	}
	if !strings.Contains(buf.String(), "matches current") {
		t.Errorf("output missing 'matches current':\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "wrk link") {
		t.Errorf("unchanged output should not mention `wrk link`:\n%s", buf.String())
	}
}

// TestPrintFingerprintHumanShowsStale pins the "stale" branch: when
// current and pinned differ but both are populated, printFingerprint
// MUST call out "stale" and steer the user to `wrk link`.
func TestPrintFingerprintHumanShowsStale(t *testing.T) {
	report := &engine.FingerprintReport{
		Resource: config.Resource{Name: "node", Path: "node_modules"},
		Current: engine.FingerprintSnapshot{Fingerprint: "aaaa"},
		Pinned:  engine.FingerprintSnapshot{Fingerprint: "bbbb"},
		Changed: true,
	}
	var buf bytes.Buffer
	if err := printFingerprint(&buf, report); err != nil {
		t.Fatalf("printFingerprint: %v", err)
	}
	if !strings.Contains(buf.String(), "stale") {
		t.Errorf("expected 'stale' in output:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "wrk link") {
		t.Errorf("expected 'wrk link' hint in output:\n%s", buf.String())
	}
}

// TestPrintFingerprintHumanNoPinned pins the "not a symlink" branch:
// an empty Pinned surfaces as a dedicated note rather than showing an
// empty digest. The `wrk link` hint is intentionally suppressed here
// too — the workspace is opted out (detached, or fresh), not stale.
func TestPrintFingerprintHumanNoPinned(t *testing.T) {
	report := &engine.FingerprintReport{
		Resource: config.Resource{Name: "node", Path: "node_modules"},
		Current: engine.FingerprintSnapshot{Fingerprint: "aaaa"},
		Pinned:  engine.FingerprintSnapshot{},
		Changed: true,
	}
	var buf bytes.Buffer
	if err := printFingerprint(&buf, report); err != nil {
		t.Fatalf("printFingerprint: %v", err)
	}
	if !strings.Contains(buf.String(), "not a symlink") {
		t.Errorf("expected 'not a symlink' note:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "wrk link") {
		t.Errorf("empty pinned should not steer to `wrk link` (not stale, opted out):\n%s", buf.String())
	}
}

// TestPrintFingerprintJSONEmitsSchemaEnvelope pins the CLI wrapper's
// contract: printFingerprintJSON round-trips through the engine
// marshaller, appends a trailing newline for shell friendliness, and
// carries the shared schema/kind envelope.
func TestPrintFingerprintJSONEmitsSchemaEnvelope(t *testing.T) {
	report := &engine.FingerprintReport{
		Resource: config.Resource{Name: "node", Path: "node_modules"},
		Current: engine.FingerprintSnapshot{
			Fingerprint: "5fd1d0d610ba6c17",
			StoragePath: "/storage/x",
			Inputs: []engine.FingerprintInput{
				{Path: "package.json", Exists: true, SizeBytes: 234},
			},
		},
		Pinned:  engine.FingerprintSnapshot{Fingerprint: "5fd1d0d610ba6c17", StoragePath: "/storage/x"},
		Changed: false,
	}
	var buf bytes.Buffer
	if err := printFingerprintJSON(&buf, report); err != nil {
		t.Fatalf("printFingerprintJSON: %v", err)
	}
	if !bytes.HasSuffix(buf.Bytes(), []byte("\n")) {
		t.Errorf("missing trailing newline")
	}
	var out struct {
		Schema int    `json:"schema"`
		Kind   string `json:"kind"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if out.Schema != 1 || out.Kind != "fingerprint" {
		t.Errorf("envelope wrong: schema=%d kind=%q", out.Schema, out.Kind)
	}
}

// TestPrintFingerprintIsolatedNote pins the isolated branch of the
// human output: the pinned line must say the workspace holds a private
// variant (fingerprint comparison does not apply) and must NOT steer
// the user to `wrk link` — link skips isolated resources, so the hint
// would be wrong advice.
func TestPrintFingerprintIsolatedNote(t *testing.T) {
	report := &engine.FingerprintReport{
		Resource: config.Resource{Name: "node", Path: "node_modules"},
		Current:  engine.FingerprintSnapshot{Fingerprint: "aaaa"},
		Pinned:   engine.FingerprintSnapshot{StoragePath: "/storage/node_modules/isolated-abc123"},
		Changed:  false,
		Isolated: true,
	}
	var buf bytes.Buffer
	if err := printFingerprint(&buf, report); err != nil {
		t.Fatalf("printFingerprint: %v", err)
	}
	if !strings.Contains(buf.String(), "isolated") {
		t.Errorf("expected isolated note:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "wrk link") {
		t.Errorf("isolated resource must not steer to `wrk link` (link skips it):\n%s", buf.String())
	}
}
