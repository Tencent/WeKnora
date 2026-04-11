package main

import (
	"context"
	"strings"
)

// App holds Wails-bound state for the desktop shell.
type App struct {
	ctx           context.Context
	backendURL    string
	apiLanBaseURL string
	listenPublic  bool
	shutdownCh    chan struct{}
}

// NewApp creates a new App application struct.
func NewApp() *App {
	return &App{
		shutdownCh: make(chan struct{}, 1),
	}
}

// startup is called when the application starts.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *App) shutdown(ctx context.Context) {
	a.shutdownCh <- struct{}{}
}

// GetAPIBaseURL returns the local HTTP base URL for REST API calls (e.g. http://127.0.0.1:PORT/api/v1).
// The desktop shell proxies the webview to this address; window.location.origin is not the API host.
func (a *App) GetAPIBaseURL() string {
	if a.backendURL == "" {
		return ""
	}
	return strings.TrimRight(a.backendURL, "/") + "/api/v1"
}

// GetDesktopHTTPPortSetting returns the saved local API port (0 = random port each launch).
func (a *App) GetDesktopHTTPPortSetting() int {
	return LoadDesktopPrefsHTTPPort()
}

// SetDesktopHTTPPortSetting saves the preferred local API port to application support. Restart the app for it to take effect unless it matches the current listener.
func (a *App) SetDesktopHTTPPortSetting(port int) error {
	return SaveDesktopHTTPPortPreference(port)
}

// GetDesktopHTTPBindPublicSetting returns whether API listens on all interfaces (0.0.0.0).
func (a *App) GetDesktopHTTPBindPublicSetting() bool {
	return LoadDesktopHTTPBindPublic()
}

// SetDesktopHTTPBindPublicSetting saves LAN/public listen preference. Restart the app for it to take effect.
func (a *App) SetDesktopHTTPBindPublicSetting(v bool) error {
	return SaveDesktopHTTPBindPublicPreference(v)
}

// GetAPILanBaseURL returns a suggested base URL for other devices on the LAN (…/api/v1), or empty if not in bind-public mode or IP detection failed.
func (a *App) GetAPILanBaseURL() string {
	return a.apiLanBaseURL
}

// GetDesktopListenPublicActive is true when this session’s API server is listening on all interfaces (runtime), not the saved preference.
func (a *App) GetDesktopListenPublicActive() bool {
	return a.listenPublic
}
