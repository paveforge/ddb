package cli

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/paveforge/go-ddb-cli/internal/awsconfig"
	"github.com/paveforge/go-ddb-cli/internal/report"
	"github.com/paveforge/go-ddb-cli/internal/transfer"
	"github.com/spf13/cobra"
)

type common struct {
	aws                               awsconfig.Options
	format, compression, file, report string
	retry                             transfer.Retry
	reportFile                        *os.File
}

func (c *common) flags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&c.aws.Profile, "profile", "", "AWS configuration profile")
	f.StringVar(&c.aws.Region, "region", "", "AWS region")
	f.StringVar(&c.aws.Endpoint, "endpoint-url", "", "DynamoDB endpoint override")
	f.StringVar(&c.format, "format", "dynamodb-jsonl", "dynamodb-jsonl or json")
	f.StringVar(&c.compression, "compression", "auto", "auto (.gz), none, or gzip")
	f.StringVar(&c.file, "file", "", "Alias for input/output path")
	f.StringVar(&c.report, "report", "", "Write a JSON report to a new file, or - for stdout")
	f.IntVar(&c.retry.Attempts, "retry-attempts", 8, "Maximum attempts per request/batch")
	f.DurationVar(&c.retry.Timeout, "retry-timeout", 2*time.Minute, "Maximum elapsed time per request/batch")
}
func (c *common) preflight(path string) error {
	if err := c.retry.Validate(); err != nil {
		return report.Fail(2, "preflight", err)
	}
	if c.format != "json" && c.format != "dynamodb-jsonl" {
		return report.Fail(2, "preflight", fmt.Errorf("unsupported format"))
	}
	if _, err := transfer.Compression(path, c.compression); err != nil {
		return report.Fail(2, "preflight", err)
	}
	if c.report != "" && c.report != "-" {
		f, e := report.NewFile(c.report)
		if e != nil {
			return report.Fail(4, "preflight", e)
		}
		c.reportFile = f
	}
	return nil
}
func (c *common) finish(cmd *cobra.Command, s *report.Summary, start time.Time, err *error) {
	s.Finish(start, *err)
	s.Print(cmd.ErrOrStderr())
	var w io.Writer
	if c.report == "-" {
		w = cmd.OutOrStdout()
	} else if c.reportFile != nil {
		w = c.reportFile
	}
	if w != nil {
		if e := s.Write(w); e != nil {
			*err = fmt.Errorf("%w; write report: %v", nonNil(*err), e)
		}
		if c.reportFile != nil {
			if e := c.reportFile.Close(); e != nil {
				*err = fmt.Errorf("%w; close report: %v", nonNil(*err), e)
			}
		}
	}
}
func nonNil(err error) error {
	if err != nil {
		return err
	}
	return report.Fail(4, "report", fmt.Errorf("report finalization failed"))
}
func pathAlias(path, alias string) (string, error) {
	if path != "" && alias != "" {
		return "", report.Fail(2, "preflight", fmt.Errorf("use one path option or --file, not both"))
	}
	if path == "" {
		path = alias
	}
	if path == "" {
		return "", report.Fail(2, "preflight", fmt.Errorf("file path is required"))
	}
	return path, nil
}
func progress(cmd *cobra.Command) func(report.Summary) {
	last := time.Now()
	return func(s report.Summary) {
		if time.Since(last) >= time.Second {
			fmt.Fprintf(cmd.ErrOrStderr(), "phase=%s confirmed=%d scanned=%d retries=%d\n", s.Phase, s.Confirmed, s.Scanned, s.Retries)
			last = time.Now()
		}
	}
}
func exportCommand() *cobra.Command {
	var c common
	var o transfer.ExportOptions
	cmd := &cobra.Command{Use: "export", Short: "Export an existing table to a local file", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) (err error) {
		start := time.Now()
		s := report.Summary{Command: "export", Table: o.Table, Endpoint: c.aws.Endpoint, Phase: "preflight"}
		defer c.finish(cmd, &s, start, &err)
		if o.Table == "" {
			return report.Fail(2, "preflight", fmt.Errorf("--table is required"))
		}
		o.Output, err = pathAlias(o.Output, c.file)
		if err != nil {
			return err
		}
		if err = c.preflight(o.Output); err != nil {
			return err
		}
		api, region, e := awsconfig.Load(cmd.Context(), c.aws)
		if e != nil {
			return report.Fail(2, "preflight", e)
		}
		s.Region = region
		o.Format = c.format
		o.Compression = c.compression
		o.Retry = c.retry
		o.Progress = progress(cmd)
		return transfer.Export(cmd.Context(), api, o, &s)
	}}
	c.flags(cmd)
	f := cmd.Flags()
	f.StringVar(&o.Table, "table", "", "Source table name")
	f.StringVar(&o.Output, "output", "", "Output file path")
	f.BoolVar(&o.Force, "force", false, "Replace an existing completed output")
	f.BoolVar(&o.Consistent, "consistent-read", false, "Use strongly consistent reads (not a snapshot)")
	f.Int32Var(&o.PageSize, "page-size", 100, "Items evaluated per scan request")
	f.Float64Var(&o.ReadRate, "read-rate", 0, "Maximum scan requests/second (0 unlimited)")
	return cmd
}
