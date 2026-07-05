package planner

import (
	"wrk/internal/config"
	"wrk/internal/placeholders"
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
}

func (InitializeResource) isAction() {}

type Detach struct {
	// Link is the path currently containing the symlink.
	Link string

	// Target is the resolved shared resource.
	Target string
}

func (Detach) isAction() {}
