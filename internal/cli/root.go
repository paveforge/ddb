package cli

import (
	"fmt"
	"github.com/paveforge/ddb/internal/report"
	"github.com/spf13/cobra"
	"io"
	"runtime/debug"
)

var Version = "dev"
var Commit = "unknown"
var Date = "unknown"

func New(out, errOut io.Writer) *cobra.Command {
	root := &cobra.Command{Use: "ddb", Short: "Streaming DynamoDB import and export tools", SilenceUsage: true, SilenceErrors: true}
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return report.Fail(2, "flags", err) })
	root.SetOut(out)
	root.SetErr(errOut)
	root.AddCommand(exportCommand(), importCommand())
	root.AddCommand(&cobra.Command{Use: "version", Short: "Show build version", Args: cobra.NoArgs, Run: func(c *cobra.Command, _ []string) {
		version, commit, date := Version, Commit, Date
		if info, ok := debug.ReadBuildInfo(); ok {
			version, commit, date = buildMetadata(info)
		}
		fmt.Fprintf(c.OutOrStdout(), "ddb %s (commit %s, built %s)\n", version, commit, date)
	}})
	return root
}

func buildMetadata(info *debug.BuildInfo) (string, string, string) {
	version, commit, date := Version, Commit, Date
	if version == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		version = info.Main.Version
	}
	for _, setting := range info.Settings {
		if commit == "unknown" && setting.Key == "vcs.revision" {
			commit = setting.Value
		}
		if date == "unknown" && setting.Key == "vcs.time" {
			date = setting.Value
		}
	}
	return version, commit, date
}
