package container

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
)

// applyDBMaxOpenConns caps the main database pool at DB_MAX_OPEN_CONNS.
//
// Unset or 0 keeps database/sql's default of no limit. Without a cap, a burst
// of parallel requests opens one connection each and the server starts
// refusing them past max_connections ("sorry, too many clients already");
// with a cap, the excess requests wait for a free connection instead.
// Invalid values are logged and ignored so a typo cannot break startup.
func applyDBMaxOpenConns(ctx context.Context, sqlDB *sql.DB) {
	raw := strings.TrimSpace(os.Getenv("DB_MAX_OPEN_CONNS"))
	if raw == "" {
		return
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		logger.Warnf(ctx, "Ignoring invalid DB_MAX_OPEN_CONNS=%q: want a non-negative integer", raw)
		return
	}
	sqlDB.SetMaxOpenConns(n)
	if n > 0 {
		logger.Infof(ctx, "Database connection pool capped at %d open connections", n)
	}
}
