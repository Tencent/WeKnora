package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/Tencent/WeKnora/internal/container"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/sandbox"
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
	// The helper a sandboxed plugin runs in (WEKNORA_PLUGIN_NETNS).
	if len(os.Args) > 1 && os.Args[1] == sandbox.Subcommand {
		return sandbox.Main(os.Args[2:]), true
	}
	return 0, false
}
