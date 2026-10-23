package main

import (
	"context"
	"fmt"
	"github.com/paveforge/ddb/internal/cli"
	"github.com/paveforge/ddb/internal/report"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() { <-ctx.Done(); cancel() }()
	if err := cli.New(os.Stdout, os.Stderr).ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(report.ExitCode(err))
	}
}
