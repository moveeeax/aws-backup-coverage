// Package cmd wires the aws-backup-coverage CLI.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// version is stamped via -ldflags at release time; defaults to a dev marker.
var version = "dev"

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "aws-backup-coverage",
		Short:         "Find AWS resources not covered by any AWS Backup plan",
		Long:          "aws-backup-coverage lists backup-eligible AWS resources (EBS, RDS, DynamoDB, EFS),\nchecks which are selected by an AWS Backup plan, and reports uncovered and stale gaps.",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newCheckCmd())
	return root
}

// Execute runs the CLI and returns a process exit code. Code 3 signals that a
// --fail-on threshold was crossed; 1 is any other error.
func Execute() int {
	if err := newRootCmd().Execute(); err != nil {
		if _, ok := err.(gateError); ok {
			return 3
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

// gateError is returned when findings cross the --fail-on threshold.
type gateError struct{ msg string }

func (e gateError) Error() string { return e.msg }
