package workspace

// State describes the current state of a managed resource.
type State struct {
	WorkspaceExists    bool
	WorkspaceDirectory bool
	WorkspaceSymlink   bool

	// WorkspaceTarget is the canonical, absolute path of the symlink target,
	// with all intermediate symlinks resolved (via EvalSymlinks).
	//
	// Empty if WorkspaceSymlink is false, or if the target cannot be
	// resolved (for example, a dangling link).
	WorkspaceTarget string

	// WorkspaceLinkText is the literal, unresolved target the symlink points
	// at (via Readlink). This is what wrk writes when creating a link, so it
	// is the correct value to compare against the intended shared path.
	//
	// Empty if WorkspaceSymlink is false.
	WorkspaceLinkText string

	SharedExists bool
}
