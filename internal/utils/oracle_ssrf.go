package utils

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strconv"

	go_ora "github.com/sijms/go-ora/v2"
)

// oracleSSRFDialer routes go-ora connections through SSRFSafeDialContext so
// the Oracle engine gets the same dial-time SSRF checks as Doris/MySQL.
type oracleSSRFDialer struct{}

func (oracleSSRFDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return SSRFSafeDialContext(ctx, network, addr)
}

// OracleConnParams describes an Oracle AI Database connection.
type OracleConnParams struct {
	Addr     string // "host:port"; port defaults to 1521
	Service  string // service name, e.g. FREEPDB1
	Username string
	Password string
	// UseTLS switches to TCPS with certificate verification.
	UseTLS bool
	// Wallet is an uploaded auto-login wallet (base64 cwallet.sso, see
	// NormalizeOracleWallet). Optional; enables mTLS, as Autonomous Database
	// requires by default.
	Wallet string
	// WalletDir is a directory containing cwallet.sso. Only for
	// operator-controlled env config (ORACLE_WALLET_DIR); Wallet wins if both set.
	WalletDir string
}

// OpenOracleDB opens a *sql.DB with the go-ora thin driver (no Instant Client
// needed). ssrfSafe routes dials through SSRFSafeDialContext; pass true for
// user-supplied configs and false only for operator-controlled env config.
// The caller owns pool settings and Close.
func OpenOracleDB(p OracleConnParams, ssrfSafe bool) (*sql.DB, error) {
	host, portStr, err := net.SplitHostPort(p.Addr)
	if err != nil {
		host, portStr = p.Addr, "1521"
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return nil, fmt.Errorf("invalid oracle port in addr %q", p.Addr)
	}
	if p.Wallet != "" {
		if p.WalletDir, err = OracleWalletDir(p.Wallet); err != nil {
			return nil, err
		}
	}
	var opts map[string]string
	if p.UseTLS || p.WalletDir != "" {
		opts = map[string]string{"SSL": "true", "SSL VERIFY": "true"}
		if p.WalletDir != "" {
			opts["WALLET"] = p.WalletDir
		}
	}
	connector := go_ora.NewConnector(go_ora.BuildUrl(host, port, p.Service, p.Username, p.Password, opts))
	if ssrfSafe {
		connector.(*go_ora.OracleConnector).Dialer(oracleSSRFDialer{})
	}
	return sql.OpenDB(connector), nil
}
