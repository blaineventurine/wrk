// Package examples exposes YAML fixtures that are both user-facing
// documentation and inputs to code paths inside wrk itself.
//
// The subdirectories basic/, node/, rails/, and local-override/ hold
// complete .wrk.yml examples that ship as-is in the docs. The init/
// subdirectory holds YAML fragments composed by `wrk init` into a
// generated .wrk.yml — they are embedded here because //go:embed
// cannot reach outside its own package tree.
package examples

import "embed"

// Init contains the YAML fragments composed by `wrk init`. Each file
// under init/ is either:
//
//   - header.yml — the top-of-file comment block used when at least
//     one project layout is detected.
//   - empty.yml — the entire generated file used when nothing is
//     detected (a commented walkthrough plus `resources: []`).
//   - <kind>.yml — one resource entry (or commented placeholder) that
//     corresponds to a detection kind in internal/engine/init.go.
//     Fragments are top-level YAML sequences at zero indent so they
//     load standalone and can be indented under `resources:` at
//     render time. node-monorepo.yml is a text/template with a
//     `.Patterns` field.
//
//go:embed init/*.yml
var Init embed.FS
