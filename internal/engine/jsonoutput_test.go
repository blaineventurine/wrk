package engine

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
)

// statusJSONMirror decodes MarshalStatusJSON output for assertions without
// coupling tests to the marshaller's own struct types.
type statusJSONMirror struct {
	Schema     int                        `json:"schema"`
	Kind       string                     `json:"kind"`
	Sources    []string                   `json:"sources"`
	Workspaces []statusJSONWorkspaceMirror `json:"workspaces"`
}

type statusJSONWorkspaceMirror struct {
	Root      string                     `json:"root"`
	IsPrimary bool                       `json:"isPrimary"`
	State     string                     `json:"state"`
	Resources []statusJSONResourceMirror `json:"resources"`
}

type statusJSONResourceMirror struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	State       string `json:"state"`
	Origin      string `json:"origin"`
	Fingerprint string `json:"fingerprint"`
	StoragePath string `json:"storagePath"`
}

func TestStatusJSONMarshalsEnvelope(t *testing.T) {
	report := &StatusReport{
		Sources: []string{".wrk.yml"},
		Rows: []ResourceStatus{
			{
				WorkspaceRoot: "/repo",
				Resource:      ".env",
				Path:          ".env",
				SharedPath:    "/store/env",
				Fingerprint:   "abc123",
				State:         StateLinked,
				Origin:        config.OriginShared,
			},
		},
	}

	data, err := MarshalStatusJSON(report, "/repo")
	if err != nil {
		t.Fatalf("MarshalStatusJSON: %v", err)
	}

	// Envelope must appear literally in the raw output. Pretty-printed
	// indented form is what json.MarshalIndent emits.
	if !strings.Contains(string(data), "\"schema\": 1") {
		t.Fatalf("schema envelope missing from output:\n%s", data)
	}
	if !strings.Contains(string(data), "\"kind\": \"status\"") {
		t.Fatalf("kind envelope missing from output:\n%s", data)
	}

	var got statusJSONMirror
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, data)
	}

	if got.Schema != 1 {
		t.Errorf("schema: got %d want 1", got.Schema)
	}
	if got.Kind != "status" {
		t.Errorf("kind: got %q want %q", got.Kind, "status")
	}
	if len(got.Sources) != 1 || got.Sources[0] != ".wrk.yml" {
		t.Errorf("sources: got %v want [.wrk.yml]", got.Sources)
	}
	if len(got.Workspaces) != 1 {
		t.Fatalf("workspaces: got %d want 1", len(got.Workspaces))
	}

	ws := got.Workspaces[0]
	if ws.Root != "/repo" {
		t.Errorf("workspace root: got %q want /repo", ws.Root)
	}
	if !ws.IsPrimary {
		t.Errorf("workspace isPrimary: got false, want true (root matches primaryRoot)")
	}
	if ws.State != "linked" {
		t.Errorf("workspace state: got %q want linked", ws.State)
	}
	if len(ws.Resources) != 1 {
		t.Fatalf("resources: got %d want 1", len(ws.Resources))
	}

	r := ws.Resources[0]
	if r.Name != ".env" {
		t.Errorf("resource name: got %q want .env", r.Name)
	}
	if r.Path != ".env" {
		t.Errorf("resource path: got %q want .env", r.Path)
	}
	if r.State != "linked" {
		t.Errorf("resource state: got %q want linked", r.State)
	}
	if r.Origin != "shared" {
		t.Errorf("resource origin: got %q want shared", r.Origin)
	}
	if r.Fingerprint != "abc123" {
		t.Errorf("resource fingerprint: got %q want abc123", r.Fingerprint)
	}
	if r.StoragePath != "/store/env" {
		t.Errorf("resource storagePath: got %q want /store/env", r.StoragePath)
	}
}

func TestStatusJSONMultipleWorkspacesGroupsRows(t *testing.T) {
	// Order is intentionally interleaved to prove first-seen grouping.
	report := &StatusReport{
		Sources: []string{".wrk.yml"},
		Rows: []ResourceStatus{
			{WorkspaceRoot: "/primary", Resource: "node_modules", State: StateLinked, Origin: config.OriginShared},
			{WorkspaceRoot: "/secondary", Resource: ".env", State: StateDetached, Origin: config.OriginShared},
			{WorkspaceRoot: "/primary", Resource: ".env", State: StateLinked, Origin: config.OriginShared},
			{WorkspaceRoot: "/secondary", Resource: "node_modules", State: StateDetached, Origin: config.OriginLocalOverride},
		},
	}

	data, err := MarshalStatusJSON(report, "/primary")
	if err != nil {
		t.Fatalf("MarshalStatusJSON: %v", err)
	}

	var got statusJSONMirror
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, data)
	}

	if len(got.Workspaces) != 2 {
		t.Fatalf("workspaces: got %d want 2", len(got.Workspaces))
	}

	// First-seen order: /primary appeared first in Rows.
	primary := got.Workspaces[0]
	secondary := got.Workspaces[1]

	if primary.Root != "/primary" {
		t.Errorf("first workspace root: got %q want /primary", primary.Root)
	}
	if !primary.IsPrimary {
		t.Errorf("primary workspace isPrimary: got false want true")
	}
	if primary.State != "linked" {
		t.Errorf("primary rollup: got %q want linked", primary.State)
	}

	if secondary.Root != "/secondary" {
		t.Errorf("second workspace root: got %q want /secondary", secondary.Root)
	}
	if secondary.IsPrimary {
		t.Errorf("secondary workspace isPrimary: got true want false")
	}
	if secondary.State != "detached" {
		t.Errorf("secondary rollup: got %q want detached", secondary.State)
	}

	// Resources inside a workspace are sorted by Name.
	if len(primary.Resources) != 2 {
		t.Fatalf("primary resources: got %d want 2", len(primary.Resources))
	}
	if primary.Resources[0].Name != ".env" || primary.Resources[1].Name != "node_modules" {
		t.Errorf("primary resources not sorted by name: %+v", primary.Resources)
	}
	if len(secondary.Resources) != 2 {
		t.Fatalf("secondary resources: got %d want 2", len(secondary.Resources))
	}
	if secondary.Resources[0].Name != ".env" || secondary.Resources[1].Name != "node_modules" {
		t.Errorf("secondary resources not sorted by name: %+v", secondary.Resources)
	}
	if secondary.Resources[1].Origin != "local-override" {
		t.Errorf("origin passthrough: got %q want local-override", secondary.Resources[1].Origin)
	}
}

func TestStatusJSONRollupStates(t *testing.T) {
	// rollupState covers the "empty" branch defensively; MarshalStatusJSON
	// never produces a workspace with zero resources given the current
	// grouping. Exercise it directly so the branch has coverage.
	if got := rollupState(nil); got != "empty" {
		t.Errorf("rollupState(nil): got %q want empty", got)
	}
	if got := rollupState([]State{}); got != "empty" {
		t.Errorf("rollupState([]): got %q want empty", got)
	}

	cases := []struct {
		name   string
		states []State
		want   string
	}{
		{"unhealthy_conflict", []State{StateLinked, StateConflict}, "unhealthy"},
		{"unhealthy_stale", []State{StateStale, StateLinked}, "unhealthy"},
		{"unhealthy_missing", []State{StateMissing}, "unhealthy"},
		{"unhealthy_not_linked", []State{StateNotLinked, StateDetached}, "unhealthy"},
		{"unhealthy_absent", []State{StateAbsent}, "unhealthy"},
		{"pending", []State{StateLinked, StatePending}, "pending"},
		{"linked_all_linked", []State{StateLinked, StateLinked}, "linked"},
		{"linked_mixes_expected", []State{StateLinked, StateExpected}, "linked"},
		{"detached_all", []State{StateDetached, StateDetached}, "detached"},
		{"partial_linked_and_detached", []State{StateLinked, StateDetached}, "partial"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows := make([]ResourceStatus, 0, len(tc.states))
			for i, s := range tc.states {
				rows = append(rows, ResourceStatus{
					WorkspaceRoot: "/repo",
					// Distinct names so the sort inside MarshalStatusJSON
					// keeps every state observable.
					Resource: string(rune('a' + i)),
					State:    s,
					Origin:   config.OriginShared,
				})
			}
			data, err := MarshalStatusJSON(&StatusReport{Rows: rows}, "/repo")
			if err != nil {
				t.Fatalf("MarshalStatusJSON: %v", err)
			}
			var got statusJSONMirror
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("unmarshal: %v\n%s", err, data)
			}
			if len(got.Workspaces) != 1 {
				t.Fatalf("workspaces: got %d want 1", len(got.Workspaces))
			}
			if got.Workspaces[0].State != tc.want {
				t.Errorf("state: got %q want %q", got.Workspaces[0].State, tc.want)
			}
		})
	}
}

func TestStatusJSONEmptyReport(t *testing.T) {
	data, err := MarshalStatusJSON(&StatusReport{}, "/repo")
	if err != nil {
		t.Fatalf("MarshalStatusJSON: %v", err)
	}

	var got statusJSONMirror
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, data)
	}

	if got.Schema != 1 {
		t.Errorf("schema: got %d want 1", got.Schema)
	}
	if got.Kind != "status" {
		t.Errorf("kind: got %q want status", got.Kind)
	}
	if len(got.Workspaces) != 0 {
		t.Errorf("workspaces: got %d want 0", len(got.Workspaces))
	}
	// Sources marshals as [] rather than null so downstream tools can
	// treat the field as an always-present array.
	if !strings.Contains(string(data), "\"sources\": []") {
		t.Errorf("expected empty sources array in output:\n%s", data)
	}
	if !strings.Contains(string(data), "\"workspaces\": []") {
		t.Errorf("expected empty workspaces array in output:\n%s", data)
	}
}

func TestStatusJSONDeterministicOrder(t *testing.T) {
	report := &StatusReport{
		Sources: []string{".wrk.yml", ".wrk.local.yml"},
		Rows: []ResourceStatus{
			{WorkspaceRoot: "/a", Resource: "z", Path: "z", State: StateLinked, Origin: config.OriginShared},
			{WorkspaceRoot: "/b", Resource: "m", Path: "m", State: StateDetached, Origin: config.OriginLocal},
			{WorkspaceRoot: "/a", Resource: "a", Path: "a", State: StateLinked, Origin: config.OriginShared, Fingerprint: "f"},
			{WorkspaceRoot: "/b", Resource: "n", Path: "n", State: StateDetached, Origin: config.OriginLocal},
		},
	}

	first, err := MarshalStatusJSON(report, "/a")
	if err != nil {
		t.Fatalf("MarshalStatusJSON first: %v", err)
	}
	// Repeat many times: any accidental map-iteration order dependency
	// tends to reveal itself under repetition rather than the first
	// hash-seed roll.
	for i := range 32 {
		next, err := MarshalStatusJSON(report, "/a")
		if err != nil {
			t.Fatalf("MarshalStatusJSON iter %d: %v", i, err)
		}
		if !bytes.Equal(first, next) {
			t.Fatalf("nondeterministic output on iter %d:\n---first---\n%s\n---next---\n%s", i, first, next)
		}
	}
}

func TestStatusJSONNilReportMarshalsEmpty(t *testing.T) {
	data, err := MarshalStatusJSON(nil, "/repo")
	if err != nil {
		t.Fatalf("MarshalStatusJSON(nil): %v", err)
	}
	var out statusJSONMirror
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, data)
	}
	if out.Schema != 1 || out.Kind != "status" {
		t.Errorf("envelope wrong: %+v", out)
	}
	if len(out.Workspaces) != 0 {
		t.Errorf("expected 0 workspaces, got %d", len(out.Workspaces))
	}
}

// TestRollupStatePriorityOrder pins the priority ordering directly: even
// when unhealthy, pending, linked and detached states all coexist, the
// rollup must resolve to "unhealthy" — the highest-priority category.
func TestRollupStatePriorityOrder(t *testing.T) {
	got := rollupState([]State{StateLinked, StateMissing, StatePending})
	if got != "unhealthy" {
		t.Errorf("priority: got %q want unhealthy", got)
	}
}

// ============================================================
// list — JSON output
// ============================================================

// listJSONMirror decodes MarshalListJSON output for assertions without
// coupling tests to the marshaller's own struct types.
type listJSONMirror struct {
	Schema    int                     `json:"schema"`
	Kind      string                  `json:"kind"`
	Root      string                  `json:"root"`
	Resources []listJSONResourceMirror `json:"resources"`
}

type listJSONResourceMirror struct {
	Name              string                  `json:"name"`
	Path              string                  `json:"path"`
	Fingerprinted     bool                    `json:"fingerprinted"`
	FingerprintInputs []string                `json:"fingerprintInputs"`
	Origin            string                  `json:"origin"`
	Variants          []listJSONVariantMirror `json:"variants"`
}

type listJSONVariantMirror struct {
	Fingerprint string   `json:"fingerprint"`
	StoragePath string   `json:"storagePath"`
	SizeBytes   int64    `json:"sizeBytes"`
	InUseBy     []string `json:"inUseBy"`
}

// TestListJSONMarshalsSchemaEnvelope pins the top-level envelope and
// per-resource projection: the schema/kind tag, repo root, and a
// resource row for each config entry — one fingerprinted, one plain.
func TestListJSONMarshalsSchemaEnvelope(t *testing.T) {
	repo := newTestRepoWithHead(t, nil)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: node\n"+
			"    path: node_modules\n"+
			"    fingerprint:\n"+
			"      - \"{root}/manifest.json\"\n"+
			"  - name: env\n"+
			"    path: .env\n",
	)
	writeFile(t, filepath.Join(repo.Root, "manifest.json"), `{"v":1}`)

	data, err := MarshalListJSON(repo, Options{StorageRoot: storage}, false)
	if err != nil {
		t.Fatalf("MarshalListJSON: %v", err)
	}
	if !strings.Contains(string(data), "\"schema\": 1") {
		t.Fatalf("schema envelope missing:\n%s", data)
	}
	if !strings.Contains(string(data), "\"kind\": \"list\"") {
		t.Fatalf("kind envelope missing:\n%s", data)
	}

	var got listJSONMirror
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, data)
	}
	if got.Schema != 1 || got.Kind != "list" {
		t.Errorf("envelope: schema=%d kind=%q", got.Schema, got.Kind)
	}
	if got.Root != repo.Root {
		t.Errorf("root: got %q want %q", got.Root, repo.Root)
	}
	if len(got.Resources) != 2 {
		t.Fatalf("resources: got %d want 2", len(got.Resources))
	}

	byName := map[string]listJSONResourceMirror{}
	for _, r := range got.Resources {
		byName[r.Name] = r
	}
	node, ok := byName["node"]
	if !ok {
		t.Fatalf("missing node resource: %+v", got.Resources)
	}
	if !node.Fingerprinted {
		t.Errorf("node should be fingerprinted")
	}
	if len(node.FingerprintInputs) != 1 || node.FingerprintInputs[0] != "{root}/manifest.json" {
		t.Errorf("node fingerprintInputs: got %v, want [\"{root}/manifest.json\"]",
			node.FingerprintInputs)
	}
	if node.Origin != "shared" {
		t.Errorf("node origin: got %q want shared", node.Origin)
	}

	env, ok := byName["env"]
	if !ok {
		t.Fatalf("missing env resource: %+v", got.Resources)
	}
	if env.Fingerprinted {
		t.Errorf("env should not be fingerprinted")
	}
	if len(env.FingerprintInputs) != 0 {
		t.Errorf("env fingerprintInputs: got %v want []", env.FingerprintInputs)
	}
}

// TestListJSONEnumeratesVariants pins the multi-variant surface: two
// fingerprint subdirectories on disk MUST both appear in the JSON
// output, sorted by fingerprint string.
func TestListJSONEnumeratesVariants(t *testing.T) {
	repo := newTestRepoWithHead(t, nil)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: node\n"+
			"    path: node_modules\n"+
			"    fingerprint:\n"+
			"      - \"{root}/manifest.json\"\n",
	)
	writeFile(t, filepath.Join(repo.Root, "manifest.json"), `{"v":1}`)

	// Fabricate two variant dirs directly on disk — MarshalListJSON
	// only reads the storage tree, so we do not need a real Link
	// invocation to exercise the enumerate path.
	base := filepath.Join(storage, repo.RepositoryID, "node_modules")
	for _, fp := range []string{"bbbbbbbbbbbbbbbb", "aaaaaaaaaaaaaaaa"} {
		if err := os.MkdirAll(filepath.Join(base, fp), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	data, err := MarshalListJSON(repo, Options{StorageRoot: storage}, false)
	if err != nil {
		t.Fatalf("MarshalListJSON: %v", err)
	}
	var got listJSONMirror
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, data)
	}
	if len(got.Resources) != 1 {
		t.Fatalf("resources: got %d want 1", len(got.Resources))
	}
	variants := got.Resources[0].Variants
	if len(variants) != 2 {
		t.Fatalf("variants: got %d want 2\n%s", len(variants), data)
	}
	// Sorted by fingerprint asc — 'a…' comes before 'b…'.
	if variants[0].Fingerprint != "aaaaaaaaaaaaaaaa" || variants[1].Fingerprint != "bbbbbbbbbbbbbbbb" {
		t.Errorf("variants not sorted by fingerprint: got %q, %q",
			variants[0].Fingerprint, variants[1].Fingerprint)
	}
	for _, v := range variants {
		if !filepath.IsAbs(v.StoragePath) {
			t.Errorf("variant storagePath must be absolute: %q", v.StoragePath)
		}
		if v.InUseBy == nil {
			t.Errorf("variant inUseBy must be an array, got nil for %q", v.Fingerprint)
		}
	}
}

// TestListJSONInUseByRespectsWorkspaceSymlinks pins the pin
// annotation: two workspaces both linking into the same variant MUST
// appear in that variant's inUseBy list (sorted), and a variant
// nothing links into MUST have an empty inUseBy — not null.
func TestListJSONInUseByRespectsWorkspaceSymlinks(t *testing.T) {
	primary := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n",
	})
	storage := storageIn(t, primary.Root)

	writeFile(t, filepath.Join(primary.Root, ".env"), "seed\n")
	if err := Link(primary, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Link primary: %v", err)
	}

	// Add a second workspace and Link it too — both symlinks resolve
	// to the same shared subtree (no fingerprint means single variant).
	_, secondary := addGitWorktree(t, primary, "ws2")
	if err := Link(secondary, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Link secondary: %v", err)
	}

	data, err := MarshalListJSON(primary, Options{StorageRoot: storage}, false)
	if err != nil {
		t.Fatalf("MarshalListJSON: %v", err)
	}
	var got listJSONMirror
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, data)
	}
	if len(got.Resources) != 1 {
		t.Fatalf("resources: got %d want 1", len(got.Resources))
	}
	variants := got.Resources[0].Variants
	if len(variants) != 1 {
		t.Fatalf("variants: got %d want 1\n%s", len(variants), data)
	}
	pins := variants[0].InUseBy
	if len(pins) != 2 {
		t.Fatalf("inUseBy: got %v want 2 workspaces\n%s", pins, data)
	}
	// Sorted ascending; the two workspace roots must both be present.
	if !sort.StringsAreSorted(pins) {
		t.Errorf("inUseBy not sorted: %v", pins)
	}
	want := map[string]bool{primary.Root: true, secondary.Root: true}
	for _, p := range pins {
		if !want[p] {
			t.Errorf("unexpected pin %q; want one of %v", p, want)
		}
	}
}

// TestListJSONSkipsBookkeepingSiblings pins the isBookkeeping filter:
// a real variant AND a `.wrk-lock` file and `.wrk-deleting/` sibling
// live in the storage subtree; only the real variant surfaces.
func TestListJSONSkipsBookkeepingSiblings(t *testing.T) {
	repo := newTestRepoWithHead(t, nil)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: node\n"+
			"    path: node_modules\n"+
			"    fingerprint:\n"+
			"      - \"{root}/manifest.json\"\n",
	)
	writeFile(t, filepath.Join(repo.Root, "manifest.json"), `{"v":1}`)

	base := filepath.Join(storage, repo.RepositoryID, "node_modules")
	if err := os.MkdirAll(filepath.Join(base, "5fd1d0d610ba6c17"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, ".wrk-lock"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, "5fd1d0d610ba6c17.wrk-deleting"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, "5fd1d0d610ba6c17.wrk-tmp"), 0o755); err != nil {
		t.Fatal(err)
	}

	data, err := MarshalListJSON(repo, Options{StorageRoot: storage}, false)
	if err != nil {
		t.Fatalf("MarshalListJSON: %v", err)
	}
	var got listJSONMirror
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, data)
	}
	variants := got.Resources[0].Variants
	if len(variants) != 1 {
		t.Fatalf("variants: got %d want 1 (bookkeeping siblings must be filtered)\n%s",
			len(variants), data)
	}
	if variants[0].Fingerprint != "5fd1d0d610ba6c17" {
		t.Errorf("variant fingerprint: got %q, want the non-bookkeeping subdir",
			variants[0].Fingerprint)
	}
}

// TestListJSONSizeBytesGatedByWithSize pins the withSize contract:
// sizeBytes is populated only when the caller passes withSize=true;
// otherwise the field is omitted from the JSON (`,omitempty` on 0).
func TestListJSONSizeBytesGatedByWithSize(t *testing.T) {
	repo := newTestRepoWithHead(t, nil)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: node\n"+
			"    path: node_modules\n"+
			"    fingerprint:\n"+
			"      - \"{root}/manifest.json\"\n",
	)
	writeFile(t, filepath.Join(repo.Root, "manifest.json"), `{"v":1}`)

	// A variant with a non-empty file so treeSize returns > 0.
	variant := filepath.Join(storage, repo.RepositoryID, "node_modules", "5fd1d0d610ba6c17")
	if err := os.MkdirAll(variant, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(variant, "marker"), []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// withSize=false: sizeBytes must be omitted from raw JSON.
	dataOff, err := MarshalListJSON(repo, Options{StorageRoot: storage}, false)
	if err != nil {
		t.Fatalf("MarshalListJSON(false): %v", err)
	}
	if strings.Contains(string(dataOff), "sizeBytes") {
		t.Errorf("sizeBytes must be omitted when withSize=false:\n%s", dataOff)
	}

	// withSize=true: sizeBytes must appear and be > 0.
	dataOn, err := MarshalListJSON(repo, Options{StorageRoot: storage}, true)
	if err != nil {
		t.Fatalf("MarshalListJSON(true): %v", err)
	}
	var got listJSONMirror
	if err := json.Unmarshal(dataOn, &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, dataOn)
	}
	if len(got.Resources) != 1 || len(got.Resources[0].Variants) != 1 {
		t.Fatalf("resources/variants shape: %+v", got.Resources)
	}
	if got.Resources[0].Variants[0].SizeBytes <= 0 {
		t.Errorf("sizeBytes = %d, want > 0 when withSize=true",
			got.Resources[0].Variants[0].SizeBytes)
	}
}

// TestListJSONUnfingerprintedSingleVariant pins the single-variant
// shape: a plain (no fingerprint) resource with a provisioned shared
// copy MUST surface one variant with an empty fingerprint field.
func TestListJSONUnfingerprintedSingleVariant(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n",
	})
	storage := storageIn(t, repo.Root)

	writeFile(t, filepath.Join(repo.Root, ".env"), "seed\n")

	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Link: %v", err)
	}

	data, err := MarshalListJSON(repo, Options{StorageRoot: storage}, false)
	if err != nil {
		t.Fatalf("MarshalListJSON: %v", err)
	}
	// The empty fingerprint field MUST appear in the raw output — the
	// contract is "emit an empty string, don't omit" so consumers can
	// distinguish `""` from a real digest.
	if !strings.Contains(string(data), "\"fingerprint\": \"\"") {
		t.Errorf("expected empty fingerprint field in output:\n%s", data)
	}
	var got listJSONMirror
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, data)
	}
	if len(got.Resources) != 1 {
		t.Fatalf("resources: got %d want 1", len(got.Resources))
	}
	if got.Resources[0].Fingerprinted {
		t.Errorf("expected fingerprinted=false for plain resource")
	}
	variants := got.Resources[0].Variants
	if len(variants) != 1 {
		t.Fatalf("variants: got %d want 1\n%s", len(variants), data)
	}
	if variants[0].Fingerprint != "" {
		t.Errorf("fingerprint: got %q, want empty string", variants[0].Fingerprint)
	}
	if len(variants[0].InUseBy) != 1 || variants[0].InUseBy[0] != repo.Root {
		t.Errorf("inUseBy: got %v, want [%q]", variants[0].InUseBy, repo.Root)
	}
}

// TestListJSONEmptyResourcesEmitsArray pins the "no null arrays"
// contract: a config with an empty resources list MUST render as
// `"resources": []` — never `null`. Consumers rely on this to iterate
// safely without a nil check.
func TestListJSONEmptyResourcesEmitsArray(t *testing.T) {
	repo := newTestRepoWithHead(t, nil)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, "resources: []\n")

	data, err := MarshalListJSON(repo, Options{StorageRoot: storage}, false)
	if err != nil {
		t.Fatalf("MarshalListJSON: %v", err)
	}
	if !strings.Contains(string(data), "\"resources\": []") {
		t.Errorf("expected `\"resources\": []`, got:\n%s", data)
	}
	if bytes.Contains(data, []byte("null")) {
		t.Errorf("output contains null:\n%s", data)
	}
}

// TestListJSONNilRepoReturnsError pins the input-validation contract:
// a nil repo is a programmer bug and MUST yield an error, never a
// runtime panic inside cfg.Load or repo.Workspaces.
func TestListJSONNilRepoReturnsError(t *testing.T) {
	if _, err := MarshalListJSON(nil, Options{}, false); err == nil {
		t.Fatal("expected error for nil repo, got nil")
	}
}
