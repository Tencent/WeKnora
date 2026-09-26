package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/Tencent/WeKnora/internal/container"
	"github.com/Tencent/WeKnora/internal/logger"
)

// runPluginHost is `WeKnora plugin-host`: a standalone plugin host that
// runs host plugins for the app nodes (see container.RunPluginHost).
func runPluginHost() int {
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals...)
	defer stop()
	if err := container.RunPluginHost(ctx); err != nil {
		logger.Errorf(context.Background(), "[plugin-host] %v", err)
		return 1
	}
	return 0
}

// subcommand runs a subcommand named on the command line, if any.
func subcommand() (code int, ran bool) {
	if len(os.Args) > 1 && os.Args[1] == "plugin-host" {
		return runPluginHost(), true
	}
	return 0, false
}
