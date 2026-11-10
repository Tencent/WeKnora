// Command weknora is a CLI for Tencent WeKnora knowledge bases.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/Tencent/WeKnora/cli/cmd"
)

func main() {
	// Wire SIGINT/SIGTERM into the root context so long-running commands
	// (chat / agent invoke / doc wait) observe ctx.Done() and can run their
	// cancellation cleanup paths (e.g., re-emit the auto-created session id
	// so users can resume with --session). On signal-triggered cancellation
	// the process exits 130 regardless of what Execute returned — matches
	// the wire contract documented in cli/README.md "Exit codes".
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	rc := cmd.Execute(ctx)

	if ctx.Err() == context.Canceled {
		os.Exit(130)
	}
	os.Exit(rc)
}
