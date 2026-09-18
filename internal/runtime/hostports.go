package runtime

import (
	"fmt"
	"os"
	"strings"
)

// Host-published ports for the app and optional SearXNG compose services.
// These match docker-compose.yml / docker-compose.dev.yml defaults.
const (
	DefaultAppHostPort     = "8080"
	DefaultSearxngHostPort = "8888"
)

func resolveHostPort(raw, fallback string) string {
	if s := strings.TrimSpace(raw); s != "" {
		return s
	}
	return fallback
}

// CheckAppSearxngHostPortCollision fails when the host-published APP_PORT and
// SEARXNG_PORT would collide after applying compose defaults (8080 vs 8888).
//
// Docker allows this silently: the app publishes 0.0.0.0:<port> while SearXNG
// publishes 127.0.0.1:<same port>. On Linux, localhost then hits SearXNG first,
// so POST /api/v1/auth/login returns SearXNG's HTML 404 instead of the app.
func CheckAppSearxngHostPortCollision(appPort, searxngPort string) error {
	app := resolveHostPort(appPort, DefaultAppHostPort)
	sx := resolveHostPort(searxngPort, DefaultSearxngHostPort)
	if app == sx {
		return fmt.Errorf("SEARXNG_PORT (%s) collides with APP_PORT (%s); SearXNG would steal localhost:%s (HTML 404 instead of the app). Keep SEARXNG_PORT at %s or another free port", sx, app, app, DefaultSearxngHostPort)
	}
	return nil
}

// CheckAppSearxngHostPortsFromEnv applies CheckAppSearxngHostPortCollision to
// APP_PORT and SEARXNG_PORT from the process environment.
func CheckAppSearxngHostPortsFromEnv() error {
	return CheckAppSearxngHostPortCollision(os.Getenv("APP_PORT"), os.Getenv("SEARXNG_PORT"))
}
