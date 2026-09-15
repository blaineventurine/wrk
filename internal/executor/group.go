package executor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/blaineventurine/wrk/internal/commands"
	"github.com/blaineventurine/wrk/internal/placeholders"
	"github.com/blaineventurine/wrk/internal/planner"
)

const groupTransactionFile = ".wrk-transaction.json"

type groupTransaction struct {
	Outputs []groupTransactionOutput `json:"outputs"`
}

type groupTransactionOutput struct {
	Path            string `json:"path"`
	PreviousTarget  string `json:"previousTarget,omitempty"`
	PreviousExisted bool   `json:"previousExisted"`
	StageTarget     string `json:"stageTarget"`
}

func initializeGroup(action planner.InitializeGroup) error {
	return withLock(action.Shared, func() error {
		if _, err := os.Stat(action.Shared); err == nil {
			return linkGroupOutputs(action, action.Shared)
		}
		stage := action.Shared + ".wrk-provisioning"
		if err := recoverGroupStage(stage); err != nil {
			return fmt.Errorf("recover interrupted group initialization: %w", err)
		}
		_ = os.RemoveAll(stage)
		previous := make(map[string]string, len(action.Outputs))
		for _, output := range action.Outputs {
			if err := verifyGroupOutput(output, ""); err != nil {
				return err
			}
			if !output.ExpectedEmpty {
				previous[output.WorkspacePath] = output.ExpectedLink
			}
			target := filepath.Join(stage, filepath.FromSlash(output.RelativePath))
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		}
		txn := groupTransaction{Outputs: make([]groupTransactionOutput, 0, len(action.Outputs))}
		for _, output := range action.Outputs {
			target, existed := previous[output.WorkspacePath]
			txn.Outputs = append(txn.Outputs, groupTransactionOutput{
				Path:            output.WorkspacePath,
				PreviousTarget:  target,
				PreviousExisted: existed,
				StageTarget:     filepath.Join(stage, filepath.FromSlash(output.RelativePath)),
			})
		}
		data, err := json.Marshal(txn)
		if err != nil {
			return rollbackGroup(action, previous, stage, err)
		}
		if err := writeGroupTransaction(stage, data); err != nil {
			return rollbackGroup(action, previous, stage, err)
		}
		for _, output := range action.Outputs {
			target := filepath.Join(stage, filepath.FromSlash(output.RelativePath))
			if err := replaceExpectedSymlink(output.WorkspacePath, output.ExpectedLink, output.ExpectedEmpty, &target); err != nil {
				return rollbackGroup(action, previous, stage, err)
			}
		}
		ctx := placeholders.Context{Root: action.Root, Shared: stage}
		resolved, err := commands.Resolve(action.Commands, ctx)
		if err != nil {
			return rollbackGroup(action, previous, stage, err)
		}
		for _, command := range resolved {
			if len(command.Args) == 0 {
				return rollbackGroup(action, previous, stage, fmt.Errorf("initialize command resolved to no arguments"))
			}
			cmd := exec.Command(command.Args[0], command.Args[1:]...)
			cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = command.Cwd, append(hookEnv(), environment(command.Env)...), os.Stdout, os.Stderr
			if err := cmd.Run(); err != nil {
				return rollbackGroup(action, previous, stage, &HookError{Command: strings.Join(command.Args, " "), Cwd: command.Cwd, Err: err})
			}
		}
		for _, output := range action.Outputs {
			target := filepath.Join(stage, filepath.FromSlash(output.RelativePath))
			if err := verifySymlink(output.WorkspacePath, target); err != nil {
				return rollbackGroup(action, previous, stage, fmt.Errorf("initialize hook replaced grouped output: %w", err))
			}
		}
		if err := os.MkdirAll(filepath.Dir(action.Shared), 0o755); err != nil {
			return rollbackGroup(action, previous, stage, err)
		}
		if err := os.Rename(stage, action.Shared); err != nil {
			return rollbackGroup(action, previous, stage, err)
		}
		_ = os.Remove(filepath.Join(action.Shared, groupTransactionFile))
		return finalizeGroupOutputs(action, stage)
	})
}

func finalizeGroupOutputs(action planner.InitializeGroup, stage string) error {
	for _, output := range action.Outputs {
		expected := filepath.Join(stage, filepath.FromSlash(output.RelativePath))
		target := filepath.Join(action.Shared, filepath.FromSlash(output.RelativePath))
		if err := replaceExpectedSymlink(output.WorkspacePath, expected, false, &target); err != nil {
			return err
		}
	}
	return nil
}

func linkGroupOutputs(action planner.InitializeGroup, root string) error {
	for _, output := range action.Outputs {
		target := filepath.Join(root, filepath.FromSlash(output.RelativePath))
		if err := verifyGroupOutput(output, target); err == nil {
			continue
		}
		if err := replaceExpectedSymlink(output.WorkspacePath, output.ExpectedLink, output.ExpectedEmpty, &target); err != nil {
			return err
		}
	}
	return nil
}

func replaceExpectedSymlink(path, expected string, expectedAbsent bool, target *string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if expectedAbsent {
		if target == nil {
			return nil
		}
		if err := os.Symlink(*target, path); err != nil {
			return fmt.Errorf("group output %s changed after planning: %w", path, err)
		}
		return nil
	}
	quarantine, err := temporarySibling(path)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(quarantine) }()
	if err := os.Rename(path, quarantine); err != nil {
		return fmt.Errorf("group output %s changed after planning: %w", path, err)
	}
	restore := func() { _ = os.Rename(quarantine, path) }
	if err := verifySymlink(quarantine, expected); err != nil {
		restore()
		return fmt.Errorf("group output %s changed after planning: %w", path, err)
	}
	if target == nil {
		return os.Remove(quarantine)
	}
	if err := os.Symlink(*target, path); err != nil {
		restore()
		return err
	}
	return os.Remove(quarantine)
}

func rollbackGroup(action planner.InitializeGroup, previous map[string]string, stage string, cause error) error {
	var rollbackErr error
	for _, output := range action.Outputs {
		stageTarget := filepath.Join(stage, filepath.FromSlash(output.RelativePath))
		previousTarget, existed := previous[output.WorkspacePath]
		var target *string
		if existed {
			target = &previousTarget
		}
		if groupOutputRestored(output.WorkspacePath, previousTarget, existed) {
			continue
		}
		if err := replaceExpectedSymlink(output.WorkspacePath, stageTarget, false, target); err != nil && rollbackErr == nil {
			rollbackErr = err
		}
	}
	if rollbackErr != nil {
		return fmt.Errorf("%v; restoring previous grouped outputs: %w", cause, rollbackErr)
	}
	_ = os.RemoveAll(stage)
	return cause
}

func recoverGroupStage(stage string) error {
	data, err := os.ReadFile(filepath.Join(stage, groupTransactionFile))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var txn groupTransaction
	if err := json.Unmarshal(data, &txn); err != nil {
		return err
	}
	for _, output := range txn.Outputs {
		var target *string
		if output.PreviousExisted {
			target = &output.PreviousTarget
		}
		if groupOutputRestored(output.Path, output.PreviousTarget, output.PreviousExisted) {
			continue
		}
		if err := replaceExpectedSymlink(output.Path, output.StageTarget, false, target); err != nil {
			return err
		}
	}
	return nil
}

func groupOutputRestored(path, previousTarget string, previousExisted bool) bool {
	if previousExisted {
		return verifySymlink(path, previousTarget) == nil
	}
	_, err := os.Lstat(path)
	return os.IsNotExist(err)
}

func verifyGroupOutput(output planner.GroupOutput, acceptableTarget string) error {
	if acceptableTarget != "" {
		if err := verifySymlink(output.WorkspacePath, acceptableTarget); err == nil {
			return nil
		}
	}
	if output.ExpectedEmpty {
		if _, err := os.Lstat(output.WorkspacePath); os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("group output %s changed after planning", output.WorkspacePath)
	}
	return verifySymlink(output.WorkspacePath, output.ExpectedLink)
}

func verifySymlink(path, target string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("%s is not a symlink", path)
	}
	actual, err := os.Readlink(path)
	if err != nil {
		return err
	}
	if actual != target {
		return fmt.Errorf("%s points to %s, expected %s", path, actual, target)
	}
	return nil
}

func temporarySibling(path string) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(path), ".wrk-group-swap-*.wrk-tmp")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := os.Remove(name); err != nil {
		return "", err
	}
	return name, nil
}

func writeGroupTransaction(stage string, data []byte) error {
	f, err := os.CreateTemp(stage, ".wrk-transaction-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer func() { _ = os.Remove(name) }()
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(stage, groupTransactionFile))
}
