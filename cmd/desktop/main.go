//go:build !bindings

package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
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
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/handler/session"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/runtime"
	"github.com/Tencent/WeKnora/internal/stream"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/joho/godotenv"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// dragHandlerJS is injected into the webview on DomReady.
// It bypasses Wails' built-in CSS-variable-based drag detection (which uses
// getComputedStyle and has timing/inheritance issues with dynamic SPA content)
// and instead uses robust DOM-traversal via el.closest() plus a Y-position
// fallback for the top 40px of a layout container, including its children.
// The "drag"
// message is sent directly through the WKWebView script-message bridge,
// which the native Objective-C handler in WailsContext.m converts to
// [NSWindow performWindowDragWithEvent:].
const dragHandlerJS = `(function(){
if(window.__wkDragBound)return;
window.__wkDragBound=true;
document.documentElement.classList.add('wails-desktop');

// Disable rubber-band overscroll that reveals the dark window background
document.documentElement.style.overscrollBehavior='none';
document.body.style.overscrollBehavior='none';

if(window.wails&&window.wails.flags){
  window.wails.flags.cssDragProperty='__disabled__';
  window.wails.flags.cssDragValue='__never__';
}

// Prevent native text/image drag-out to fix the "selected and dragged away" issue
window.addEventListener('dragstart', function(e){
  e.preventDefault();
}, true);

var TITLEBAR_H=40;

// We specifically look for Wails' inline style attributes injected by Vue,
// and custom drag classes, avoiding generic headers like .section-header.
// .chat-topbar is the window top on the conversation page.
var dragSel='.logo_row,.menu_top,.chat-topbar,.drag-region,[data-wails-drag],' +
  '[style*="--wails-draggable: drag"],[style*="--wails-draggable:drag"]';

var noDragSel='button,a,input,select,textarea,[role="button"],' +
  '.t-button,.t-input,.t-select,.t-textarea,' +
  '.header-actions,.header-action-btn,.sidebar-toggle,.logo_box,' +
  '.close-btn,.menu_item,.submenu,.submenu_item,.menu_bottom,' +
  '.t-popup,.t-dropdown,.t-tooltip,.t-dialog,[data-no-drag],' +
  '[style*="--wails-draggable: no-drag"],[style*="--wails-draggable:no-drag"]';

var layoutClasses=['main','chat','dialogue-wrap','kb-list-container','kb-list-content',
  'agent-list-container','agent-list-content','org-list-container','org-list-content','aside_box',
  'ks-container','ks-content','settings-overlay','knowledge-layout',
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

function inTitlebar(el,y){
  if(y>TITLEBAR_H)return false;
  var node=el;
  while(node&&node instanceof Element){
    if(isLayoutEl(node))return true;
    node=node.parentElement;
  }
  return false;
}

function shouldDrag(el,y){
  if(!(el instanceof Element))return false;
  if(el.closest(noDragSel))return false;
  if(el.closest(dragSel))return true;
  if(inTitlebar(el,y))return true;
  return false;
}

window.addEventListener('mousedown',function(e){
  var target=e.target;
  if(target&&target.nodeType===Node.TEXT_NODE){
    target=target.parentElement;
  }
  if(e.button!==0||e.detail!==1)return;
  if(!shouldDrag(target,e.clientY))return;
  // The session title renames on double-click. Wait until the pointer
  // actually moves so that click still lands; other top-bar hits drag now.
  var defer=target.closest&&target.closest('.chat-header__title');
  if(!defer){
    e.preventDefault();
    sendDrag();
    return;
  }
  var x=e.clientX,y=e.clientY;
  function move(ev){
    if(Math.abs(ev.clientX-x)<4&&Math.abs(ev.clientY-y)<4)return;
    cleanup();
    sendDrag();
  }
  function up(){cleanup();}
  function cleanup(){
    window.removeEventListener('mousemove',move,true);
    window.removeEventListener('mouseup',up,true);
  }
  window.addEventListener('mousemove',move,true);
  window.addEventListener('mouseup',up,true);
},true);

// Intercept external link clicks and window.open so they open in the system browser
document.addEventListener('click',function(e){
  var el=e.target;
  while(el&&el.tagName!=='A')el=el.parentElement;
  if(!el||!el.href)return;
  var href=el.href;
  if(href.indexOf('http://')===0||href.indexOf('https://')===0){
    if(window.runtime&&window.runtime.BrowserOpenURL){
      e.preventDefault();
      e.stopPropagation();
      window.runtime.BrowserOpenURL(href);
    }
  }
},true);

var origOpen=window.open;
window.open=function(url){
  if(url&&(typeof url==='string')&&(url.indexOf('http://')===0||url.indexOf('https://')===0)){
    if(window.runtime&&window.runtime.BrowserOpenURL){
      window.runtime.BrowserOpenURL(url);
      return null;
    }
  }
  return origOpen.apply(window,arguments);
};
})();`

// wailsThemeSyncJS：与 index.html 首屏一致，在 DomReady 再跑一遍，覆盖 Ctrl+R 后 runtime 就绪时机
const wailsThemeSyncJS = `(function(){try{var t=localStorage.getItem('WeKnora_theme')||'light';if(t==='system')t=window.matchMedia('(prefers-color-scheme: dark)').matches?'dark':'light';var bg=t==='dark'?'#181818':'#eee';document.documentElement.setAttribute('theme-mode',t);document.documentElement.style.background=bg;document.documentElement.style.minHeight='100%';document.documentElement.style.colorScheme=t==='dark'?'dark':'light';if(document.body){document.body.style.background=bg;document.body.style.minHeight='100%';}var w=window.runtime;if(!w)return;if(t==='dark'){if(w.WindowSetDarkTheme)w.WindowSetDarkTheme();if(w.WindowSetBackgroundColour)w.WindowSetBackgroundColour(24,24,24,255);}else{if(w.WindowSetLightTheme)w.WindowSetLightTheme();if(w.WindowSetBackgroundColour)w.WindowSetBackgroundColour(238,238,238,255);}}catch(e){}})()`

const weknoraGitHubRepoURL = "https://github.com/Tencent/WeKnora"

// ensureDesktopLiteEdition marks this process as Lite. cmd/desktop is the
// Lite app; packaged builds also inject this via ldflags. wails dev often
// does not, and auto-setup / the embedded SPA / capabilities still key off
// the string. Host sandbox is gated by the desktop build tag, not this.
func ensureDesktopLiteEdition() {
	handler.Edition = "lite"
}

func main() {
	ensureDesktopLiteEdition()

	// For macOS .app bundle, the working directory is usually "/" or the MacOS folder.
	// We need to change the working directory to the Resources folder where our configs are.
	execPath, errPath := os.Executable()
	// A packaged .app keeps config under Contents/Resources. wails dev also
	// runs from a .app, but that bundle has no copied config; stay in the
	// repo and use config/ there.
	if errPath == nil && strings.Contains(execPath, ".app/Contents/MacOS") {
		resPath := filepath.Join(filepath.Dir(filepath.Dir(execPath)), "Resources")
		if _, err := os.Stat(filepath.Join(resPath, "config", "config.yaml")); err == nil {
			_ = os.Chdir(resPath)
		}
	}
	if _, err := os.Stat(filepath.Join("config", "config.yaml")); os.IsNotExist(err) {
		// wails dev 的 cwd 是 cmd/desktop。仓库配置在上两级。
		repoRoot := filepath.Clean(filepath.Join("..", ".."))
		if _, err := os.Stat(filepath.Join(repoRoot, "config", "config.yaml")); err == nil {
			_ = os.Chdir(repoRoot)
		}
	}

	// Load .env explicitly for the desktop app so DB_DRIVER gets loaded
	_ = godotenv.Load()
	// wails dev picks up the repo .env, which may ask for Redis streams
	// without REDIS_ADDR. go-redis then dials localhost:6379 and aborts
	// startup. Lite has no Redis; keep the in-memory stream manager.
	if strings.TrimSpace(os.Getenv("REDIS_ADDR")) == "" &&
		strings.EqualFold(strings.TrimSpace(os.Getenv("STREAM_MANAGER_TYPE")), stream.TypeRedis) {
		_ = os.Setenv("STREAM_MANAGER_TYPE", stream.TypeMemory)
	}
	configureDesktopStorage(execPath)
	configureDesktopFileStorage(execPath)
	logger.ConfigureFromEnv()

	// Set Gin mode
	if os.Getenv("GIN_MODE") == "release" {
		gin.SetMode(gin.ReleaseMode)
	} else {
		gin.SetMode(gin.DebugMode)
	}
	// Mute Gin's per-route registration spam; replaced by a single
	// summary printed after router build.
	runtime.SilenceGinRouteSpam()
	// Provision the signing key first so the startup banner reflects it
	// instead of warning about a key the desktop is about to create.
	if err := ensureDesktopSigningKey(); err != nil {
		panic(fmt.Sprintf("initialize desktop signing key: %v", err))
	}
	runtime.LogStartupEnv(context.Background())

	// Build dependency injection container
	c := container.BuildContainer(runtime.GetContainer())
	if err := c.Decorate(func(container.HostApprovalModeLoader) container.HostApprovalModeLoader {
		return LoadApprovalMode
	}); err != nil {
		panic(fmt.Sprintf("wire desktop approval mode: %v", err))
	}
	if err := c.Decorate(func(session.HostProjectDirsLoader) session.HostProjectDirsLoader {
		return LoadProjectDirs
	}); err != nil {
		panic(fmt.Sprintf("wire desktop project dirs: %v", err))
	}

	// Initialize the WeKnora App struct
	app := NewApp()
	setupBytes := make([]byte, 32)
	if _, err := rand.Read(setupBytes); err != nil {
		panic("failed to generate desktop authentication capability")
	}
	app.setupToken = base64.RawURLEncoding.EncodeToString(setupBytes)
	handler.SetLiteSetupToken(app.setupToken)
	handler.SetHostProjectPicker(app.PickProjectDir)

	// Error channel to capture server startup errors
	serverErrCh := make(chan error, 1)

	// Run backend in a separate goroutine
	go func() {
		err := c.Invoke(func(
			cfg *config.Config,
			router *gin.Engine,
			resourceCleaner interfaces.ResourceCleaner,
		) error {
			server := &http.Server{Handler: router}

			runtime.LogGinRouteCount(context.Background())

			// 127.0.0.1 + saved port from settings (desktop-prefs.json), or :0 for random free port.
			addr := desktopBackendListenAddr()

			listener, err := listenWithRetry(addr, 10, 300*time.Millisecond)
			if err != nil {
				return fmt.Errorf("failed to start server: %v", err)
			}

			tcpAddr := listener.Addr().(*net.TCPAddr)
			port := tcpAddr.Port
			app.listenPublic = LoadDesktopHTTPBindPublic()
			// Reverse proxy and webview API calls always use loopback; avoid 0.0.0.0 / [::] as dial target.
			app.backendURL = fmt.Sprintf("http://127.0.0.1:%d", port)
			app.apiLanBaseURL = ""
			if app.listenPublic {
				if ip := desktopPreferredLANIPv4(); ip != nil {
					app.apiLanBaseURL = fmt.Sprintf("http://%s:%d/api/v1", ip.String(), port)
				}
			}

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

			logger.Infof(context.Background(), "Server is running at %s (proxy -> %s)", tcpAddr.String(), app.backendURL)
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
		if app.ctx == nil {
			return
		}
		choice, err := wailsruntime.MessageDialog(app.ctx, wailsruntime.MessageDialogOptions{
			Type:          wailsruntime.InfoDialog,
			Title:         "WeKnora Lite",
			Message:       fmt.Sprintf("WeKnora Lite — Desktop Edition\n\nA RAG framework for document understanding and semantic Q&A over complex, heterogeneous content.\n\nVersion %s\n© 2026 Tencent\n\nGitHub:\n%s", desktopAboutVersion(), weknoraGitHubRepoURL),
			Buttons:       []string{"Open GitHub", "OK"},
			DefaultButton: "OK",
		})
		if err != nil {
			logger.Warnf(context.Background(), "About dialog: %v", err)
			return
		}
		if choice == "Open GitHub" {
			wailsruntime.BrowserOpenURL(app.ctx, weknoraGitHubRepoURL)
		}
	})
	FileMenu.AddText("Check for Updates...", nil, func(_ *menu.CallbackData) {
		if app.ctx == nil {
			return
		}
		checkUpdate(app.ctx, desktopAboutVersion(), true, false)
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
			wailsruntime.EventsEmit(app.ctx, "app:reload")
		}
	})

	// Wait for the backend URL to be set
	targetURL, _ := url.Parse(app.backendURL)
	proxy := desktopAPIProxy(targetURL)

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
			wailsruntime.WindowExecJS(ctx, wailsThemeSyncJS)
			wailsruntime.WindowExecJS(ctx, dragHandlerJS)
			// 注入真实 API 根路径（与 window.location.origin 不同）；无 Go 绑定时仍可显示。
			if u := strings.TrimSpace(app.backendURL); u != "" {
				apiRoot := strings.TrimRight(u, "/") + "/api/v1"
				inject := fmt.Sprintf(`try{window.__WEKNORA_API_BASE__=%s}catch(e){}`, strconv.Quote(apiRoot))
				wailsruntime.WindowExecJS(ctx, inject)
			}
			if lan := strings.TrimSpace(app.apiLanBaseURL); lan != "" {
				injectLan := fmt.Sprintf(`try{window.__WEKNORA_API_LAN_BASE__=%s}catch(e){}`, strconv.Quote(lan))
				wailsruntime.WindowExecJS(ctx, injectLan)
			}
		},
		OnShutdown: app.shutdown,
		Bind: []interface{}{
			app,
		},
		BackgroundColour: &options.RGBA{R: 255, G: 255, B: 255, A: 255},
		Mac: &mac.Options{
			TitleBar:             mac.TitleBarHiddenInset(),
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
	})
	if err != nil {
		println("Error:", err.Error())
	}
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

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		logger.Warnf(context.Background(), "Failed to create desktop DB directory %s: %v", filepath.Dir(dbPath), err)
	}

	_ = os.Setenv("DB_PATH", dbPath)
}

// configureDesktopFileStorage points Lite file uploads at ~/.weknora/data/files.
// wails dev is not always inside a packaged .app, so this runs for every
// desktop process. An explicit absolute directory is kept; the Docker default
// /data/files is not writable on macOS and is replaced. Uploads move over from
// wherever the previous build kept them.
func configureDesktopFileStorage(execPath string) {
	home, err := os.UserHomeDir()
	if err != nil {
		logger.Warnf(context.Background(), "Failed to resolve home dir for file storage: %v", err)
		return
	}
	filesPath := desktopLocalFilesDir(home)
	raw := strings.TrimSpace(os.Getenv("LOCAL_STORAGE_BASE_DIR"))
	if !useDesktopFilesDir(raw) {
		return
	}
	if appSupport, err := defaultMacAppSupportDir(execPath); err == nil {
		migrateDesktopFiles(legacyDesktopFilesDir(raw, appSupport), filesPath)
	}
	if err := os.MkdirAll(filesPath, 0o755); err != nil {
		logger.Warnf(context.Background(), "Failed to create desktop files directory %s: %v", filesPath, err)
		return
	}
	_ = os.Setenv("LOCAL_STORAGE_BASE_DIR", filesPath)
}

func desktopLocalFilesDir(home string) string {
	return filepath.Join(home, ".weknora", "data", "files")
}

func useDesktopFilesDir(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return true
	}
	cleaned := filepath.Clean(trimmed)
	switch cleaned {
	case "/data/files", "data/files", filepath.Join(".", "data", "files"):
		return true
	default:
		return !filepath.IsAbs(cleaned)
	}
}

// legacyDesktopFilesDir is where the previous build stored uploads: raw
// resolved under the bundle's Application Support dir, or its data/files
// default when raw is empty or the absolute Docker default.
func legacyDesktopFilesDir(raw, appSupportDir string) string {
	if filepath.IsAbs(strings.TrimSpace(raw)) {
		raw = ""
	}
	return resolveDesktopDataPath(raw, filepath.Join("data", "files"), appSupportDir)
}

func migrateDesktopFiles(oldDir, newDir string) {
	if filepath.Clean(oldDir) == filepath.Clean(newDir) {
		return
	}
	info, err := os.Stat(oldDir)
	if err != nil || !info.IsDir() {
		return
	}
	if _, err := os.Stat(newDir); err == nil {
		logger.Warnf(context.Background(),
			"Desktop files already exist at %s; leaving %s in place", newDir, oldDir)
		return
	}
	if err := os.MkdirAll(filepath.Dir(newDir), 0o755); err != nil {
		logger.Warnf(context.Background(), "Failed to create %s: %v", filepath.Dir(newDir), err)
		return
	}
	if err := os.Rename(oldDir, newDir); err != nil {
		logger.Warnf(context.Background(), "Failed to migrate desktop files from %s to %s: %v", oldDir, newDir, err)
	}
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

// desktopAPIProxy forwards the webview to the loopback Gin server. Pairing
// needs the API listener as Host so the link cannot be aimed at another
// machine. Other routes keep the page Host (wails.localhost), which OIDC
// callbacks and embed origin checks still compare against.
func desktopAPIProxy(target *url.URL) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			r.SetXForwarded()
			if !isBrowserPairingRequest(r.In) {
				r.Out.Host = r.In.Host
			}
		},
	}
}

func isBrowserPairingRequest(req *http.Request) bool {
	return req != nil && req.URL != nil && req.Method == http.MethodPost && req.URL.Path == "/api/v1/me/browser"
}

// desktopBackendListenAddr returns the TCP address for the embedded Gin server (Wails desktop).
// Binds 127.0.0.1 by default, or 0.0.0.0 when http_bind_public is set in desktop-prefs.json.
func desktopBackendListenAddr() string {
	host := "127.0.0.1"
	if LoadDesktopHTTPBindPublic() {
		host = "0.0.0.0"
	}
	pref := LoadDesktopPrefsHTTPPort()
	if pref >= 1 && pref <= 65535 {
		return net.JoinHostPort(host, strconv.Itoa(pref))
	}
	return net.JoinHostPort(host, "0")
}

// desktopPreferredLANIPv4 picks a non-loopback IPv4 for “other devices on this network” URL hints.
func desktopPreferredLANIPv4() net.IP {
	ifaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, aerr := iface.Addrs()
			if aerr != nil {
				continue
			}
			for _, a := range addrs {
				ipNet, ok := a.(*net.IPNet)
				if !ok || ipNet.IP == nil {
					continue
				}
				ip4 := ipNet.IP.To4()
				if ip4 == nil || ip4.IsLoopback() {
					continue
				}
				if ip4.IsPrivate() {
					return ip4
				}
			}
		}
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, aerr := iface.Addrs()
			if aerr != nil {
				continue
			}
			for _, a := range addrs {
				ipNet, ok := a.(*net.IPNet)
				if !ok || ipNet.IP == nil {
					continue
				}
				ip4 := ipNet.IP.To4()
				if ip4 == nil || ip4.IsLoopback() || ip4.IsUnspecified() {
					continue
				}
				return ip4
			}
		}
	}
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return nil
	}
	defer conn.Close()
	la, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || la.IP == nil {
		return nil
	}
	return la.IP.To4()
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
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		listener, err := net.Listen("tcp", addr)
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
