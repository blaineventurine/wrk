package engine

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/repository"
)

// seedIsolatedResource walks a workspace through Link -> Detach ->
// RelinkIsolate, returning the isolated variant path recorded in the
// registry. After it returns, the workspace's `node_modules` symlink
// points at that path and the isolation registry pins it. Consumers
// pin the subsequent behavior (link/detach/relink/status/gc) that this
// task is verifying.
func seedIsolatedResource(t *testing.T) (repo *repository.Repository, storage, isoTarget string) {
	t.Helper()
	r := newTestRepo(t)
	stor := storageIn(t, r.Root)

	writeConfig(t, r.Root, config.Filename, isolateConfigYAML)
	seedIsolateWorkspace(t, r.Root, `{"v":1}`, "v1\n")

	opts := Options{StorageRoot: stor, Stdout: &bytes.Buffer{}}
	if err := Link(r, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if err := Detach(r, opts); err != nil {
		t.Fatalf("Detach: %v", err)
	}
	if err := RelinkIsolate(r, []string{"node"}, opts); err != nil {
		t.Fatalf("RelinkIsolate: %v", err)
	}

	target, err := os.Readlink(filepath.Join(r.Root, "node_modules"))
	if err != nil {
		t.Fatalf("readlink workspace symlink: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(target), "isolated-") {
		t.Fatalf("post-isolate target = %q, want isolated-<hex>", target)
	}
	return r, stor, target
}

// TestLinkSkipsIsolatedResource: after isolate, `wrk link` must leave
// the workspace symlink untouched — repointing it would silently drag
// the workspace back onto the shared variant it explicitly walked away
// from.
func TestLinkSkipsIsolatedResource(t *testing.T) {
	repo, storage, target := seedIsolatedResource(t)

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link after isolate: %v", err)
	}

	got, err := os.Readlink(filepath.Join(repo.Root, "node_modules"))
	if err != nil {
		t.Fatalf("readlink post-Link: %v", err)
	}
	if got != target {
		t.Errorf("workspace symlink retargeted by Link: got %q, want %q", got, target)
	}
}

// TestRelinkSkipsIsolatedResource: `wrk relink` (no --isolate flag) is
// the "reconnect to shared storage" path. On an already-isolated
// resource it MUST no-op — un-isolation is a separate flow that Task
// 3.4 does not implement.
func TestRelinkSkipsIsolatedResource(t *testing.T) {
	repo, storage, target := seedIsolatedResource(t)

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Relink(repo, opts); err != nil {
		t.Fatalf("Relink after isolate: %v", err)
	}

	got, err := os.Readlink(filepath.Join(repo.Root, "node_modules"))
	if err != nil {
		t.Fatalf("readlink post-Relink: %v", err)
	}
	if got != target {
		t.Errorf("workspace symlink retargeted by Relink: got %q, want %q", got, target)
	}
}

// TestDetachSkipsIsolatedResource: `wrk detach` on an already-isolated
// resource must skip. Detaching would replace the symlink with a real
// copy and strand the isolated variant on disk — worse, the user would
// end up in `detached` state despite having explicitly asked for
// isolation.
func TestDetachSkipsIsolatedResource(t *testing.T) {
	repo, storage, target := seedIsolatedResource(t)

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Detach(repo, opts); err != nil {
		t.Fatalf("Detach after isolate: %v", err)
	}

	wsPath := filepath.Join(repo.Root, "node_modules")
	info, err := os.Lstat(wsPath)
	if err != nil {
		t.Fatalf("lstat post-Detach: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("post-Detach workspace path is not a symlink; Detach was not skipped (mode=%v)", info.Mode())
	}
	got, err := os.Readlink(wsPath)
	if err != nil {
		t.Fatalf("readlink post-Detach: %v", err)
	}
	if got != target {
		t.Errorf("workspace symlink retargeted by Detach: got %q, want %q", got, target)
	}
}

// TestStatusReportsIsolatedState: `wrk status` on the isolated
// resource must surface StateIsolated so the user (and dashboards) can
// see the workspace is deliberately off-shared.
func TestStatusReportsIsolatedState(t *testing.T) {
	repo, storage, _ := seedIsolatedResource(t)

	report, err := Status(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(report.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d: %+v", len(report.Rows), report.Rows)
	}
	row := report.Rows[0]
	if row.Path != "node_modules" {
		t.Fatalf("row.Path = %q, want node_modules", row.Path)
	}
	if row.State != StateIsolated {
		t.Errorf("row.State = %q, want %q", row.State, StateIsolated)
	}
}

// TestGCPreservesIsolatedVariant: after RelinkIsolate, BuildGCPlan +
// ExecuteGC must leave the isolated variant on disk. The pin comes
// from both the workspace symlink AND the isolation registry — this
// test asserts the composed result.
func TestGCPreservesIsolatedVariant(t *testing.T) {
	repo, storage, target := seedIsolatedResource(t)

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	plan, err := BuildGCPlan(repo, opts)
	if err != nil {
		t.Fatalf("BuildGCPlan: %v", err)
	}

	// The isolated variant must NOT be marked for deletion.
	for _, v := range plan.DeleteVariants {
		if v.StoragePath == target {
			t.Fatalf("BuildGCPlan queued isolated variant for deletion: %+v", v)
		}
	}
	if err := ExecuteGC(repo, plan, opts); err != nil {
		t.Fatalf("ExecuteGC: %v", err)
	}

	if _, err := os.Stat(target); err != nil {
		t.Errorf("isolated variant %q was removed by gc: %v", target, err)
	}
}

// TestGCPinsIsolationTargetsEvenWithBrokenSymlink: this is the whole
// reason we pin from the registry rather than trusting the readlink
// walk. If the user (or a bug) removed the workspace symlink,
// pinnedVariantsForRoots's per-workspace scan would not see the
// variant at all — only the isolation registry keeps it alive.
func TestGCPinsIsolationTargetsEvenWithBrokenSymlink(t *testing.T) {
	repo, storage, target := seedIsolatedResource(t)

	// Simulate a user removing the workspace symlink to inspect state.
	if err := os.Remove(filepath.Join(repo.Root, "node_modules")); err != nil {
		t.Fatalf("remove workspace symlink: %v", err)
	}

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	plan, err := BuildGCPlan(repo, opts)
	if err != nil {
		t.Fatalf("BuildGCPlan: %v", err)
	}
	for _, v := range plan.DeleteVariants {
		if v.StoragePath == target {
			t.Fatalf("BuildGCPlan queued isolated variant for deletion despite registry pin: %+v", v)
		}
	}

	// And confirm it lands in KeepVariants — the pin is authoritative.
	var kept bool
	for _, v := range plan.KeepVariants {
		if v.StoragePath == target {
			kept = true
			break
		}
	}
	if !kept {
		t.Errorf("isolated variant %q is neither kept nor scheduled for deletion; plan = %+v", target, plan)
	}
}

// TestRollupStateHandlesIsolated pins the "isolated" rollup rules.
// The `partial` cases matter as much as the pure resting cases —
// mixing isolated with any other resting state means the workspace
// has a deliberate split personality and neither label captures it.
func TestRollupStateHandlesIsolated(t *testing.T) {
	cases := []struct {
		name   string
		states []State
		want   string
	}{
		{"all isolated", []State{StateIsolated, StateIsolated}, "isolated"},
		{"isolated + linked", []State{StateIsolated, StateLinked}, "partial"},
		{"isolated + expected", []State{StateIsolated, StateExpected}, "partial"},
		{"isolated + detached", []State{StateIsolated, StateDetached}, "partial"},
		{"isolated + unhealthy (missing)", []State{StateIsolated, StateMissing}, "unhealthy"},
		{"isolated + unhealthy (stale)", []State{StateIsolated, StateStale}, "unhealthy"},
		{"isolated + pending", []State{StateIsolated, StatePending}, "pending"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rollupState(tc.states); got != tc.want {
				t.Errorf("rollupState(%v) = %q, want %q", tc.states, got, tc.want)
			}
		})
	}
}

// TestStatusJSONWorkspaceIsolatedRollup: end-to-end confirmation that
// the rollup surfaces through MarshalStatusJSON. A workspace whose
// only resource is isolated MUST roll up to "isolated" in the JSON
// envelope — dashboards key off this string.
func TestStatusJSONWorkspaceIsolatedRollup(t *testing.T) {
	root := "/tmp/ws"
	report := &StatusReport{
		Sources: []string{".wrk.yml"},
		Rows: []ResourceStatus{
			{
				WorkspaceRoot: root,
				Resource:      "node",
				Path:          "node_modules",
				State:         StateIsolated,
			},
		},
	}

	data, err := MarshalStatusJSON(report, root)
	if err != nil {
		t.Fatalf("MarshalStatusJSON: %v", err)
	}

	var parsed struct {
		Workspaces []struct {
			Root  string `json:"root"`
			State string `json:"state"`
		} `json:"workspaces"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v\npayload=%s", err, data)
	}
	if len(parsed.Workspaces) != 1 {
		t.Fatalf("expected 1 workspace, got %d: %s", len(parsed.Workspaces), data)
	}
	if got := parsed.Workspaces[0].State; got != "isolated" {
		t.Errorf("workspaces[0].state = %q, want %q\npayload=%s", got, "isolated", data)
	}
}
