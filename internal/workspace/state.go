package workspace

// State describes the current state of a managed resource.
type State struct {
	WorkspaceExists    bool
	WorkspaceDirectory bool
	WorkspaceSymlink   bool

	// WorkspaceTarget is the canonical, absolute path of the symlink target.
	//
	// Empty if WorkspaceSymlink is false.
	WorkspaceTarget string

	SharedExists bool
}
