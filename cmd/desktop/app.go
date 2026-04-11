package main

import (
	"context"
	"strings"
)

// App holds Wails-bound state for the desktop shell.
type App struct {
	ctx        context.Context
	backendURL string
	shutdownCh chan struct{}
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
