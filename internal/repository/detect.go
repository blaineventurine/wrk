package repository

import (
	"fmt"
	"os"
	"path/filepath"
)

// Detect discovers the repository containing start.
func Detect(start string, preferred VCS) (*Repository, error) {
	root, err := findRoot(start)
	if err != nil {
		return nil, err
	}

	vcs, err := detectVCS(root, preferred)
	if err != nil {
		return nil, err
	}

	b, err := backendFor(vcs)
	if err != nil {
		return nil, err
	}

	metadataDir, err := b.commonDir(root)
	if err != nil {
		return nil, err
	}

	id, err := repositoryID(root, metadataDir)
	if err != nil {
		return nil, err
	}

	return newRepository(root, id, metadataDir, b), nil
}

func findRoot(start string) (string, error) {
	absStart, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}

	current := absStart
	for {
		if exists(filepath.Join(current, ".jj")) ||
			exists(filepath.Join(current, ".git")) {
			return canonicalize(current), nil
		}

		parent := filepath.Dir(current)

		if parent == current {
			break
		}

		current = parent
	}

	return "", fmt.Errorf(
		"not inside a Git or Jujutsu repository (searched from %s)",
		absStart,
	)
}

func detectVCS(root string, preferred VCS) (VCS, error) {
	hasGit := exists(filepath.Join(root, ".git"))
	hasJJ := exists(filepath.Join(root, ".jj"))

	switch preferred {
	case Auto:
		switch {
		case hasGit && !hasJJ:
			return Git, nil
		case hasJJ && !hasGit:
			return JJ, nil
		case hasGit && hasJJ:
			// Colocated repository: prefer jj.
			return JJ, nil
		}
		// Race: findRoot saw a marker, but by the time we
		// re-statted here the .git/.jj directory was removed
		// (concurrent `rm -rf .git`, VCS migration, etc.).
		// Falling through to the generic "unsupported VCS" message
		// leaves users chasing a nonsense error; call the scenario
		// out so they can retry.
		return "", fmt.Errorf(
			"repository markers vanished between detection and vcs selection; please re-run wrk",
		)

	case Git:
		if hasGit {
			return Git, nil
		}
		return "", fmt.Errorf("repository is not Git-managed")

	case JJ:
		if hasJJ {
			return JJ, nil
		}
		return "", fmt.Errorf("repository is not Jujutsu-managed")
	}

	return "", fmt.Errorf("unsupported VCS %q", preferred)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
