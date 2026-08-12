package utils

import (
	"context"
	"net"
	"sync"

	"github.com/go-sql-driver/mysql"
)

// MySQLSSRFNetwork is the mysql driver network name registered by
// RegisterMySQLSSRFDialer. DSNs must set cfg.Net to this value so every
// database/sql open uses SSRFSafeDialContext at the final TCP sink.
const MySQLSSRFNetwork = "tcp-weknora-ssrf"

var mysqlSSRFDialerOnce sync.Once

// RegisterMySQLSSRFDialer installs a go-sql-driver/mysql dialer that routes
// connections through SSRFSafeDialContext. Safe to call from multiple packages;
// registration happens at most once per process.
func RegisterMySQLSSRFDialer() {
	mysqlSSRFDialerOnce.Do(func() {
		mysql.RegisterDialContext(MySQLSSRFNetwork, func(ctx context.Context, addr string) (net.Conn, error) {
			return SSRFSafeDialContext(ctx, "tcp", addr)
		})
	})
}
