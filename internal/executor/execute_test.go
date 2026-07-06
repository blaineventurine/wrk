package executor

import (
	"os"
	"path/filepath"
	"testing"

	"wrk/internal/planner"
	"wrk/internal/resolver"
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
