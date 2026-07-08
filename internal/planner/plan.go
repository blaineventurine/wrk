package planner

import "github.com/blaineventurine/wrk/internal/resolver"

// PlannedAction associates an action with the resource that produced it.
type PlannedAction struct {
	Instance resolver.ResourceInstance
	Action   Action
}

type Conflict struct {
	Instance resolver.ResourceInstance
	Message  string
}

// ResourcePlan is the plan for a single resource instance.
type ResourcePlan struct {
	Actions   []PlannedAction
	Conflicts []Conflict
}

func (p *ResourcePlan) AddAction(
	instance resolver.ResourceInstance,
	action Action,
) {
	p.Actions = append(
		p.Actions,
		PlannedAction{
			Instance: instance,
			Action:   action,
		},
	)
}

func (p *ResourcePlan) AddConflict(
	instance resolver.ResourceInstance,
	message string,
) {
	p.Conflicts = append(
		p.Conflicts,
		Conflict{
			Instance: instance,
			Message:  message,
		},
	)
}

// Plan is the plan for an entire workspace.
type Plan struct {
	// WorkspaceRoot is the canonical repository root the plan applies to.
	// The executor uses it to reject workspace-side actions whose path
	// escapes the root through a symlink.
	WorkspaceRoot string

	Actions   []PlannedAction
	Conflicts []Conflict
}

func (p *Plan) AddResourcePlan(
	resource ResourcePlan,
) {
	p.Actions = append(
		p.Actions,
		resource.Actions...,
	)

	p.Conflicts = append(
		p.Conflicts,
		resource.Conflicts...,
	)
}

func (p Plan) HasConflicts() bool {
	return len(p.Conflicts) > 0
}
