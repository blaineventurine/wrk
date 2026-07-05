package repository

import (
	"fmt"
	"os"
	"path/filepath"
)

// Detect discovers the repository containing start.
func Detect(
	start string,
	preferred VCS,
) (*Repository, error) {
	root, err := findRoot(start)
	if err != nil {
		return nil, err
	}

	vcs, err := detectVCS(
		root,
		preferred,
	)
	if err != nil {
		return nil, err
	}

	metadataDir, err := gitCommonDir(root)
	if err != nil {
		return nil, err
	}

	repositoryID, err := repositoryID(
		root,
		metadataDir,
	)
	if err != nil {
		return nil, err
	}

	return newRepository(
		root,
		repositoryID,
		metadataDir,
		vcs,
	), nil
}

func findRoot(start string) (string, error) {
	current, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}

	for {
		if exists(filepath.Join(current, ".jj")) ||
			exists(filepath.Join(current, ".git")) {
			return current, nil
		}

		parent := filepath.Dir(current)

		if parent == current {
			break
		}

		current = parent
	}

	return "", fmt.Errorf(
		"not inside a Git or Jujutsu repository",
	)
}

func detectVCS(
	root string,
	preferred VCS,
) (VCS, error) {
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
			return JJ, nil
		}

	case Git:
		if hasGit {
			return Git, nil
		}

		return "", fmt.Errorf(
			"repository is not Git-managed",
		)

	case JJ:
		if hasJJ {
			return JJ, nil
		}

		return "", fmt.Errorf(
			"repository is not Jujutsu-managed",
		)
	}

	return "", fmt.Errorf(
		"unsupported VCS %q",
		preferred,
	)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
