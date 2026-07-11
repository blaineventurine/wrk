package engine

import "io"

// Options controls engine behavior.
type Options struct {
	StorageRoot string

	DryRun bool

	Stdout io.Writer

	// Progress is an optional byte-count callback fired by destructive
	// executor primitives (wrk remove / gc / forget) as they delete
	// individual regular files. Nil is a no-op; the CLI wires it to a
	// *progress.Bar when the operation is worth rendering. Engine
	// callers that do not care about progress rendering leave it nil.
	Progress func(int64)
}
