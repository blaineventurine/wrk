package planner

import (
	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/placeholders"
)

type Action interface {
	isAction()
}

type CreateDirectory struct {
	Path string
}

func (CreateDirectory) isAction() {}

type Move struct {
	Source      string
	Destination string
}

func (Move) isAction() {}

type Remove struct {
	Path string
}

func (Remove) isAction() {}

type Symlink struct {
	Link   string
	Target string
}

func (Symlink) isAction() {}

type InitializeResource struct {
	Description string
	Context     placeholders.Context
	Commands    []config.Command
	// Force reprovisions the shared variant even when it already exists.
	// The executor renames the current variant aside, runs the hook into
	// a fresh scratch, then swaps the scratch into place — an atomic
	// rename-then-remove of the old variant. Callers use this for
	// explicit retry semantics (`wrk run`); the normal Link path leaves
	// it false so the existing shared variant is preserved.
	Force bool
}

func (InitializeResource) isAction() {}

type Detach struct {
	// Link is the path currently containing the symlink.
	Link string

	// Target is the resolved shared resource.
	Target string
}

func (Detach) isAction() {}
