// Package cmd wires the cobra command tree and translates a --fail-on gate into
// a process exit code.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// version is stamped at build time via -ldflags "-X .../cmd.version=...".
var version = "dev"

// exitError carries a specific process exit code up to Execute without printing
// a spurious "error:" line (used by the --fail-on gate).
type exitError struct{ code int }

func (e *exitError) Error() string { return fmt.Sprintf("exit code %d", e.code) }

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "aws-lambda-runtime-eol",
		Short:         "Flag AWS Lambda functions on deprecated or soon-to-be end-of-life runtimes",
		Long:          "aws-lambda-runtime-eol scans your Lambda functions and reports which ones run a runtime that is already past its AWS end-of-life date, or will be within a chosen window.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}
	root.AddCommand(newScanCmd())
	return root
}

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	if err := newRootCmd().Execute(); err != nil {
		if ec, ok := err.(*exitError); ok {
			return ec.code
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}
