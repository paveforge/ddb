package cli

import (
	"fmt"
	"github.com/spf13/cobra"
	"io"
)

var Version = "dev"
var Commit = "unknown"
var Date = "unknown"

func New(out, errOut io.Writer) *cobra.Command {
	root := &cobra.Command{Use: "ddb", Short: "Streaming DynamoDB import and export tools", SilenceUsage: true, SilenceErrors: true}
	root.SetOut(out)
	root.SetErr(errOut)
	root.AddCommand(exportCommand())
	root.AddCommand(&cobra.Command{Use: "version", Args: cobra.NoArgs, Run: func(c *cobra.Command, _ []string) {
		fmt.Fprintf(c.OutOrStdout(), "ddb %s (commit %s, built %s)\n", Version, Commit, Date)
	}})
	return root
}
