package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	sandboxTerminalTicketType = "sandbox_terminal"
	// DefaultSandboxTerminalTicketTTL is long enough for the WebSocket
	// handshake, short enough that a leaked query string is not a 24h
	// session. After upgrade the ticket itself is discarded; the open PTY
	// rechecks the bound access-token id instead.
	DefaultSandboxTerminalTicketTTL = 2 * time.Minute
)

// SandboxTerminalTicketClaims is the handshake identity bound to one session.
// TokenID is the auth_tokens.id of the access JWT that minted the ticket;
// the open PTY rechecks that row so logout / revocation tear the bridge down.
type SandboxTerminalTicketClaims struct {
	UserID    string
	TenantID  uint64
	SessionID string
	TokenID   string
}

// IssueSandboxTerminalTicket mints a short-lived JWT for the browser
// WebSocket handshake. It is not an access token: ValidateToken rejects it.
func IssueSandboxTerminalTicket(userID string, tenantID uint64, sessionID, tokenID string, ttl time.Duration) (string, error) {
	userID = strings.TrimSpace(userID)
	sessionID = strings.TrimSpace(sessionID)
	tokenID = strings.TrimSpace(tokenID)
	if userID == "" || sessionID == "" || tokenID == "" || tenantID == 0 {
		return "", errors.New("sandbox terminal ticket requires user, tenant, session, and access token")
	}
	if ttl <= 0 {
		ttl = DefaultSandboxTerminalTicketTTL
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id":    userID,
		"tenant_id":  tenantID,
		"session_id": sessionID,
		"token_id":   tokenID,
		"type":       sandboxTerminalTicketType,
		"exp":        time.Now().Add(ttl).Unix(),
		"iat":        time.Now().Unix(),
	})
	return token.SignedString([]byte(getJwtSecret()))
}

// ParseSandboxTerminalTicket validates a handshake ticket. Access and
// refresh tokens are rejected.
func ParseSandboxTerminalTicket(raw string) (*SandboxTerminalTicketClaims, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("missing terminal ticket")
	}
	token, err := jwt.Parse(raw, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(getJwtSecret()), nil
	})
	if err != nil || token == nil || !token.Valid {
		return nil, errors.New("invalid or expired terminal ticket")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("invalid terminal ticket claims")
	}
	tokenType, _ := claims["type"].(string)
	if tokenType != sandboxTerminalTicketType {
		return nil, errors.New("not a terminal ticket")
	}
	userID, _ := claims["user_id"].(string)
	sessionID, _ := claims["session_id"].(string)
	tokenID, _ := claims["token_id"].(string)
	tenantID := tenantIDFromClaims(claims, 0)
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(tokenID) == "" || tenantID == 0 {
		return nil, errors.New("invalid terminal ticket claims")
	}
	return &SandboxTerminalTicketClaims{
		UserID:    userID,
		TenantID:  tenantID,
		SessionID: sessionID,
		TokenID:   tokenID,
	}, nil
}
