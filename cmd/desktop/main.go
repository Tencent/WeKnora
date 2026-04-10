package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/container"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/runtime"
	"github.com/Tencent/WeKnora/internal/tracing"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"golang.org/x/sys/unix"
)

func main() {
	// Set Gin mode
	if os.Getenv("GIN_MODE") == "release" {
		gin.SetMode(gin.ReleaseMode)
	} else {
		gin.SetMode(gin.DebugMode)
	}

	// Build dependency injection container
	c := container.BuildContainer(runtime.GetContainer())

	// Initialize the WeKnora App struct
	app := NewApp()

	// Error channel to capture server startup errors
	serverErrCh := make(chan error, 1)

	// Run backend in a separate goroutine
	go func() {
		err := c.Invoke(func(
			cfg *config.Config,
			router *gin.Engine,
			tracer *tracing.Tracer,
			resourceCleaner interfaces.ResourceCleaner,
		) error {
			server := &http.Server{Handler: router}
			
			// Use port 0 to assign a random free port for the desktop app
			// Force localhost binding to prevent firewall popups on macOS
			addr := "127.0.0.1:0"

			listener, err := listenWithRetry(addr, 10, 300*time.Millisecond)
			if err != nil {
				return fmt.Errorf("failed to start server: %v", err)
			}
			
			// Get the actual assigned port
			actualPort := listener.Addr().(*net.TCPAddr).Port
			actualAddr := fmt.Sprintf("127.0.0.1:%d", actualPort)
			
			// Store the backend URL so Wails can load it
			app.backendURL = fmt.Sprintf("http://%s", actualAddr)

			// Handle graceful shutdown from Wails OnShutdown hook
			go func() {
				<-app.shutdownCh
				logger.Infof(context.Background(), "Wails shutting down, stopping Go backend...")
				
				listener.Close()
				shutdownTimeout := cfg.Server.ShutdownTimeout
				if shutdownTimeout == 0 {
					shutdownTimeout = 30 * time.Second
				}
				shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
				defer cancel()
				
				if err := server.Shutdown(shutdownCtx); err != nil {
					server.Close()
				}
				resourceCleaner.Cleanup(shutdownCtx)
			}()

			// Also listen for OS signals just in case
			signals := make(chan os.Signal, 1)
			signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-signals
				app.shutdownCh <- struct{}{} // trigger shutdown
			}()

			logger.Infof(context.Background(), "Server is running at %s", actualAddr)
			if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
				return fmt.Errorf("server error: %v", err)
			}
			return nil
		})
		
		if err != nil {
			serverErrCh <- err
			logger.Fatalf(context.Background(), "Failed to run backend: %v", err)
		}
	}()

	// Give the server a moment to start and determine its port
	time.Sleep(500 * time.Millisecond)

	// Create application with options
	// macOS app menu
	AppMenu := menu.NewMenu()
	FileMenu := AppMenu.AddSubmenu("WeKnora Lite")
	FileMenu.AddText("About WeKnora", keys.CmdOrCtrl("i"), func(_ *menu.CallbackData) {
		println("WeKnora Lite Desktop App")
	})
	FileMenu.AddSeparator()
	FileMenu.AddText("Quit", keys.CmdOrCtrl("q"), func(_ *menu.CallbackData) {
		app.shutdown(context.Background())
		os.Exit(0)
	})

	EditMenu := AppMenu.AddSubmenu("Edit")
	EditMenu.AddText("Undo", keys.CmdOrCtrl("z"), func(_ *menu.CallbackData) {})
	EditMenu.AddText("Redo", keys.CmdOrCtrl("y"), func(_ *menu.CallbackData) {})
	EditMenu.AddSeparator()
	EditMenu.AddText("Cut", keys.CmdOrCtrl("x"), func(_ *menu.CallbackData) {})
	EditMenu.AddText("Copy", keys.CmdOrCtrl("c"), func(_ *menu.CallbackData) {})
	EditMenu.AddText("Paste", keys.CmdOrCtrl("v"), func(_ *menu.CallbackData) {})
	EditMenu.AddText("Select All", keys.CmdOrCtrl("a"), func(_ *menu.CallbackData) {})

	// Start Wails application
	err := wails.Run(&options.App{
		Title:  "WeKnora Lite",
		Width:  1280,
		Height: 800,
		Menu:   AppMenu,
		AssetServer: &assetserver.Options{
			Assets: nil,
		},
		StartHidden: false, // Show window on startup
		OnStartup:   app.startup,
		OnShutdown:  app.shutdown,
		Bind: []interface{}{
			app,
		},
		Mac: &mac.Options{
			TitleBar:             mac.TitleBarHiddenInset(), // Beautiful borderless titlebar
			Appearance:           mac.NSAppearanceNameDarkAqua,
			WebviewIsTransparent: true,
			WindowIsTranslucent:  true,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}

// App struct
type App struct {
	ctx        context.Context
	backendURL string
	shutdownCh chan struct{}
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{
		shutdownCh: make(chan struct{}, 1),
	}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	
	// Navigate the Wails webview to our local Go backend
	// We use the wails runtime to change the URL programmatically
	go func() {
		// Navigate the Wails webview to our local Go backend
		// Wait a tiny bit just to ensure window is ready
		time.Sleep(100 * time.Millisecond)
		// Instead of WindowNavigate, we change the URL via javascript
		wailsruntime.WindowExecJS(a.ctx, fmt.Sprintf("window.location.href = '%s';", a.backendURL))
	}()
}

func (a *App) shutdown(ctx context.Context) {
	a.shutdownCh <- struct{}{}
}

func listenWithRetry(addr string, maxRetries int, baseDelay time.Duration) (net.Listener, error) {
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, unix.SO_REUSEPORT, 1)
			})
		},
	}
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		listener, err := lc.Listen(context.Background(), "tcp", addr)
		if err == nil {
			return listener, nil
		}
		lastErr = err
		if i < maxRetries-1 {
			delay := baseDelay * time.Duration(1<<uint(i))
			if delay > 3*time.Second { delay = 3 * time.Second }
			time.Sleep(delay)
		}
	}
	return nil, lastErr
}
