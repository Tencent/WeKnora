package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
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
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/joho/godotenv"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/unix"
)

// dragHandlerJS is injected into the webview on DomReady.
// It bypasses Wails' built-in CSS-variable-based drag detection (which uses
// getComputedStyle and has timing/inheritance issues with dynamic SPA content)
// and instead uses robust DOM-traversal via el.closest() plus a Y-position
// fallback for the macOS title-bar region on layout containers. The "drag"
// message is sent directly through the WKWebView script-message bridge,
// which the native Objective-C handler in WailsContext.m converts to
// [NSWindow performWindowDragWithEvent:].
const dragHandlerJS = `(function(){
if(window.__wkDragBound)return;
window.__wkDragBound=true;

if(window.wails&&window.wails.flags){
  window.wails.flags.cssDragProperty='__disabled__';
  window.wails.flags.cssDragValue='__never__';
}

var TITLEBAR_H=38;

var dragSel='.logo_row,.menu_top,.header,.header-title,.title-row,' +
  '.dialogue-title,.section-header,.dialog-header,.sidebar-header,' +
  '.document-header,.drag-region,[data-wails-drag]';

var noDragSel='button,a,input,select,textarea,[role="button"],' +
  '.t-button,.t-input,.t-select,.t-textarea,' +
  '.header-actions,.header-action-btn,.sidebar-toggle,.logo_box,' +
  '.close-btn,.menu_item,.submenu,.submenu_item,.menu_bottom,' +
  '.t-popup,.t-dropdown,.t-tooltip,.t-dialog,[data-no-drag]';

var layoutClasses=['main','chat','dialogue-wrap','kb-list-container',
  'agent-list-container','org-list-container','aside_box',
  'ks-container','settings-overlay','knowledge-layout',
  'faq-manager-wrapper','login-layout'];

function sendDrag(){
  try{window.webkit.messageHandlers.external.postMessage('drag')}
  catch(_){try{window.WailsInvoke('drag')}catch(e){}}
}

function isLayoutEl(el){
  for(var i=0;i<layoutClasses.length;i++){
    if(el.classList.contains(layoutClasses[i]))return true;
  }
  var tag=el.tagName;
  return tag==='BODY'||tag==='HTML';
}

function shouldDrag(el,y){
  if(!(el instanceof Element))return false;
  if(el.closest(noDragSel))return false;
  if(el.closest(dragSel))return true;
  if(y<=TITLEBAR_H&&isLayoutEl(el))return true;
  return false;
}

window.addEventListener('mousedown',function(e){
  var target=e.target;
  if(target&&target.nodeType===Node.TEXT_NODE){
    target=target.parentElement;
  }
  if(e.button!==0||e.detail!==1)return;
  if(!shouldDrag(target,e.clientY))return;
  e.preventDefault();
  sendDrag();
},true);
})();`

func main() {
	// For macOS .app bundle, the working directory is usually "/" or the MacOS folder.
	// We need to change the working directory to the Resources folder where our configs are.
	execPath, errPath := os.Executable()
	if errPath == nil && strings.Contains(execPath, ".app/Contents/MacOS") {
		resPath := filepath.Join(filepath.Dir(filepath.Dir(execPath)), "Resources")
		_ = os.Chdir(resPath)
	}

	// Load .env explicitly for the desktop app so DB_DRIVER gets loaded
	_ = godotenv.Load()
	configureDesktopStorage(execPath)
	logger.ConfigureFromEnv()

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

	AppMenu.Append(menu.EditMenu())

	ViewMenu := AppMenu.AddSubmenu("View")
	ViewMenu.AddText("Reload", keys.CmdOrCtrl("r"), func(_ *menu.CallbackData) {
		if app.ctx != nil {
			wailsruntime.WindowReloadApp(app.ctx)
		}
	})

	// Wait for the backend URL to be set
	targetURL, _ := url.Parse(app.backendURL)
	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	// Start Wails application
	// We use a Reverse Proxy to seamlessly proxy Wails' frontend to our Go backend
	err := wails.Run(&options.App{
		Title:         "WeKnora Lite",
		Width:         1280,
		Height:        800,
		DisableResize: false,
		Menu:          AppMenu,
		AssetServer: &assetserver.Options{
			Handler: proxy,
		},
		StartHidden: false, // Show window on startup
		OnStartup:   app.startup,
		OnDomReady: func(ctx context.Context) {
			wailsruntime.WindowExecJS(ctx, dragHandlerJS)
		},
		OnShutdown: app.shutdown,
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
}

func (a *App) shutdown(ctx context.Context) {
	a.shutdownCh <- struct{}{}
}

func configureDesktopStorage(execPath string) {
	if execPath == "" || !strings.Contains(execPath, ".app/Contents/MacOS") {
		return
	}

	appSupportDir, err := defaultMacAppSupportDir(execPath)
	if err != nil {
		logger.Warnf(context.Background(), "Failed to resolve app support dir: %v", err)
		return
	}

	legacyResourcesDir := filepath.Join(filepath.Dir(filepath.Dir(execPath)), "Resources")
	targetDataDir := filepath.Join(appSupportDir, "data")
	migrateLegacyDesktopData(legacyResourcesDir, targetDataDir)

	dbPath := resolveDesktopDataPath(os.Getenv("DB_PATH"), filepath.Join("data", "weknora.db"), appSupportDir)
	filesPath := resolveDesktopDataPath(os.Getenv("LOCAL_STORAGE_BASE_DIR"), filepath.Join("data", "files"), appSupportDir)

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		logger.Warnf(context.Background(), "Failed to create desktop DB directory %s: %v", filepath.Dir(dbPath), err)
	}
	if err := os.MkdirAll(filesPath, 0o755); err != nil {
		logger.Warnf(context.Background(), "Failed to create desktop files directory %s: %v", filesPath, err)
	}

	_ = os.Setenv("DB_PATH", dbPath)
	_ = os.Setenv("LOCAL_STORAGE_BASE_DIR", filesPath)
}

func defaultMacAppSupportDir(execPath string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	appName := "WeKnora Lite"
	if idx := strings.Index(execPath, ".app/Contents/MacOS"); idx >= 0 {
		bundleName := filepath.Base(execPath[:idx+4])
		if trimmed := strings.TrimSuffix(bundleName, ".app"); trimmed != "" {
			appName = trimmed
		}
	}

	return filepath.Join(homeDir, "Library", "Application Support", appName), nil
}

func resolveDesktopDataPath(rawPath, defaultRelativePath, appSupportDir string) string {
	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" {
		trimmed = defaultRelativePath
	}
	if filepath.IsAbs(trimmed) {
		return filepath.Clean(trimmed)
	}
	trimmed = strings.TrimPrefix(trimmed, "."+string(filepath.Separator))
	return filepath.Join(appSupportDir, filepath.Clean(trimmed))
}

func migrateLegacyDesktopData(resourcesDir, targetDataDir string) {
	legacyDataDir := filepath.Join(resourcesDir, "data")
	if info, err := os.Stat(legacyDataDir); err != nil || !info.IsDir() {
		return
	}
	if _, err := os.Stat(targetDataDir); err == nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(targetDataDir), 0o755); err != nil {
		logger.Warnf(context.Background(), "Failed to create app support parent dir %s: %v", filepath.Dir(targetDataDir), err)
		return
	}
	if err := os.Rename(legacyDataDir, targetDataDir); err != nil {
		logger.Warnf(context.Background(), "Failed to migrate legacy desktop data from %s to %s: %v", legacyDataDir, targetDataDir, err)
		return
	}
	logger.Infof(context.Background(), "Migrated legacy desktop data to %s", targetDataDir)
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
			if delay > 3*time.Second {
				delay = 3 * time.Second
			}
			time.Sleep(delay)
		}
	}
	return nil, lastErr
}
