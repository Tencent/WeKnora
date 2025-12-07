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

	// 该方法用于构建依赖注入容器，负责按需注册和管理所有应用所需的服务、组件和资源，便于后续通过依赖注入获取应用各部分实例。
	c := container.BuildContainer(runtime.GetContainer())

	// 启动 gin 框架，如果发生错误则进行异常退出处理
	err := c.Invoke(func(
		cfg *config.Config,
		router *gin.Engine,
		tracer *tracing.Tracer,
		resourceCleaner interfaces.ResourceCleaner,
	) error {
		// 创建用于资源清理的 context
		shutdownTimeout := cfg.Server.ShutdownTimeout
		if shutdownTimeout == 0 {
			shutdownTimeout = 30 * time.Second
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cleanupCancel()

		// 注册跟踪器清理函数
		resourceCleaner.RegisterWithName("Tracer", func() error {
			return tracer.Cleanup(cleanupCtx)
		})

		// 创建 HTTP server
		server := &http.Server{
			Addr:    fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
			Handler: router,
		}

		// 监听系统信号，用于优雅关闭
		ctx, done := context.WithCancel(context.Background())
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
		go func() {
			sig := <-signals
			log.Printf("收到信号: %v，开始优雅关闭服务器...", sig)

			// 优雅关闭 server
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer shutdownCancel()

			if err := server.Shutdown(shutdownCtx); err != nil {
				log.Fatalf("服务器强制关闭: %v", err)
			}

			// 清理所有已注册的资源
			log.Println("清理资源中...")
			errs := resourceCleaner.Cleanup(cleanupCtx)
			if len(errs) > 0 {
				log.Printf("资源清理期间发生错误: %v", errs)
			}

			log.Println("服务器已退出")
			done()
		}()

		// 启动 Gin 服务
		log.Printf("服务器启动中，监听地址 %s:%d", cfg.Server.Host, cfg.Server.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			// 启动 gin 失败，执行异常退出处理
			return fmt.Errorf("启动服务器失败: %v", err)
		}

		// 等待优雅退出信号
		<-ctx.Done()
		return nil
	})

	if err != nil {
		log.Fatalf("应用启动失败: %v", err)
	}
}
