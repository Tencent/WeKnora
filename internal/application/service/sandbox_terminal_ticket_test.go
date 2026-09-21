package service

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func TestSandboxTerminalTicketRoundTrip(t *testing.T) {
	ticket, err := IssueSandboxTerminalTicket("user-1", 42, "sess-9", "tok-abc", time.Minute)
	require.NoError(t, err)
	require.NotEmpty(t, ticket)

	claims, err := ParseSandboxTerminalTicket(ticket)
	require.NoError(t, err)
	require.Equal(t, "user-1", claims.UserID)
	require.Equal(t, uint64(42), claims.TenantID)
	require.Equal(t, "sess-9", claims.SessionID)
	require.Equal(t, "tok-abc", claims.TokenID)
}

func TestSandboxTerminalTicketRejectsMalformedAndExpired(t *testing.T) {
	_, err := ParseSandboxTerminalTicket("not-a-jwt")
	require.Error(t, err)

	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id":    "user-1",
		"tenant_id":  1,
		"session_id": "sess",
		"token_id":   "tok-1",
		"type":       sandboxTerminalTicketType,
		"exp":        time.Now().Add(-time.Minute).Unix(),
	})
	raw, err := tok.SignedString([]byte(getJwtSecret()))
	require.NoError(t, err)
	_, err = ParseSandboxTerminalTicket(raw)
	require.Error(t, err)
}

func TestSandboxTerminalTicketRejectsAccessToken(t *testing.T) {
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id":    "user-1",
		"tenant_id":  1,
		"session_id": "sess",
		"token_id":   "tok-1",
		"exp":        time.Now().Add(time.Hour).Unix(),
	})
	raw, err := tok.SignedString([]byte(getJwtSecret()))
	require.NoError(t, err)
	_, err = ParseSandboxTerminalTicket(raw)
	require.Error(t, err)
}

func TestSandboxTerminalTicketRejectsEmptyFields(t *testing.T) {
	_, err := IssueSandboxTerminalTicket("", 1, "sess", "tok", time.Minute)
	require.Error(t, err)
	_, err = IssueSandboxTerminalTicket("user", 0, "sess", "tok", time.Minute)
	require.Error(t, err)
	_, err = IssueSandboxTerminalTicket("user", 1, "", "tok", time.Minute)
	require.Error(t, err)
	_, err = IssueSandboxTerminalTicket("user", 1, "sess", "", time.Minute)
	require.Error(t, err)
}

func TestSandboxTerminalTicketRejectsMissingTokenIDClaim(t *testing.T) {
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id":    "user-1",
		"tenant_id":  1,
		"session_id": "sess",
		"type":       sandboxTerminalTicketType,
		"exp":        time.Now().Add(time.Minute).Unix(),
	})
	raw, err := tok.SignedString([]byte(getJwtSecret()))
	require.NoError(t, err)
	_, err = ParseSandboxTerminalTicket(raw)
	require.Error(t, err)
}
