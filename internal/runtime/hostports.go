package runtime

import (
	"fmt"
	"os"
	"strings"
)

// Host-published ports for the app and optional SearXNG compose services.
// These match docker-compose.yml / docker-compose.dev.yml defaults when the
// `searxng` profile is enabled.
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

// CheckAppSearxngHostPortCollision fails when an explicitly configured host
// SEARXNG_PORT would collide with APP_PORT (after applying the APP_PORT
// compose default of 8080).
//
// An empty searxngPort means local SearXNG host publishing is not configured
// (standalone app, or SearXNG deployed independently). In that case the check
// is a no-op — optional-service defaults must not reject a valid APP_PORT.
//
// Docker allows collisions silently: the app publishes 0.0.0.0:<port> while
// SearXNG publishes 127.0.0.1:<same port>. On Linux, localhost then hits
// SearXNG first, so POST /api/v1/auth/login returns SearXNG's HTML 404.
func CheckAppSearxngHostPortCollision(appPort, searxngPort string) error {
	sx := strings.TrimSpace(searxngPort)
	if sx == "" {
		return nil
	}
	app := resolveHostPort(appPort, DefaultAppHostPort)
	if app == sx {
		return fmt.Errorf("SEARXNG_PORT (%s) collides with APP_PORT (%s); SearXNG would steal localhost:%s (HTML 404 instead of the app). Keep SEARXNG_PORT at %s or another free port", sx, app, app, DefaultSearxngHostPort)
	}
	return nil
}

// CheckAppSearxngHostPortsFromEnv applies CheckAppSearxngHostPortCollision to
// APP_PORT and SEARXNG_PORT from the process environment. Unset SEARXNG_PORT
// skips the check (local SearXNG not enabled on this host path).
func CheckAppSearxngHostPortsFromEnv() error {
	return CheckAppSearxngHostPortCollision(os.Getenv("APP_PORT"), os.Getenv("SEARXNG_PORT"))
}
