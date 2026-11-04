package runtime

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/utils"
)

// SilenceGinRouteSpam mutes Gin's per-route "[GIN-debug] METHOD path -->
// handler" lines that flood the startup log with one entry per registered
// route (we have ~150 of them). DebugMode features (panic recovery
// stacktraces, runtime warnings) are preserved.
//
// In place of the per-route spam, prints a single summary line at the
// end of router build with the route count.
//
// Call once at the start of main(), before container.BuildContainer.
func SilenceGinRouteSpam() {
	var count int64
	gin.DebugPrintRouteFunc = func(httpMethod, absolutePath, handlerName string, nuHandlers int) {
		atomic.AddInt64(&count, 1)
	}
	// gin.DebugPrintFunc handles non-route debug lines (e.g. "Listening
	// and serving HTTP"). Route them through the structured logger so
	// they share the same format as the rest of startup.
	gin.DebugPrintFunc = func(format string, values ...interface{}) {
		msg := strings.TrimRight(fmt.Sprintf(format, values...), "\n")
		// Drop the redundant "Creating an Engine instance with the Logger
		// and Recovery middleware already attached" — purely informational.
		if strings.Contains(msg, "Creating an Engine instance") {
			return
		}
		logger.Info(context.Background(), "[gin] "+msg)
	}
	// Expose a way to print the suppressed count once routes are wired up.
	ginRouteCount = &count
}

// ginRouteCount is set by SilenceGinRouteSpam and read by LogGinRouteCount
// so callers can print a one-line summary after router build.
var ginRouteCount *int64

// LogGinRouteCount writes a single summary line for the number of routes
// Gin registered. Safe to call even if SilenceGinRouteSpam wasn't called
// (it just becomes a no-op).
func LogGinRouteCount(ctx context.Context) {
	if ginRouteCount == nil {
		return
	}
	logger.Infof(ctx, "[gin] registered %d routes", atomic.LoadInt64(ginRouteCount))
}

// envVarSpec describes one env var to surface in the startup banner.
type envVarSpec struct {
	name      string
	sensitive bool // if true, only presence/length is printed, never value
}

// startupEnvVars lists the env vars whose presence is most worth
// surfacing at boot — primarily security-sensitive ones whose silent
// absence has caused real incidents (SYSTEM_AES_KEY rotation, JWT secret
// drift) plus the basic DB / storage selectors that change behaviour
// significantly.
//
// Keep the list short — this banner exists to answer "is the config I
// expect actually loaded?" at a glance, not to dump the entire env.
var startupEnvVars = []envVarSpec{
	// Security
	{name: "SYSTEM_AES_KEY", sensitive: true},
	{name: "SYSTEM_SIGNING_KEY", sensitive: true},
	{name: "JWT_SECRET", sensitive: true},
	// Runtime
	{name: "GIN_MODE"},
	{name: "AUTO_MIGRATE"},
	// Database
	{name: "DB_DRIVER"},
	{name: "DB_HOST"},
	{name: "DB_PORT"},
	{name: "DB_USER"},
	{name: "DB_NAME"},
	{name: "DB_PATH"}, // sqlite
	{name: "DB_PASSWORD", sensitive: true},
	// Cache / queue
	{name: "REDIS_ADDR"},
	{name: "REDIS_PASSWORD", sensitive: true},
	{name: "REDIS_USE_TLS"},
	{name: "REDIS_TLS_SERVER_NAME"},
	// Object storage
	{name: "STORAGE_TYPE"},
	{name: "MINIO_ENDPOINT"},
	{name: "MINIO_BUCKET_NAME"},
	{name: "MINIO_SECRET_ACCESS_KEY", sensitive: true},
	{name: "TOS_ENDPOINT"},
	{name: "TOS_BUCKET_NAME"},
	{name: "TOS_SECRET_KEY", sensitive: true},
	// External services
	{name: "DOCREADER_ADDR"},
	{name: "RETRIEVE_DRIVER"},
}

// LogStartupEnv prints a single banner block summarising the curated set
// of env vars in startupEnvVars. Sensitive values are reported as
// "set (N chars)" — the goal is to give the operator confidence the
// config they expected actually landed, without echoing secrets to logs.
//
// Output format (one line per var to stay grep-able):
//
//	[startup-env] SYSTEM_AES_KEY=set (32 chars)
//	[startup-env] JWT_SECRET=<unset>
//	[startup-env] DB_DRIVER=postgres
//
// Special call-outs are printed afterwards for misconfigurations that
// would silently degrade behaviour (e.g. SYSTEM_AES_KEY set but not
// decodable to 32 bytes is treated as unset by crypto.GetAESKey — easy
// to miss without a loud warning; see ValidateStartupEnv, which turns
// that case into a fatal startup error instead of a warning).
func LogStartupEnv(ctx context.Context) {
	// Sort by name for deterministic output.
	specs := make([]envVarSpec, len(startupEnvVars))
	copy(specs, startupEnvVars)
	sort.Slice(specs, func(i, j int) bool { return specs[i].name < specs[j].name })

	logger.Info(ctx, "[startup-env] resolved environment:")
	for _, s := range specs {
		val := os.Getenv(s.name)
		logger.Infof(ctx, "[startup-env]   %s=%s", s.name, formatEnvValue(s, val))
	}

	// Targeted warnings for footguns. SYSTEM_AES_KEY set but not decodable
	// to 32 bytes is the most common one — utils.GetAESKey() falls back
	// to nil (== "no encryption"). ValidateStartupEnv (called by main
	// right after this banner) turns it into a fatal error; the warning
	// stays so the banner alone still names the problem.
	if k := os.Getenv("SYSTEM_AES_KEY"); k != "" && utils.GetAESKey() == nil {
		logger.Warnf(ctx,
			"[startup-env] SYSTEM_AES_KEY is set but does not decode to 32 bytes (%d chars); "+
				"AES-256 requires exactly 32 bytes — encryption is DISABLED",
			len(k))
	}
	// Upgrades from example configs keep the published example AES key,
	// which SystemHMACKey rejects — embed sessions and presigned links then
	// fail at request time with no hint at boot.
	if len(utils.SystemHMACKey()) == 0 {
		logger.Warn(ctx,
			"[startup-env] no usable signing key: set SYSTEM_SIGNING_KEY (e.g. `openssl rand -hex 32`) — "+
				"embed sessions and presigned file links are DISABLED. SYSTEM_AES_KEY is only used as a fallback "+
				"when it is at least 16 chars and not an example value; do not change it to fix this")
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("REDIS_TLS_INSECURE_SKIP_VERIFY")), "true") {
		logger.Warn(ctx,
			"[startup-env] REDIS_TLS_INSECURE_SKIP_VERIFY=true — Redis TLS certificate verification is DISABLED; do not use in production")
	}
}

// ValidateStartupEnv rejects configurations that would silently degrade
// security. Currently it covers one case:
//
//   - SYSTEM_AES_KEY is set (non-empty) but does not decode to 32 bytes
//     under utils.GetAESKey: every Value() hook then skips encryption and
//     credentials are written to the database in plaintext. A refused
//     boot is recoverable in seconds; plaintext credentials sitting in a
//     database backup are not.
//
// An unset SYSTEM_AES_KEY is still allowed (fresh installs and
// deployments that intentionally run without at-rest encryption keep
// today's behaviour); only a set-but-invalid value is fatal. Callers in
// cmd/server and cmd/desktop run this right after LogStartupEnv and exit
// non-zero on error, before any credential is written.
func ValidateStartupEnv() error {
	if raw := os.Getenv("SYSTEM_AES_KEY"); raw != "" && utils.GetAESKey() == nil {
		return fmt.Errorf(
			"SYSTEM_AES_KEY is set (%d chars) but does not decode to 32 bytes; "+
				"refusing to start rather than write credentials in plaintext: "+
				"set it to a raw 32-byte value, `openssl rand -hex 16` (32 chars), "+
				"a 64-char hex encoding, or a base64 encoding of 32 bytes",
			len(raw))
	}
	return nil
}

func formatEnvValue(s envVarSpec, val string) string {
	if val == "" {
		return "<unset>"
	}
	if s.sensitive {
		return fmt.Sprintf("set (%d chars)", len(val))
	}
	return val
}
