package engine

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/planner"
)

// statusJSONMirror decodes MarshalStatusJSON output for assertions without
// coupling tests to the marshaller's own struct types.
type statusJSONMirror struct {
	Schema     int                         `json:"schema"`
	Kind       string                      `json:"kind"`
	Sources    []string                    `json:"sources"`
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
	Schema    int                      `json:"schema"`
	Kind      string                   `json:"kind"`
	Root      string                   `json:"root"`
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
	Isolated    bool     `json:"isolated"`
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

// TestListJSONMarksIsolatedVariants pins the isolated labeling: an
// isolated-<hex> directory in the resource subtree is a per-workspace
// private variant, not a fingerprint variant. It must surface with
// isolated=true and fingerprint="" — emitting the random suffix as a
// "fingerprint" would be semantically wrong and break consumers that
// join on digests. Fingerprint variants keep isolated=false.
func TestListJSONMarksIsolatedVariants(t *testing.T) {
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
	for _, dir := range []string{"aaaaaaaaaaaaaaaa", "isolated-abc123def456"} {
		if err := os.MkdirAll(filepath.Join(base, dir), 0o755); err != nil {
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

	// Isolated sorts first (empty fingerprint < any digest).
	iso, fp := variants[0], variants[1]
	if !iso.Isolated {
		t.Errorf("isolated variant: isolated = false, want true\n%s", data)
	}
	if iso.Fingerprint != "" {
		t.Errorf("isolated variant: fingerprint = %q, want empty — the dir name is a random suffix",
			iso.Fingerprint)
	}
	if !strings.Contains(iso.StoragePath, "isolated-abc123def456") {
		t.Errorf("isolated variant storagePath = %q, want the isolated dir", iso.StoragePath)
	}
	if fp.Isolated {
		t.Errorf("fingerprint variant: isolated = true, want false\n%s", data)
	}
	if fp.Fingerprint != "aaaaaaaaaaaaaaaa" {
		t.Errorf("fingerprint variant: fingerprint = %q, want aaaaaaaaaaaaaaaa", fp.Fingerprint)
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

// ============================================================
// fingerprint — JSON output
// ============================================================

// TestFingerprintJSONMarshalsEnvelope pins the top-level envelope and
// per-input projection: schema/kind tag, resource identity, both
// snapshots populated, and every declared input surfacing with its
// Path/Exists/SizeBytes trio.
func TestFingerprintJSONMarshalsEnvelope(t *testing.T) {
	report := &FingerprintReport{
		Resource: config.Resource{Name: "node", Path: "node_modules"},
		Current: FingerprintSnapshot{
			Fingerprint: "5fd1d0d610ba6c17",
			StoragePath: "/storage/repo/node_modules/5fd1d0d610ba6c17",
			Inputs: []FingerprintInput{
				{Path: "package.json", Exists: true, SizeBytes: 234},
				{Path: "yarn.lock", Exists: true, SizeBytes: 45678},
			},
		},
		Pinned: FingerprintSnapshot{
			Fingerprint: "8a71d8b219fd0031",
			StoragePath: "/storage/repo/node_modules/8a71d8b219fd0031",
		},
		Changed: true,
	}
	data, err := MarshalFingerprintJSON(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out struct {
		Schema   int    `json:"schema"`
		Kind     string `json:"kind"`
		Resource struct {
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"resource"`
		Current struct {
			Fingerprint string `json:"fingerprint"`
			StoragePath string `json:"storagePath"`
			Inputs      []struct {
				Path      string `json:"path"`
				Exists    bool   `json:"exists"`
				SizeBytes int64  `json:"sizeBytes"`
			} `json:"inputs"`
		} `json:"current"`
		Pinned struct {
			Fingerprint string `json:"fingerprint"`
			StoragePath string `json:"storagePath"`
		} `json:"pinned"`
		Changed bool `json:"changed"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, data)
	}
	if out.Schema != 1 || out.Kind != "fingerprint" {
		t.Errorf("envelope wrong: schema=%d kind=%q", out.Schema, out.Kind)
	}
	if out.Resource.Name != "node" || out.Resource.Path != "node_modules" {
		t.Errorf("resource wrong: %+v", out.Resource)
	}
	if out.Current.Fingerprint != "5fd1d0d610ba6c17" {
		t.Errorf("current.fingerprint = %q", out.Current.Fingerprint)
	}
	if out.Pinned.Fingerprint != "8a71d8b219fd0031" {
		t.Errorf("pinned.fingerprint = %q", out.Pinned.Fingerprint)
	}
	if out.Changed != true {
		t.Errorf("changed = %v, want true", out.Changed)
	}
	if len(out.Current.Inputs) != 2 {
		t.Fatalf("inputs = %d, want 2", len(out.Current.Inputs))
	}
	if out.Current.Inputs[0].Path != "package.json" {
		t.Errorf("input[0].path = %q", out.Current.Inputs[0].Path)
	}
	if out.Current.Inputs[1].SizeBytes != 45678 {
		t.Errorf("input[1].sizeBytes = %d", out.Current.Inputs[1].SizeBytes)
	}
}

// TestFingerprintJSONOmitsEmptyPinned pins the omitempty contract on
// pinned.fingerprint / pinned.storagePath: a workspace whose path is
// not a symlink into shared storage has both fields empty, and the
// JSON output MUST elide the keys entirely rather than emit `""`.
func TestFingerprintJSONOmitsEmptyPinned(t *testing.T) {
	report := &FingerprintReport{
		Resource: config.Resource{Name: "node", Path: "node_modules"},
		Current: FingerprintSnapshot{
			Fingerprint: "5fd1d0d610ba6c17",
			StoragePath: "/storage/repo/node_modules/5fd1d0d610ba6c17",
		},
		Pinned:  FingerprintSnapshot{},
		Changed: true,
	}
	data, err := MarshalFingerprintJSON(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(data, []byte(`"fingerprint": ""`)) {
		t.Errorf("expected omitempty to elide empty fingerprint, got:\n%s", data)
	}
	if bytes.Contains(data, []byte(`"storagePath": ""`)) {
		t.Errorf("expected omitempty to elide empty storagePath, got:\n%s", data)
	}
	// Round-trip: pinned decodes cleanly to a zero-value struct.
	var out struct {
		Pinned struct {
			Fingerprint string `json:"fingerprint"`
			StoragePath string `json:"storagePath"`
		} `json:"pinned"`
		Changed bool `json:"changed"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, data)
	}
	if out.Pinned.Fingerprint != "" || out.Pinned.StoragePath != "" {
		t.Errorf("pinned decoded non-empty: %+v", out.Pinned)
	}
	if !out.Changed {
		t.Errorf("changed = %v, want true", out.Changed)
	}
}

// TestFingerprintJSONNilReportErrors pins the input-validation
// contract: a nil report is a programmer bug and MUST yield an error
// rather than silently emit a zero-value envelope.
func TestFingerprintJSONNilReportErrors(t *testing.T) {
	if _, err := MarshalFingerprintJSON(nil); err == nil {
		t.Fatal("expected error for nil report")
	}
}

// ============================================================
// doctor — JSON output
// ============================================================

// TestDoctorJSONMarshalsEnvelope pins the top-level envelope and the
// "no null arrays" contract: even a healthy report — one with nil
// slices for every check — MUST marshal every string-slice field as
// `[]` so consumers can iterate without a nil check.
func TestDoctorJSONMarshalsEnvelope(t *testing.T) {
	report := &DoctorReport{
		Root:         "/repo",
		RepositoryID: "local/abc123",
		VCS:          "git",
		Checks: DoctorChecks{
			ConfigValid:      true,
			StorageSizeBytes: 1024,
		},
	}
	data, err := MarshalDoctorJSON(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out struct {
		Schema int    `json:"schema"`
		Kind   string `json:"kind"`
		Root   string `json:"root"`
		Checks struct {
			ConfigValid       bool     `json:"configValid"`
			GhostWorkspaces   []string `json:"ghostWorkspaces"`
			OrphanedLocks     []string `json:"orphanedLocks"`
			StaleProvisioning []string `json:"staleProvisioning"`
			StaleDeleting     []string `json:"staleDeleting"`
			StaleForgetting   []string `json:"staleForgetting"`
			StorageSizeBytes  int64    `json:"storageSizeBytes"`
		} `json:"checks"`
		Issues []string `json:"issues"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, data)
	}
	if out.Schema != 1 || out.Kind != "doctor" {
		t.Errorf("envelope wrong: schema=%d kind=%q", out.Schema, out.Kind)
	}
	if out.Root != "/repo" {
		t.Errorf("root wrong: %q", out.Root)
	}
	if !out.Checks.ConfigValid {
		t.Error("ConfigValid: got false, want true")
	}
	if out.Checks.StorageSizeBytes != 1024 {
		t.Errorf("StorageSizeBytes: got %d, want 1024", out.Checks.StorageSizeBytes)
	}
	// Never-null arrays: every slice field MUST decode to a non-nil,
	// empty slice — not nil.
	for name, got := range map[string][]string{
		"GhostWorkspaces":   out.Checks.GhostWorkspaces,
		"OrphanedLocks":     out.Checks.OrphanedLocks,
		"StaleProvisioning": out.Checks.StaleProvisioning,
		"StaleDeleting":     out.Checks.StaleDeleting,
		"StaleForgetting":   out.Checks.StaleForgetting,
		"Issues":            out.Issues,
	} {
		if got == nil {
			t.Errorf("%s: nil, want []", name)
		}
	}
}

// TestDoctorJSONOmitsConfigErrorWhenValid pins the omitempty contract
// on configError: a healthy config MUST NOT surface an empty
// configError string — the key's absence is itself the signal.
func TestDoctorJSONOmitsConfigErrorWhenValid(t *testing.T) {
	report := &DoctorReport{Checks: DoctorChecks{ConfigValid: true}}
	data, err := MarshalDoctorJSON(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(data, []byte(`"configError"`)) {
		t.Errorf("omitempty broken — configError present for valid config:\n%s", data)
	}
}

// TestDoctorJSONIncludesConfigErrorWhenInvalid pins the inverse: a
// broken config MUST surface the underlying error message verbatim so
// consumers can machine-dispatch on it.
func TestDoctorJSONIncludesConfigErrorWhenInvalid(t *testing.T) {
	report := &DoctorReport{Checks: DoctorChecks{ConfigError: "missing field: name"}}
	data, err := MarshalDoctorJSON(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(data, []byte(`"configError": "missing field: name"`)) {
		t.Errorf("configError missing:\n%s", data)
	}
}

// TestDoctorJSONPopulatesSliceChecks pins the round-trip contract on
// populated slices: entries added to any check MUST surface verbatim
// in the JSON output.
func TestDoctorJSONPopulatesSliceChecks(t *testing.T) {
	report := &DoctorReport{
		Checks: DoctorChecks{
			ConfigValid:       true,
			GhostWorkspaces:   []string{"/ghost/a", "/ghost/b"},
			OrphanedLocks:     []string{"/lock/a"},
			StaleProvisioning: []string{"/tmp/a"},
			StaleDeleting:     []string{"/del/a"},
			StaleForgetting:   []string{"/fgt/a"},
		},
		Issues: []string{"2 ghost workspace(s) — run `wrk gc`"},
	}
	data, err := MarshalDoctorJSON(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out struct {
		Checks struct {
			GhostWorkspaces   []string `json:"ghostWorkspaces"`
			OrphanedLocks     []string `json:"orphanedLocks"`
			StaleProvisioning []string `json:"staleProvisioning"`
			StaleDeleting     []string `json:"staleDeleting"`
			StaleForgetting   []string `json:"staleForgetting"`
		} `json:"checks"`
		Issues []string `json:"issues"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, data)
	}
	if got, want := out.Checks.GhostWorkspaces, []string{"/ghost/a", "/ghost/b"}; !slicesEqual(got, want) {
		t.Errorf("GhostWorkspaces: got %v want %v", got, want)
	}
	if got, want := out.Checks.OrphanedLocks, []string{"/lock/a"}; !slicesEqual(got, want) {
		t.Errorf("OrphanedLocks: got %v want %v", got, want)
	}
	if got, want := out.Checks.StaleProvisioning, []string{"/tmp/a"}; !slicesEqual(got, want) {
		t.Errorf("StaleProvisioning: got %v want %v", got, want)
	}
	if got, want := out.Checks.StaleDeleting, []string{"/del/a"}; !slicesEqual(got, want) {
		t.Errorf("StaleDeleting: got %v want %v", got, want)
	}
	if got, want := out.Checks.StaleForgetting, []string{"/fgt/a"}; !slicesEqual(got, want) {
		t.Errorf("StaleForgetting: got %v want %v", got, want)
	}
	if got, want := out.Issues, []string{"2 ghost workspace(s) — run `wrk gc`"}; !slicesEqual(got, want) {
		t.Errorf("Issues: got %v want %v", got, want)
	}
}

// TestDoctorJSONNilReportErrors pins the input-validation contract:
// a nil report is a programmer bug and MUST yield an error rather
// than silently emit a zero-value envelope.
func TestDoctorJSONNilReportErrors(t *testing.T) {
	if _, err := MarshalDoctorJSON(nil); err == nil {
		t.Fatal("expected error for nil report")
	}
}

// ============================================================
// destructive-command marshaler tests
// ============================================================
//
// Every destructive command's marshaler shares the same envelope
// contract: `schema`, `kind`, `dryRun`, `plan`, and an optional
// `result` pointer whose omit-when-nil behaviour these tests pin
// exhaustively.
//
// Uses a raw json.RawMessage for `result` so tests can distinguish
// "key missing" (dry-run / refused) from "key present with a value"
// (executed) without coupling to the marshaler's own struct type.

// destructiveEnvelopeMirror decodes the shared envelope for the plan+
// result destructive commands. RawResult stays as raw bytes so tests
// can distinguish "result absent" from "result null" — the omitempty
// pointer contract requires the key to disappear entirely on a
// dry-run / refused execution.
type destructiveEnvelopeMirror struct {
	Schema    int             `json:"schema"`
	Kind      string          `json:"kind"`
	DryRun    bool            `json:"dryRun"`
	RawResult json.RawMessage `json:"result"`
}

// resultMirror decodes the result envelope shared by every
// destructive command. Kept flat and untyped so tests can assert on
// the exact wire shape without importing the marshaler's own struct.
type resultMirror struct {
	Attempted  bool     `json:"attempted"`
	BytesFreed int64    `json:"bytesFreed"`
	Warnings   []string `json:"warnings"`
}

// ============================================================
// workspaces
// ============================================================

// TestWorkspacesJSONEmitsEnvelope pins the schema+kind envelope
// for `wrk workspaces --json`: an empty summary list must still
// carry the envelope AND emit `workspaces: []` (not null) so
// consumers can iterate without a nil check.
func TestWorkspacesJSONEmitsEnvelope(t *testing.T) {
	data, err := MarshalWorkspacesJSON(nil)
	if err != nil {
		t.Fatalf("MarshalWorkspacesJSON(nil): %v", err)
	}
	if !strings.Contains(string(data), "\"kind\": \"workspaces\"") {
		t.Fatalf("kind envelope missing:\n%s", data)
	}
	if !strings.Contains(string(data), "\"workspaces\": []") {
		t.Fatalf("expected `workspaces: []`, got:\n%s", data)
	}

	var out struct {
		Schema     int `json:"schema"`
		Workspaces []struct {
			Root string `json:"root"`
		} `json:"workspaces"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Schema != 1 {
		t.Errorf("schema: got %d want 1", out.Schema)
	}
	if out.Workspaces == nil {
		t.Errorf("workspaces MUST unmarshal to a non-nil slice")
	}
}

// TestWorkspacesJSONProjectsCounts pins the resourceCounts rollup:
// every configured state maps to a fixed field, and Unhealthy
// aggregates every state that would drive a WorkspaceUnhealthy
// rollup (conflict + stale + missing + not-linked + absent).
func TestWorkspacesJSONProjectsCounts(t *testing.T) {
	summaries := []WorkspaceSummary{
		{
			Root:      "/primary",
			IsCurrent: true,
			State:     WorkspaceUnhealthy,
			Counts: map[State]int{
				StateLinked:    2,
				StateDetached:  1,
				StateIsolated:  1,
				StatePending:   1,
				StateExpected:  1,
				StateConflict:  1,
				StateStale:     1,
				StateMissing:   1,
				StateNotLinked: 1,
				StateAbsent:    1,
			},
		},
	}

	data, err := MarshalWorkspacesJSON(summaries)
	if err != nil {
		t.Fatalf("MarshalWorkspacesJSON: %v", err)
	}
	var out struct {
		Workspaces []struct {
			Root           string `json:"root"`
			IsPrimary      bool   `json:"isPrimary"`
			State          string `json:"state"`
			ResourceCounts struct {
				Linked    int `json:"linked"`
				Detached  int `json:"detached"`
				Isolated  int `json:"isolated"`
				Pending   int `json:"pending"`
				Unhealthy int `json:"unhealthy"`
				Expected  int `json:"expected"`
			} `json:"resourceCounts"`
		} `json:"workspaces"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Workspaces) != 1 {
		t.Fatalf("workspaces: got %d want 1", len(out.Workspaces))
	}
	w := out.Workspaces[0]
	if w.Root != "/primary" {
		t.Errorf("root: got %q want /primary", w.Root)
	}
	if !w.IsPrimary {
		t.Errorf("isPrimary: got false want true (IsCurrent=true)")
	}
	if w.State != "unhealthy" {
		t.Errorf("state: got %q want unhealthy", w.State)
	}
	if w.ResourceCounts.Linked != 2 {
		t.Errorf("linked: got %d want 2", w.ResourceCounts.Linked)
	}
	if w.ResourceCounts.Detached != 1 {
		t.Errorf("detached: got %d want 1", w.ResourceCounts.Detached)
	}
	if w.ResourceCounts.Isolated != 1 {
		t.Errorf("isolated: got %d want 1", w.ResourceCounts.Isolated)
	}
	if w.ResourceCounts.Pending != 1 {
		t.Errorf("pending: got %d want 1", w.ResourceCounts.Pending)
	}
	if w.ResourceCounts.Expected != 1 {
		t.Errorf("expected: got %d want 1", w.ResourceCounts.Expected)
	}
	// Unhealthy = conflict+stale+missing+notLinked+absent = 5
	if w.ResourceCounts.Unhealthy != 5 {
		t.Errorf("unhealthy: got %d want 5 (sum of 5 unhealthy states)",
			w.ResourceCounts.Unhealthy)
	}
}

// ============================================================
// gc
// ============================================================

// TestGCJSONDryRunOmitsResult pins the omitempty contract: a
// --dry-run marshal MUST NOT carry a `result` key at all (nil
// pointer = key elided) so consumers can dispatch on "was the
// executor invoked?" purely by key presence.
func TestGCJSONDryRunOmitsResult(t *testing.T) {
	plan := GCPlan{
		Ghosts: []string{"/g/1"},
		DeleteVariants: []variant{{
			Resource:    "node",
			Path:        "node_modules",
			Fingerprint: "abc",
			StoragePath: "/store/abc",
			Size:        4096,
		}},
		TotalBytesFreed: 4096,
	}

	data, err := MarshalGCJSON(GCJSONInput{Plan: plan, DryRun: true})
	if err != nil {
		t.Fatalf("MarshalGCJSON: %v", err)
	}

	var env destructiveEnvelopeMirror
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v\n%s", err, data)
	}
	if env.Schema != 1 || env.Kind != "gc" {
		t.Errorf("envelope: schema=%d kind=%q, want 1/gc", env.Schema, env.Kind)
	}
	if !env.DryRun {
		t.Errorf("dryRun: got false want true")
	}
	if len(env.RawResult) != 0 {
		t.Errorf("result MUST be omitted in dry-run mode, got %s",
			string(env.RawResult))
	}

	// Plan fields land in the payload.
	var full struct {
		Plan struct {
			VariantsToRemove       []gcVariantJSON `json:"variantsToRemove"`
			GhostWorkspacesToPrune []string        `json:"ghostWorkspacesToPrune"`
			TotalBytesToFree       int64           `json:"totalBytesToFree"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(data, &full); err != nil {
		t.Fatalf("unmarshal plan: %v", err)
	}
	if len(full.Plan.VariantsToRemove) != 1 || full.Plan.VariantsToRemove[0].Resource != "node" {
		t.Errorf("variantsToRemove: %+v", full.Plan.VariantsToRemove)
	}
	if len(full.Plan.GhostWorkspacesToPrune) != 1 || full.Plan.GhostWorkspacesToPrune[0] != "/g/1" {
		t.Errorf("ghostWorkspacesToPrune: %+v", full.Plan.GhostWorkspacesToPrune)
	}
	if full.Plan.TotalBytesToFree != 4096 {
		t.Errorf("totalBytesToFree: got %d want 4096", full.Plan.TotalBytesToFree)
	}
}

// TestGCJSONExecutePopulatesResult pins the executed branch: with
// Attempted=true the marshaler MUST carry a non-nil `result` object
// whose Attempted / BytesFreed / Warnings mirror the input.
func TestGCJSONExecutePopulatesResult(t *testing.T) {
	plan := GCPlan{}
	data, err := MarshalGCJSON(GCJSONInput{
		Plan:       plan,
		Attempted:  true,
		BytesFreed: 1234567,
		Warnings:   []string{"warning: skipped foo"},
	})
	if err != nil {
		t.Fatalf("MarshalGCJSON: %v", err)
	}

	var env destructiveEnvelopeMirror
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if len(env.RawResult) == 0 {
		t.Fatalf("result MUST be present when Attempted=true")
	}

	var res resultMirror
	if err := json.Unmarshal(env.RawResult, &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !res.Attempted {
		t.Errorf("attempted: got false want true")
	}
	if res.BytesFreed != 1234567 {
		t.Errorf("bytesFreed: got %d want 1234567", res.BytesFreed)
	}
	if len(res.Warnings) != 1 || res.Warnings[0] != "warning: skipped foo" {
		t.Errorf("warnings: %+v", res.Warnings)
	}
}

// TestGCJSONEmptyPlanEmitsNullSafeArrays pins that every plan slice
// serialises as `[]` even when the underlying GCPlan carries nil
// slices — consumers iterate without a nil check.
func TestGCJSONEmptyPlanEmitsNullSafeArrays(t *testing.T) {
	data, err := MarshalGCJSON(GCJSONInput{Plan: GCPlan{}, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	// Grep for each slice field explicitly — decoded nil vs empty are
	// indistinguishable in Go, but the raw JSON bytes MUST show `[]`.
	for _, field := range []string{
		"\"variantsToRemove\": []",
		"\"ghostWorkspacesToPrune\": []",
		"\"orphanedLocks\": []",
		"\"staleProvisioning\": []",
		"\"staleDeleting\": []",
		"\"staleForgetting\": []",
		"\"pendingSwaps\": []",
		"\"orphanedIsolationEntries\": []",
		"\"orphanRegistry\": []",
		"\"unreachableWorkspaces\": []",
	} {
		if !strings.Contains(string(data), field) {
			t.Errorf("missing empty-array field %q in:\n%s", field, data)
		}
	}
}

// TestGCJSONExecutedNilWarningsSerializeAsEmptyArray pins the
// warnings-array contract on the result envelope: even when the
// caller passes nil, the wire form MUST be `[]` so consumers can
// iterate without a nil check.
func TestGCJSONExecutedNilWarningsSerializeAsEmptyArray(t *testing.T) {
	data, err := MarshalGCJSON(GCJSONInput{Plan: GCPlan{}, Attempted: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\"warnings\": []") {
		t.Errorf("warnings MUST serialize as []:\n%s", data)
	}
}

// ============================================================
// remove
// ============================================================

// TestRemoveJSONDryRunOmitsResult pins the shared envelope shape
// for `wrk remove --json --dry-run`: schema=1, kind=remove, no
// result key.
func TestRemoveJSONDryRunOmitsResult(t *testing.T) {
	plan := RemovePlan{
		Target:             "/wk/feature",
		Backend:            "git",
		VCSCommand:         "git worktree remove /wk/feature",
		UncommittedChanges: 2,
		DetachedPaths:      []string{".env"},
		TotalBytes:         512,
		Refusal:            "workspace has uncommitted changes",
		IsGhost:            false,
	}

	data, err := MarshalRemoveJSON(RemoveJSONInput{Plan: plan, DryRun: true})
	if err != nil {
		t.Fatalf("MarshalRemoveJSON: %v", err)
	}
	var env destructiveEnvelopeMirror
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	if env.Schema != 1 || env.Kind != "remove" || !env.DryRun {
		t.Errorf("envelope: %+v", env)
	}
	if len(env.RawResult) != 0 {
		t.Errorf("result MUST be omitted in dry-run mode")
	}

	var full struct {
		Plan removePlanJSON `json:"plan"`
	}
	if err := json.Unmarshal(data, &full); err != nil {
		t.Fatal(err)
	}
	if full.Plan.Target != "/wk/feature" {
		t.Errorf("target: got %q", full.Plan.Target)
	}
	if full.Plan.Backend != "git" {
		t.Errorf("backend: got %q", full.Plan.Backend)
	}
	if full.Plan.UncommittedChanges != 2 {
		t.Errorf("uncommittedChanges: got %d", full.Plan.UncommittedChanges)
	}
	if len(full.Plan.DetachedPaths) != 1 || full.Plan.DetachedPaths[0] != ".env" {
		t.Errorf("detachedPaths: %+v", full.Plan.DetachedPaths)
	}
	if full.Plan.TotalBytesToFree != 512 {
		t.Errorf("totalBytesToFree: got %d", full.Plan.TotalBytesToFree)
	}
	if full.Plan.Refusal != "workspace has uncommitted changes" {
		t.Errorf("refusal: got %q", full.Plan.Refusal)
	}
}

// TestRemoveJSONExecutePopulatesResult pins the executed branch:
// Attempted=true carries BytesFreed and any Warnings.
func TestRemoveJSONExecutePopulatesResult(t *testing.T) {
	data, err := MarshalRemoveJSON(RemoveJSONInput{
		Plan:       RemovePlan{Target: "/wk/f", Backend: "jj"},
		Attempted:  true,
		BytesFreed: 999,
	})
	if err != nil {
		t.Fatal(err)
	}

	var env destructiveEnvelopeMirror
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	var res resultMirror
	if err := json.Unmarshal(env.RawResult, &res); err != nil {
		t.Fatal(err)
	}
	if !res.Attempted || res.BytesFreed != 999 {
		t.Errorf("result: %+v", res)
	}
	if res.Warnings == nil {
		t.Errorf("warnings MUST be non-nil (empty slice)")
	}
}

// TestRemoveJSONEmptyRefusalOmittedFromWire pins the `omitempty`
// contract on the refusal field: an empty string MUST NOT appear on
// the wire so consumers can dispatch on key presence.
func TestRemoveJSONEmptyRefusalOmittedFromWire(t *testing.T) {
	data, err := MarshalRemoveJSON(RemoveJSONInput{
		Plan: RemovePlan{Target: "/wk/f", Backend: "git"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "\"refusal\"") {
		t.Errorf("empty refusal MUST be omitted from wire:\n%s", data)
	}
}

// ============================================================
// forget
// ============================================================

// TestForgetJSONDryRunOmitsResult pins the shared envelope shape
// for `wrk forget --json --dry-run`.
func TestForgetJSONDryRunOmitsResult(t *testing.T) {
	plan := ForgetPlan{
		RepositoryID:  "local/abc",
		StoragePath:   "/store/local/abc",
		VariantCount:  3,
		ResourceCount: 2,
		TotalSize:     4096,
		RegistryEntries: map[string][]string{
			"/wk/b": {".env"},
			"/wk/a": {"node_modules"},
		},
		Refusal: "detached-file registry entries exist",
	}

	data, err := MarshalForgetJSON(ForgetJSONInput{Plan: plan, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	var env destructiveEnvelopeMirror
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	if env.Kind != "forget" || !env.DryRun || len(env.RawResult) != 0 {
		t.Errorf("envelope: %+v; raw=%s", env, string(env.RawResult))
	}

	var full struct {
		Plan forgetPlanJSON `json:"plan"`
	}
	if err := json.Unmarshal(data, &full); err != nil {
		t.Fatal(err)
	}
	if full.Plan.RepositoryID != "local/abc" {
		t.Errorf("repositoryId: %q", full.Plan.RepositoryID)
	}
	if full.Plan.VariantCount != 3 {
		t.Errorf("variantCount: %d", full.Plan.VariantCount)
	}
	if full.Plan.TotalSize != 4096 {
		t.Errorf("totalSize: %d", full.Plan.TotalSize)
	}
	// Registry entries MUST be sorted for deterministic output.
	if len(full.Plan.RegistryEntries) != 2 ||
		full.Plan.RegistryEntries[0] != "/wk/a" ||
		full.Plan.RegistryEntries[1] != "/wk/b" {
		t.Errorf("registryEntries MUST be sorted, got %+v", full.Plan.RegistryEntries)
	}
	if full.Plan.Refusal == "" {
		t.Errorf("refusal MUST be surfaced when set")
	}
}

// TestForgetJSONExecutePopulatesResult pins the executed branch.
func TestForgetJSONExecutePopulatesResult(t *testing.T) {
	data, err := MarshalForgetJSON(ForgetJSONInput{
		Plan:       ForgetPlan{RepositoryID: "local/abc"},
		Attempted:  true,
		BytesFreed: 8192,
		Warnings:   []string{"warning: could not remove /store/x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var env destructiveEnvelopeMirror
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	var res resultMirror
	if err := json.Unmarshal(env.RawResult, &res); err != nil {
		t.Fatal(err)
	}
	if !res.Attempted || res.BytesFreed != 8192 {
		t.Errorf("result: %+v", res)
	}
	if len(res.Warnings) != 1 {
		t.Errorf("warnings: %+v", res.Warnings)
	}
}

// ============================================================
// run
// ============================================================

// TestRunJSONDryRunEmitsResourceAndCommandCount pins the shape of
// the run plan projection: resource identity + command count only,
// no result key in dry-run mode.
func TestRunJSONDryRunEmitsResourceAndCommandCount(t *testing.T) {
	plan := RunPlan{
		Root: "/wk",
		Resource: config.Resource{
			Name: "node",
			Path: "node_modules",
		},
		Commands: []config.Command{
			{Run: "yarn install"},
			{Run: "yarn build"},
		},
	}
	data, err := MarshalRunJSON(RunJSONInput{Plan: plan, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	var env destructiveEnvelopeMirror
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	if env.Kind != "run" || !env.DryRun || len(env.RawResult) != 0 {
		t.Errorf("envelope: %+v", env)
	}
	var full struct {
		Plan runPlanJSON `json:"plan"`
	}
	if err := json.Unmarshal(data, &full); err != nil {
		t.Fatal(err)
	}
	if full.Plan.Resource.Name != "node" || full.Plan.Resource.Path != "node_modules" {
		t.Errorf("resource: %+v", full.Plan.Resource)
	}
	if full.Plan.CommandCount != 2 {
		t.Errorf("commandCount: got %d want 2", full.Plan.CommandCount)
	}
	if full.Plan.VariantPath != "" {
		t.Errorf("variantPath: empty plan actions MUST yield \"\", got %q", full.Plan.VariantPath)
	}
}

// TestRunJSONExecutePopulatesResult pins the executed branch: the
// caller records BytesFreed / Warnings, marshaler wires them in.
func TestRunJSONExecutePopulatesResult(t *testing.T) {
	data, err := MarshalRunJSON(RunJSONInput{
		Plan:       RunPlan{Resource: config.Resource{Name: "n"}},
		Attempted:  true,
		BytesFreed: 42,
	})
	if err != nil {
		t.Fatal(err)
	}
	var env destructiveEnvelopeMirror
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	var res resultMirror
	if err := json.Unmarshal(env.RawResult, &res); err != nil {
		t.Fatal(err)
	}
	if !res.Attempted || res.BytesFreed != 42 {
		t.Errorf("result: %+v", res)
	}
}

// ============================================================
// relink
// ============================================================

// TestRelinkJSONDryRunFlattensActions pins the relink plan projection:
// the planner.Action union is flattened to one-line descriptions
// matching the human printer's output. Actions count and description
// text both surface for machine consumers verifying "what would
// happen".
func TestRelinkJSONDryRunFlattensActions(t *testing.T) {
	plan := planner.Plan{
		WorkspaceRoot: "/wk",
		Actions: []planner.PlannedAction{
			{Action: planner.CreateDirectory{Path: "/store/env"}},
			{Action: planner.Symlink{Link: "/wk/.env", Target: "/store/env/.env"}},
		},
	}
	data, err := MarshalRelinkJSON(RelinkJSONInput{Plan: RelinkPlan{Plan: plan}, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	var env destructiveEnvelopeMirror
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	if env.Kind != "relink" || !env.DryRun || len(env.RawResult) != 0 {
		t.Errorf("envelope: %+v", env)
	}
	var full struct {
		Plan relinkPlanJSON `json:"plan"`
	}
	if err := json.Unmarshal(data, &full); err != nil {
		t.Fatal(err)
	}
	if full.Plan.ActionCount != 2 {
		t.Errorf("actionCount: got %d want 2", full.Plan.ActionCount)
	}
	if len(full.Plan.Descriptions) != 2 {
		t.Fatalf("descriptions: got %d want 2", len(full.Plan.Descriptions))
	}
	if !strings.Contains(full.Plan.Descriptions[0], "create directory") {
		t.Errorf("first description: %q", full.Plan.Descriptions[0])
	}
	if !strings.Contains(full.Plan.Descriptions[1], "link") {
		t.Errorf("second description: %q", full.Plan.Descriptions[1])
	}
}

// TestRelinkJSONExecutePopulatesResult pins the executed branch.
func TestRelinkJSONExecutePopulatesResult(t *testing.T) {
	data, err := MarshalRelinkJSON(RelinkJSONInput{
		Attempted:  true,
		BytesFreed: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	var env destructiveEnvelopeMirror
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	var res resultMirror
	if err := json.Unmarshal(env.RawResult, &res); err != nil {
		t.Fatal(err)
	}
	if !res.Attempted || res.BytesFreed != 10 {
		t.Errorf("result: %+v", res)
	}
}

// TestRelinkJSONSurfacesIsolationExits pins the isolation projection
// added when RelinkJSONInput.Plan became engine.RelinkPlan: each
// planned isolation exit surfaces as {resource,path,storagePath} and
// each unmanageable registry entry as a skippedIsolation string —
// the machine-readable counterpart of the human printer's ⚠ block.
// A consumer gating a destructive confirmation on this array would
// silently destroy isolated variants if the exits were dropped from
// the wire shape.
func TestRelinkJSONSurfacesIsolationExits(t *testing.T) {
	plan := RelinkPlan{
		Plan: planner.Plan{
			WorkspaceRoot: "/wk",
			Actions: []planner.PlannedAction{
				{Action: planner.Symlink{
					Link:   "/wk/node_modules",
					Target: "/store/repo/node_modules/abc123",
				}},
			},
		},
		IsolationExits: []IsolationExit{
			{
				ResourceName: "node",
				ResourcePath: "node_modules",
				StoragePath:  "/store/repo/node_modules/isolated-ab12",
			},
		},
		SkippedIsolation: []string{"gone/path"},
	}
	data, err := MarshalRelinkJSON(RelinkJSONInput{Plan: plan, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	var env destructiveEnvelopeMirror
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	if env.Schema != 1 || env.Kind != "relink" {
		t.Errorf("envelope changed: schema=%d kind=%q, want 1/relink", env.Schema, env.Kind)
	}
	var full struct {
		Plan struct {
			IsolationExits []struct {
				Resource    string `json:"resource"`
				Path        string `json:"path"`
				StoragePath string `json:"storagePath"`
			} `json:"isolationExits"`
			SkippedIsolation []string `json:"skippedIsolation"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(data, &full); err != nil {
		t.Fatal(err)
	}
	if len(full.Plan.IsolationExits) != 1 {
		t.Fatalf("isolationExits: got %d want 1", len(full.Plan.IsolationExits))
	}
	e := full.Plan.IsolationExits[0]
	if e.Resource != "node" || e.Path != "node_modules" ||
		e.StoragePath != "/store/repo/node_modules/isolated-ab12" {
		t.Errorf("isolationExits[0]: %+v", e)
	}
	if len(full.Plan.SkippedIsolation) != 1 || full.Plan.SkippedIsolation[0] != "gone/path" {
		t.Errorf("skippedIsolation: %v, want [gone/path]", full.Plan.SkippedIsolation)
	}
}

// TestRelinkJSONEmptyPlanEmitsEmptyDescriptions pins the null-safety
// contract on every array in the relink plan projection: nil slices
// serialise as `[]`, never `null`, so consumers iterate without a
// nil check.
func TestRelinkJSONEmptyPlanEmitsEmptyDescriptions(t *testing.T) {
	data, err := MarshalRelinkJSON(RelinkJSONInput{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"descriptions", "isolationExits", "skippedIsolation"} {
		if !strings.Contains(string(data), "\""+key+"\": []") {
			t.Errorf("%s MUST serialize as []:\n%s", key, data)
		}
	}
}

// ============================================================
// relink --isolate
// ============================================================

// TestRelinkIsolateJSONDryRunSurfacesResources pins the isolate plan
// projection: each resource entry maps to a {name,path} pair via the
// shared runResourceJSON shape.
func TestRelinkIsolateJSONDryRunSurfacesResources(t *testing.T) {
	plan := IsolatePlan{
		Root: "/wk",
		Resources: []config.Resource{
			{Name: "node", Path: "node_modules"},
			{Name: "env", Path: ".env"},
		},
	}
	data, err := MarshalRelinkIsolateJSON(RelinkIsolateJSONInput{Plan: plan, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	var env destructiveEnvelopeMirror
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	if env.Kind != "relink-isolate" || !env.DryRun || len(env.RawResult) != 0 {
		t.Errorf("envelope: %+v", env)
	}
	var full struct {
		Plan struct {
			Resources []runResourceJSON `json:"resources"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(data, &full); err != nil {
		t.Fatal(err)
	}
	if len(full.Plan.Resources) != 2 {
		t.Fatalf("resources: got %d want 2", len(full.Plan.Resources))
	}
	if full.Plan.Resources[0].Name != "node" || full.Plan.Resources[0].Path != "node_modules" {
		t.Errorf("first resource: %+v", full.Plan.Resources[0])
	}
}

// TestRelinkIsolateJSONExecutePopulatesResult pins the executed
// branch.
func TestRelinkIsolateJSONExecutePopulatesResult(t *testing.T) {
	data, err := MarshalRelinkIsolateJSON(RelinkIsolateJSONInput{
		Attempted:  true,
		BytesFreed: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	var env destructiveEnvelopeMirror
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	var res resultMirror
	if err := json.Unmarshal(env.RawResult, &res); err != nil {
		t.Fatal(err)
	}
	if !res.Attempted || res.BytesFreed != 7 {
		t.Errorf("result: %+v", res)
	}
}

// ============================================================
// detach
// ============================================================

// TestDetachJSONDryRunFlattensActions pins the detach plan projection:
// same shape as relink so a consumer can share a parser.
func TestDetachJSONDryRunFlattensActions(t *testing.T) {
	plan := planner.Plan{
		WorkspaceRoot: "/wk",
		Actions: []planner.PlannedAction{
			{Action: planner.Detach{Link: "/wk/.env"}},
		},
	}
	data, err := MarshalDetachJSON(DetachJSONInput{Plan: plan, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	var env destructiveEnvelopeMirror
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	if env.Kind != "detach" || !env.DryRun || len(env.RawResult) != 0 {
		t.Errorf("envelope: %+v", env)
	}
	var full struct {
		Plan detachPlanJSON `json:"plan"`
	}
	if err := json.Unmarshal(data, &full); err != nil {
		t.Fatal(err)
	}
	if full.Plan.ActionCount != 1 {
		t.Errorf("actionCount: got %d want 1", full.Plan.ActionCount)
	}
	if len(full.Plan.Descriptions) != 1 || !strings.Contains(full.Plan.Descriptions[0], "independent copy") {
		t.Errorf("descriptions: %+v", full.Plan.Descriptions)
	}
}

// TestDetachJSONExecutePopulatesResult pins the executed branch.
func TestDetachJSONExecutePopulatesResult(t *testing.T) {
	data, err := MarshalDetachJSON(DetachJSONInput{
		Attempted: true,
		Warnings:  []string{"warning: something"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var env destructiveEnvelopeMirror
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	var res resultMirror
	if err := json.Unmarshal(env.RawResult, &res); err != nil {
		t.Fatal(err)
	}
	if !res.Attempted {
		t.Errorf("attempted: got false want true")
	}
	if len(res.Warnings) != 1 || res.Warnings[0] != "warning: something" {
		t.Errorf("warnings: %+v", res.Warnings)
	}
}

// ============================================================
// T1: envelope stability tests
// ============================================================
//
// One test per JSON-emitting command locks the top-level key set of
// its output. These tests are cheap to add (no VCS setup for the
// pure-marshaler variants) and catch accidental additions or removals
// of top-level fields — the wire contract downstream tooling
// dispatches on. Renaming or dropping a key is a breaking API change
// that MUST update the expected list here so a reviewer sees it.
//
// The destructive commands cover both branches: dry-run / refused
// (result key ABSENT via omitempty) and attempted (result key
// PRESENT). Both matter to consumers because they route on presence.

// assertTopLevelKeys decodes data as a JSON object and pins the exact
// set of top-level keys against want. Order-independent.
func assertTopLevelKeys(t *testing.T, data []byte, want []string) {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, data)
	}
	got := make([]string, 0, len(raw))
	for k := range raw {
		got = append(got, k)
	}
	sort.Strings(got)
	sortedWant := make([]string, len(want))
	copy(sortedWant, want)
	sort.Strings(sortedWant)
	if !reflect.DeepEqual(got, sortedWant) {
		t.Errorf("top-level keys = %v, want %v", got, sortedWant)
	}
}

// TestStatusJSONTopLevelKeys locks the top-level shape of
// `wrk status --json`. Adding or dropping a key is a breaking
// change to the machine-readable contract.
func TestStatusJSONTopLevelKeys(t *testing.T) {
	data, err := MarshalStatusJSON(&StatusReport{}, "/repo")
	if err != nil {
		t.Fatal(err)
	}
	assertTopLevelKeys(t, data, []string{"schema", "kind", "sources", "workspaces"})
}

// TestListJSONTopLevelKeys locks the top-level shape of
// `wrk list --json`.
func TestListJSONTopLevelKeys(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources: []\n",
	})
	data, err := MarshalListJSON(repo, Options{StorageRoot: storageIn(t, repo.Root)}, false)
	if err != nil {
		t.Fatal(err)
	}
	assertTopLevelKeys(t, data, []string{"schema", "kind", "root", "resources"})
}

// TestWorkspacesJSONTopLevelKeys locks the top-level shape of
// `wrk workspaces --json`.
func TestWorkspacesJSONTopLevelKeys(t *testing.T) {
	data, err := MarshalWorkspacesJSON(nil)
	if err != nil {
		t.Fatal(err)
	}
	assertTopLevelKeys(t, data, []string{"schema", "kind", "workspaces"})
}

// TestFingerprintJSONTopLevelKeys locks the top-level shape of
// `wrk fingerprint --json`.
func TestFingerprintJSONTopLevelKeys(t *testing.T) {
	data, err := MarshalFingerprintJSON(&FingerprintReport{
		Resource: config.Resource{Name: "node", Path: "node_modules"},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertTopLevelKeys(t, data, []string{
		"schema", "kind", "resource", "current", "pinned", "changed", "isolated",
	})
}

// TestDoctorJSONTopLevelKeys locks the top-level shape of
// `wrk doctor --json`.
func TestDoctorJSONTopLevelKeys(t *testing.T) {
	data, err := MarshalDoctorJSON(&DoctorReport{
		Root:         "/repo",
		RepositoryID: "local/abc",
		VCS:          "git",
		Checks:       DoctorChecks{ConfigValid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertTopLevelKeys(t, data, []string{
		"schema", "kind", "root", "repositoryId", "vcs", "checks", "issues",
	})
}

// TestGCJSONTopLevelKeysDryRunOmitsResult pins the omitempty contract:
// in --dry-run mode the `result` key is absent.
func TestGCJSONTopLevelKeysDryRunOmitsResult(t *testing.T) {
	data, err := MarshalGCJSON(GCJSONInput{Plan: GCPlan{}, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	assertTopLevelKeys(t, data, []string{"schema", "kind", "dryRun", "plan"})
}

// TestGCJSONTopLevelKeysWithResult pins the executed branch: Attempted
// flips the `result` key on.
func TestGCJSONTopLevelKeysWithResult(t *testing.T) {
	data, err := MarshalGCJSON(GCJSONInput{Plan: GCPlan{}, Attempted: true})
	if err != nil {
		t.Fatal(err)
	}
	assertTopLevelKeys(t, data, []string{"schema", "kind", "dryRun", "plan", "result"})
}

// TestRemoveJSONTopLevelKeysDryRunOmitsResult pins the omitempty
// contract on `wrk remove --json --dry-run`.
func TestRemoveJSONTopLevelKeysDryRunOmitsResult(t *testing.T) {
	data, err := MarshalRemoveJSON(RemoveJSONInput{
		Plan: RemovePlan{Target: "/wk/f", Backend: "git"}, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertTopLevelKeys(t, data, []string{"schema", "kind", "dryRun", "plan"})
}

// TestRemoveJSONTopLevelKeysWithResult pins the executed branch.
func TestRemoveJSONTopLevelKeysWithResult(t *testing.T) {
	data, err := MarshalRemoveJSON(RemoveJSONInput{
		Plan: RemovePlan{Target: "/wk/f", Backend: "git"}, Attempted: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertTopLevelKeys(t, data, []string{"schema", "kind", "dryRun", "plan", "result"})
}

// TestForgetJSONTopLevelKeysDryRunOmitsResult pins the omitempty
// contract on `wrk forget --json --dry-run`.
func TestForgetJSONTopLevelKeysDryRunOmitsResult(t *testing.T) {
	data, err := MarshalForgetJSON(ForgetJSONInput{
		Plan: ForgetPlan{RepositoryID: "local/abc"}, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertTopLevelKeys(t, data, []string{"schema", "kind", "dryRun", "plan"})
}

// TestForgetJSONTopLevelKeysWithResult pins the executed branch.
func TestForgetJSONTopLevelKeysWithResult(t *testing.T) {
	data, err := MarshalForgetJSON(ForgetJSONInput{
		Plan: ForgetPlan{RepositoryID: "local/abc"}, Attempted: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertTopLevelKeys(t, data, []string{"schema", "kind", "dryRun", "plan", "result"})
}

// TestRunJSONTopLevelKeysDryRunOmitsResult pins the omitempty
// contract on `wrk run --json --dry-run`.
func TestRunJSONTopLevelKeysDryRunOmitsResult(t *testing.T) {
	data, err := MarshalRunJSON(RunJSONInput{
		Plan: RunPlan{Resource: config.Resource{Name: "n"}}, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertTopLevelKeys(t, data, []string{"schema", "kind", "dryRun", "plan"})
}

// TestRunJSONTopLevelKeysWithResult pins the executed branch.
func TestRunJSONTopLevelKeysWithResult(t *testing.T) {
	data, err := MarshalRunJSON(RunJSONInput{
		Plan: RunPlan{Resource: config.Resource{Name: "n"}}, Attempted: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertTopLevelKeys(t, data, []string{"schema", "kind", "dryRun", "plan", "result"})
}

// TestDetachJSONTopLevelKeysDryRunOmitsResult pins the omitempty
// contract on `wrk detach --json --dry-run`.
func TestDetachJSONTopLevelKeysDryRunOmitsResult(t *testing.T) {
	data, err := MarshalDetachJSON(DetachJSONInput{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	assertTopLevelKeys(t, data, []string{"schema", "kind", "dryRun", "plan"})
}

// TestDetachJSONTopLevelKeysWithResult pins the executed branch.
func TestDetachJSONTopLevelKeysWithResult(t *testing.T) {
	data, err := MarshalDetachJSON(DetachJSONInput{Attempted: true})
	if err != nil {
		t.Fatal(err)
	}
	assertTopLevelKeys(t, data, []string{"schema", "kind", "dryRun", "plan", "result"})
}

// TestRelinkJSONTopLevelKeysDryRunOmitsResult pins the omitempty
// contract on `wrk relink --json --dry-run`.
func TestRelinkJSONTopLevelKeysDryRunOmitsResult(t *testing.T) {
	data, err := MarshalRelinkJSON(RelinkJSONInput{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	assertTopLevelKeys(t, data, []string{"schema", "kind", "dryRun", "plan"})
}

// TestRelinkJSONTopLevelKeysWithResult pins the executed branch.
func TestRelinkJSONTopLevelKeysWithResult(t *testing.T) {
	data, err := MarshalRelinkJSON(RelinkJSONInput{Attempted: true})
	if err != nil {
		t.Fatal(err)
	}
	assertTopLevelKeys(t, data, []string{"schema", "kind", "dryRun", "plan", "result"})
}

// TestRelinkIsolateJSONTopLevelKeysDryRunOmitsResult pins the
// omitempty contract on `wrk relink --isolate --json --dry-run`.
func TestRelinkIsolateJSONTopLevelKeysDryRunOmitsResult(t *testing.T) {
	data, err := MarshalRelinkIsolateJSON(RelinkIsolateJSONInput{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	assertTopLevelKeys(t, data, []string{"schema", "kind", "dryRun", "plan"})
}

// TestRelinkIsolateJSONTopLevelKeysWithResult pins the executed branch.
func TestRelinkIsolateJSONTopLevelKeysWithResult(t *testing.T) {
	data, err := MarshalRelinkIsolateJSON(RelinkIsolateJSONInput{Attempted: true})
	if err != nil {
		t.Fatal(err)
	}
	assertTopLevelKeys(t, data, []string{"schema", "kind", "dryRun", "plan", "result"})
}

// TestForgetPlanJSONCarriesIsolatedEntries pins the isolatedEntries
// projection: a populated plan surfaces the pre-sorted entries; an
// empty plan emits `[]`, never null, so consumers can iterate without
// a nil check.
func TestForgetPlanJSONCarriesIsolatedEntries(t *testing.T) {
	data, err := MarshalForgetJSON(ForgetJSONInput{Plan: ForgetPlan{
		RepositoryID:    "local/abc",
		IsolatedEntries: []string{"/wk/a: node_modules", "/wk/b: vendor/bundle"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var full struct {
		Plan forgetPlanJSON `json:"plan"`
	}
	if err := json.Unmarshal(data, &full); err != nil {
		t.Fatal(err)
	}
	if len(full.Plan.IsolatedEntries) != 2 ||
		full.Plan.IsolatedEntries[0] != "/wk/a: node_modules" ||
		full.Plan.IsolatedEntries[1] != "/wk/b: vendor/bundle" {
		t.Errorf("isolatedEntries: %+v", full.Plan.IsolatedEntries)
	}

	empty, err := MarshalForgetJSON(ForgetJSONInput{Plan: ForgetPlan{RepositoryID: "local/abc"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(empty), "\"isolatedEntries\": []") {
		t.Errorf("empty plan MUST emit isolatedEntries as []:\n%s", empty)
	}
}

// TestRemovePlanJSONCarriesIsolatedPaths is the remove-side mirror:
// isolatedPaths is always an array, populated or `[]`.
func TestRemovePlanJSONCarriesIsolatedPaths(t *testing.T) {
	data, err := MarshalRemoveJSON(RemoveJSONInput{Plan: RemovePlan{
		Target:        "/wk/feature",
		Backend:       "git",
		IsolatedPaths: []string{"node_modules"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var full struct {
		Plan removePlanJSON `json:"plan"`
	}
	if err := json.Unmarshal(data, &full); err != nil {
		t.Fatal(err)
	}
	if len(full.Plan.IsolatedPaths) != 1 || full.Plan.IsolatedPaths[0] != "node_modules" {
		t.Errorf("isolatedPaths: %+v", full.Plan.IsolatedPaths)
	}

	empty, err := MarshalRemoveJSON(RemoveJSONInput{Plan: RemovePlan{Target: "/wk/f", Backend: "git"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(empty), "\"isolatedPaths\": []") {
		t.Errorf("empty plan MUST emit isolatedPaths as []:\n%s", empty)
	}
}
