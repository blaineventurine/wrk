package main

import (
	"os"
	"strings"

	"github.com/blaineventurine/wrk/internal/engine"
	"github.com/blaineventurine/wrk/internal/repository"
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

	repo, err := repository.Detect(cwd, selected)
	if err != nil {
		// Route the "not inside a repository" case onto its stable
		// error code so `wrk <cmd> --json` emits `not_a_repository`
		// (documented in the README's error table) instead of the
		// `unknown` fallback. Detection failures for other reasons
		// (VCS binary missing, jj not colocated, ...) keep their
		// original wording and fall through untyped.
		if strings.Contains(err.Error(), "not inside a Git or Jujutsu repository") {
			return nil, engine.Wrapf(engine.ErrNotARepository,
				"run wrk inside a git worktree or jj workspace",
				err, "%s", err.Error())
		}
		return nil, err
	}
	return repo, nil
}
