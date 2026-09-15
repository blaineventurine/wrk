package executor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/blaineventurine/wrk/internal/planner"
)

func TestRecoverGroupStageRestoresPreviousLinks(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace", "node_modules")
	stage := filepath.Join(root, "shared.wrk-provisioning")
	old := filepath.Join(root, "old", "node_modules")
	if err := os.MkdirAll(filepath.Dir(workspace), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stage, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(stage, "node_modules"), workspace); err != nil {
		t.Fatal(err)
	}
	txn := groupTransaction{Outputs: []groupTransactionOutput{{Path: workspace, PreviousTarget: old, PreviousExisted: true, StageTarget: filepath.Join(stage, "node_modules")}}}
	data, err := json.Marshal(txn)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, groupTransactionFile), data, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := recoverGroupStage(stage); err != nil {
		t.Fatal(err)
	}
	got, err := os.Readlink(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if got != old {
		t.Fatalf("restored target = %q, want %q", got, old)
	}
}

func TestRecoverGroupStagePreservesChangedWorkspaceOutput(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace", "node_modules")
	stage := filepath.Join(root, "shared.wrk-provisioning")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(workspace, "user-data")
	if err := os.WriteFile(marker, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stage, 0o755); err != nil {
		t.Fatal(err)
	}
	txn := groupTransaction{Outputs: []groupTransactionOutput{{Path: workspace, StageTarget: filepath.Join(stage, "node_modules")}}}
	data, err := json.Marshal(txn)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, groupTransactionFile), data, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := recoverGroupStage(stage); err == nil {
		t.Fatal("recovery overwrote a changed workspace output")
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "keep" {
		t.Fatalf("user data = %q, err=%v", data, err)
	}
}

func TestInitializeGroupPreservesOutputCreatedAfterPlanning(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "node_modules")
	marker := filepath.Join(workspace, "user-data")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := planner.Plan{WorkspaceRoot: root, Actions: []planner.PlannedAction{{Action: planner.InitializeGroup{
		Root:   root,
		Shared: filepath.Join(root, "storage", "group"),
		Outputs: []planner.GroupOutput{{
			WorkspacePath: workspace,
			RelativePath:  "node_modules",
			ExpectedEmpty: true,
		}},
	}}}}
	if err := Execute(plan); err == nil {
		t.Fatal("InitializeGroup replaced an output created after planning")
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "keep" {
		t.Fatalf("user data = %q, err=%v", data, err)
	}
}
