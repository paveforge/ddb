package main

import (
	"context"
	"fmt"
	"github.com/paveforge/go-ddb-cli/internal/cli"
	"github.com/paveforge/go-ddb-cli/internal/report"
	"os"
	"os/signal"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	go func() { <-ctx.Done(); cancel() }()
	if err := cli.New(os.Stdout, os.Stderr).ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(report.ExitCode(err))
	}
}
