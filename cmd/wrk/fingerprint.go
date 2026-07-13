package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/blaineventurine/wrk/internal/engine"
)

var fingerprintJSONFlag bool

var fingerprintCmd = &cobra.Command{
	Use:   "fingerprint <resource>",
	Short: "Show fingerprint details for a resource (why is it stale?)",
	Long: `Prints the current fingerprint computed from the resource's inputs and
the fingerprint currently pinned by the workspace symlink. When they differ,
run 'wrk link' to re-point the workspace at the current variant.

Use this to answer "why does wrk status say this resource is stale?"
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, err := currentRepository()
		if err != nil {
			return err
		}
		report, err := engine.FingerprintOne(repo, args[0], engine.Options{
			StorageRoot: storageRoot,
			Stdout:      os.Stdout,
		})
		if err != nil {
			return err
		}
		if fingerprintJSONFlag {
			return printFingerprintJSON(os.Stdout, report)
		}
		return printFingerprint(os.Stdout, report)
	},
}

func init() {
	rootCmd.AddCommand(fingerprintCmd)
	fingerprintCmd.Flags().BoolVar(&fingerprintJSONFlag, "json", false,
		"Emit machine-readable JSON instead of the human-readable summary")
}

// printFingerprintJSON writes the engine's machine-readable fingerprint
// JSON to w, followed by a trailing newline for shell-friendliness. It
// is the JSON equivalent of printFingerprint.
func printFingerprintJSON(w io.Writer, r *engine.FingerprintReport) error {
	data, err := engine.MarshalFingerprintJSON(r)
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	_, err = w.Write([]byte("\n"))
	return err
}

// printFingerprint writes a human-readable analysis for a single
// resource: the raw inputs that fed the fingerprint, the current
// digest, and either a "matches current" note or a "stale" note that
// steers the user to `wrk link`. When the workspace path is not a
// symlink into shared storage (detached, or never linked), pinned
// is called out explicitly rather than showing an empty digest.
func printFingerprint(w io.Writer, r *engine.FingerprintReport) error {
	fmt.Fprintf(w, "Resource:   %s (%s)\n", r.Resource.Name, r.Resource.Path)
	fmt.Fprintln(w, "Fingerprint inputs:")
	for _, in := range r.Current.Inputs {
		exists := "missing"
		if in.Exists {
			exists = "exists"
		}
		fmt.Fprintf(w, "  %-24s  %-7s  %d B\n", in.Path, exists, in.SizeBytes)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Current variant:  %s\n", displayFingerprint(r.Current.Fingerprint))
	if r.Isolated {
		fmt.Fprintln(w, "Pinned variant:   (isolated — this workspace pins a private variant; fingerprint comparison does not apply)")
	} else if r.Pinned.Fingerprint == "" {
		fmt.Fprintln(w, "Pinned variant:   (workspace path is not a symlink into shared storage)")
	} else if r.Changed {
		fmt.Fprintf(w, "Pinned variant:   %s  (stale)\n", r.Pinned.Fingerprint)
	} else {
		fmt.Fprintf(w, "Pinned variant:   %s  (matches current)\n", r.Pinned.Fingerprint)
	}
	if r.Changed && r.Pinned.Fingerprint != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Run `wrk link` to re-point this workspace at the current variant.")
	}
	return nil
}

// displayFingerprint renders a fingerprint string for human output,
// falling back to "-" when the digest is empty. Callers use this only
// for Current (Pinned has its own dedicated "not a symlink" branch).
func displayFingerprint(fp string) string {
	if fp == "" {
		return "-"
	}
	return strings.TrimSpace(fp)
}
