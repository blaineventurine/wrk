package executor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/blaineventurine/wrk/internal/planner"
	"github.com/blaineventurine/wrk/internal/resolver"
)

func TestExecuteEmptyPlan(t *testing.T) {
	if err := Execute(planner.Plan{}); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteCreateDirectory(t *testing.T) {
	root := t.TempDir()

	path := filepath.Join(root, "a", "b", "c")

	plan := planner.Plan{
		Actions: []planner.PlannedAction{
			{
				Instance: resolver.ResourceInstance{},
				Action: planner.CreateDirectory{
					Path: path,
				},
			},
		},
	}

	if err := Execute(plan); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

// TestExecuteRefusesSymlinkEscape guards the runtime containment check.
// A Symlink action whose Link falls outside plan.WorkspaceRoot (via an
// in-root symlink pointing outside) must be refused before the executor
// creates or mutates anything.
func TestExecuteRefusesSymlinkEscape(t *testing.T) {
	root := canonRoot(t, t.TempDir())
	outside := canonRoot(t, t.TempDir())

	// Symlink `<root>/escape` -> outside — anything under `escape/` is
	// really under `outside/`.
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(root, "escape", "resource")
	target := filepath.Join(t.TempDir(), "shared-resource")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan := planner.Plan{
		WorkspaceRoot: root,
		Actions: []planner.PlannedAction{
			{
				Instance: resolver.ResourceInstance{},
				Action: planner.Symlink{
					Link:   link,
					Target: target,
				},
			},
		},
	}

	err := Execute(plan)
	if err == nil {
		t.Fatal("expected Execute to refuse symlink escape, got nil error")
	}

	// Nothing must have been created at the escaping link.
	if _, statErr := os.Lstat(link); !os.IsNotExist(statErr) {
		t.Errorf("expected no symlink at %s, got err=%v", link, statErr)
	}
}
