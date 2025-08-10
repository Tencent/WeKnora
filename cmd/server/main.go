// Package main is the main package for the WeKnora server
// It contains the main function and the entry point for the server
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/container"
	"github.com/Tencent/WeKnora/internal/runtime"
	"github.com/Tencent/WeKnora/internal/tracing"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func main() {
	// Set log format with request ID
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)
	log.SetOutput(os.Stdout)

	// Set Gin mode
	if os.Getenv("GIN_MODE") == "release" {
		gin.SetMode(gin.ReleaseMode)
	} else {
		gin.SetMode(gin.DebugMode)
	}

	// Build dependency injection container
	c := container.BuildContainer(runtime.GetContainer())

	// Run application
	err := c.Invoke(func(
		cfg *config.Config,
		router *gin.Engine,
		tracer *tracing.Tracer,
		resourceCleaner interfaces.ResourceCleaner,
	) error {
		// Create context for resource cleanup
		shutdownTimeout := cfg.Server.ShutdownTimeout
		if shutdownTimeout == 0 {
			shutdownTimeout = 30 * time.Second
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cleanupCancel()

		// Register tracer cleanup function to resource cleaner
		resourceCleaner.RegisterWithName("Tracer", func() error {
			return tracer.Cleanup(cleanupCtx)
		})

		// Create HTTP server
		server := &http.Server{
			Addr:    fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
			Handler: router,
		}

		ctx, done := context.WithCancel(context.Background())
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
		go func() {
			sig := <-signals
			log.Printf("Received signal: %v, starting server shutdown...", sig)

			// Create a context with timeout for server shutdown
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer shutdownCancel()

			if err := server.Shutdown(shutdownCtx); err != nil {
				log.Fatalf("Server forced to shutdown: %v", err)
			}

			// Clean up all registered resources
			log.Println("Cleaning up resources...")
			errs := resourceCleaner.Cleanup(cleanupCtx)
			if len(errs) > 0 {
				log.Printf("Errors occurred during resource cleanup: %v", errs)
			}

			log.Println("Server has exited")
			done()
		}()

		// Start server
		log.Printf("Server is running at %s:%d", cfg.Server.Host, cfg.Server.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("failed to start server: %v", err)
		}

		// Wait for shutdown signal
		<-ctx.Done()
		return nil
	})
	if err != nil {
		log.Fatalf("Failed to run application: %v", err)
	}
}
