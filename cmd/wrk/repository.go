package main

import (
	"os"

	"wrk/internal/repository"
)

func currentRepository() (*repository.Repository, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	selected, err := repository.ParseVCS(vcs)
	if err != nil {
		return nil, err
	}

	return repository.Detect(
		cwd,
		selected,
	)
}
