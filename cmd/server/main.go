// Package main is the main package for the WeKnora server
// It contains the main function and the entry point for the server
//
// @title           WeKnora API
// @version         1.0
// @description     WeKnora 知识库管理系统 API 文档
// @termsOfService  http://swagger.io/terms/
//
// @contact.name   WeKnora Github
// @contact.url    https://github.com/Tencent/WeKnora
//
// @BasePath  /api/v1
//
// @securityDefinitions.apikey Bearer
// @in header
// @name Authorization
// @description 用户登录认证：输入 Bearer {token} 格式的 JWT 令牌

// @securityDefinitions.apikey ApiKeyAuth
// @in header
// @name X-API-Key
// @description API Key 认证：空间 Key 固定访问所属空间；平台 Key 调用空间接口时需同时传 X-Tenant-ID
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/container"
	"github.com/Tencent/WeKnora/internal/envfile"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/runtime"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func main() {
	// Load dotenv configuration before any container provider reads it.
	// Explicit ENV_FILE is honored on every build; automatic .env.lite
	// discovery is scoped to Lite builds (internal/handler.Edition is
	// injected at build time), so a standard binary built in the repo root
	// never picks up Lite defaults. Process variables remain authoritative.
	if err := envfile.Load(handler.Edition == "lite"); err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v\n", err)
	}
	// Dotenv loading happens after the logger package initialized, so
	// re-apply file-based LOG_LEVEL / LOG_PATH (mirrors cmd/desktop).
	logger.ConfigureFromEnv()
	// Set Gin mode
	if os.Getenv("GIN_MODE") == "release" {
		gin.SetMode(gin.ReleaseMode)
	} else {
		gin.SetMode(gin.DebugMode)
	}
	// Mute Gin's per-route registration spam (one line per route × ~150
	// routes) — replaced by a single summary printed after router build.
	runtime.SilenceGinRouteSpam()
	// Print the env banner before container build so operators see what
	// config landed even when DB / storage init fails.
	runtime.LogStartupEnv(context.Background())
	runtime.MarkServerStarted()

	// Build dependency injection container
	c := container.BuildContainer(runtime.GetContainer())

	// One-shot bootstrap hooks (e.g. promote env-named user to system
	// admin). Best-effort: never aborts startup — see bootstrap.go.
	runStartupBootstrap(c)

	// Run application
	err := c.Invoke(func(
		cfg *config.Config,
		router *gin.Engine,
		resourceCleaner interfaces.ResourceCleaner,
		systemSettingSvc interfaces.SystemSettingService,
		modelCatalogSvc *service.ModelCatalogService,
	) error {
		// Create HTTP server
		server := &http.Server{
			Handler: router,
		}

		addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
		listener, err := listenWithRetry(addr, 10, 300*time.Millisecond)
		if err != nil {
			return fmt.Errorf("failed to start server: %v", err)
		}

		ctx, done := context.WithCancel(context.Background())
		modelCatalogSvc.Start(ctx)

		// Start the system_settings pubsub subscriber. Runs in its own
		// goroutine and exits when ctx is cancelled at shutdown. Best-
		// effort: an error here only warns (Redis may legitimately be
		// disabled in lite-mode deployments — the service no-ops in
		// that case anyway).
		if err := systemSettingSvc.SubscribeRedis(ctx); err != nil {
			logger.Warnf(ctx, "[system_settings] subscribe failed: %v", err)
		}

		signals := make(chan os.Signal, 1)
		signal.Notify(signals, shutdownSignals...)
		go func() {
			sig := <-signals
			logger.Infof(context.Background(), "Received signal: %v, starting server shutdown...", sig)

			drainBudget, cleanupBudget := runtime.ShutdownBudgets(cfg.Server.ShutdownTimeout)
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), drainBudget)
			defer shutdownCancel()

			// Second signal → force close all connections immediately.
			go func() {
				sig := <-signals
				logger.Warnf(context.Background(), "Received second signal: %v, forcing shutdown...", sig)
				server.Close()
			}()

			// Do NOT close the listener manually before Shutdown. Serve would
			// return the raw accept error ("use of closed network connection")
			// instead of ErrServerClosed, and logger.Fatalf → os.Exit(1) would
			// kill the process before ResourceCleaner runs. Shutdown closes
			// listeners first, so the port is released before the drain.
			if err := server.Shutdown(shutdownCtx); err != nil {
				logger.Errorf(context.Background(), "Server forced to shutdown: %v", err)
				server.Close()
			}

			logger.Info(context.Background(), "Cleaning up resources...")
			// Fresh context sized from the reserved slice of ShutdownTimeout.
			// Drain must not hand cleanup an already-expired context, and the
			// two slices must still add up to one supervisor grace period.
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), cleanupBudget)
			defer cleanupCancel()
			errs := resourceCleaner.Cleanup(cleanupCtx)
			if len(errs) > 0 {
				logger.Errorf(context.Background(), "Errors occurred during resource cleanup: %v", errs)
			}
			logger.Info(context.Background(), "Server has exited")
			done()
		}()

		runtime.LogGinRouteCount(context.Background())
		logger.Infof(context.Background(), "Server is running at %s", addr)
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("server error: %v", err)
		}

		<-ctx.Done()
		return nil
	})
	if err != nil {
		logger.Fatalf(context.Background(), "Failed to run application: %v", err)
	}
}
