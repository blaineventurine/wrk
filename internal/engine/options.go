package engine

import "io"

// Options controls engine behavior.
type Options struct {
	StorageRoot string

	DryRun bool

	Stdout io.Writer
}
