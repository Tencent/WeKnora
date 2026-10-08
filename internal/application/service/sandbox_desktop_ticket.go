// Package service provides one-shot handshake tickets for the sandbox desktop WebSocket.
//
// Unlike the terminal's ticket (sandbox_terminal_ticket.go) this is an opaque
// random string in a store, not a signed JWT. The desktop ticket must be
// single-use, and once a store is needed to enforce that, the stateless JWT's
// only advantage is gone — while its drawback (an HS256 blob sitting in
// gateway access logs and browser history for its whole TTL) remains. The
// terminal's implementation is deliberately left untouched.
package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/redis/go-redis/v9"
)

// DefaultSandboxDesktopTicketTTL is long enough for the WebSocket handshake,
// short enough that a leaked query string is not a session.
const DefaultSandboxDesktopTicketTTL = 2 * time.Minute

const desktopTicketRedisPrefix = "weknora:desktop-ticket:"

// ErrDesktopTicketInvalid covers unknown, expired, and already-consumed
// tickets. They are one error on purpose: telling them apart would let a
// caller probe which tickets ever existed.
var ErrDesktopTicketInvalid = errors.New("invalid or expired desktop ticket")

// SandboxDesktopTicketClaims is the handshake identity bound to one session.
// TokenID is the auth_tokens.id of the access JWT that minted the ticket, so
// logout and revocation can tear the relay down.
type SandboxDesktopTicketClaims struct {
	UserID    string `json:"user_id"`
	TenantID  uint64 `json:"tenant_id"`
	SessionID string `json:"session_id"`
	TokenID   string `json:"token_id"`
}

func (c SandboxDesktopTicketClaims) valid() bool {
	return strings.TrimSpace(c.UserID) != "" &&
		strings.TrimSpace(c.SessionID) != "" &&
		strings.TrimSpace(c.TokenID) != "" &&
		c.TenantID != 0
}

// SandboxDesktopTicketStore issues and consumes one-shot desktop tickets.
type SandboxDesktopTicketStore interface {
	Issue(ctx context.Context, claims SandboxDesktopTicketClaims) (string, error)
	Consume(ctx context.Context, ticket string) (*SandboxDesktopTicketClaims, error)
}

// NewSandboxDesktopTicketStore returns a Redis-backed store, or an in-process
// one when Redis is not configured.
//
// The memory fallback mirrors selectSessionBindingStore (container/sandbox.go):
// Lite mode has no REDIS_ADDR and runs one process, so a process-local store
// is correct there. In a multi-replica deployment without Redis the ticket
// would be minted on one replica and consumed on another, so this logs loudly.
func NewSandboxDesktopTicketStore(rdb *redis.Client) SandboxDesktopTicketStore {
	if rdb != nil {
		return &redisDesktopTicketStore{client: rdb, ttl: DefaultSandboxDesktopTicketTTL}
	}
	logger.Warnf(context.Background(),
		"[sandbox] No Redis configured, using in-memory desktop ticket store (single-instance)")
	return newMemoryDesktopTicketStore(DefaultSandboxDesktopTicketTTL)
}

func newDesktopTicketID() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

type redisDesktopTicketStore struct {
	client redis.UniversalClient
	ttl    time.Duration
}

func (s *redisDesktopTicketStore) Issue(
	ctx context.Context, claims SandboxDesktopTicketClaims,
) (string, error) {
	if !claims.valid() {
		return "", errors.New("desktop ticket requires user, tenant, session, and access token")
	}
	id, err := newDesktopTicketID()
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	if err := s.client.Set(ctx, desktopTicketRedisPrefix+id, payload, s.ttl).Err(); err != nil {
		return "", err
	}
	return id, nil
}

func (s *redisDesktopTicketStore) Consume(
	ctx context.Context, ticket string,
) (*SandboxDesktopTicketClaims, error) {
	ticket = strings.TrimSpace(ticket)
	if ticket == "" {
		return nil, ErrDesktopTicketInvalid
	}
	// GETDEL is the whole point: read and invalidate in one round trip, so
	// two concurrent handshakes cannot both win.
	raw, err := s.client.GetDel(ctx, desktopTicketRedisPrefix+ticket).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrDesktopTicketInvalid
	}
	if err != nil {
		return nil, err
	}
	var claims SandboxDesktopTicketClaims
	if err := json.Unmarshal(raw, &claims); err != nil || !claims.valid() {
		return nil, ErrDesktopTicketInvalid
	}
	return &claims, nil
}

type memoryDesktopTicketEntry struct {
	claims    SandboxDesktopTicketClaims
	expiresAt time.Time
}

type memoryDesktopTicketStore struct {
	mu      sync.Mutex
	tickets map[string]memoryDesktopTicketEntry
	ttl     time.Duration
}

func newMemoryDesktopTicketStore(ttl time.Duration) *memoryDesktopTicketStore {
	return &memoryDesktopTicketStore{
		tickets: make(map[string]memoryDesktopTicketEntry),
		ttl:     ttl,
	}
}

func (s *memoryDesktopTicketStore) Issue(
	_ context.Context, claims SandboxDesktopTicketClaims,
) (string, error) {
	if !claims.valid() {
		return "", errors.New("desktop ticket requires user, tenant, session, and access token")
	}
	id, err := newDesktopTicketID()
	if err != nil {
		return "", err
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	// Opportunistic sweep: tickets live 2 minutes and are issued one per
	// desktop open, so this never has real work to do.
	for key, entry := range s.tickets {
		if now.After(entry.expiresAt) {
			delete(s.tickets, key)
		}
	}
	s.tickets[id] = memoryDesktopTicketEntry{claims: claims, expiresAt: now.Add(s.ttl)}
	return id, nil
}

func (s *memoryDesktopTicketStore) Consume(
	_ context.Context, ticket string,
) (*SandboxDesktopTicketClaims, error) {
	ticket = strings.TrimSpace(ticket)
	if ticket == "" {
		return nil, ErrDesktopTicketInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.tickets[ticket]
	if !ok {
		return nil, ErrDesktopTicketInvalid
	}
	delete(s.tickets, ticket)
	if time.Now().After(entry.expiresAt) {
		return nil, ErrDesktopTicketInvalid
	}
	claims := entry.claims
	return &claims, nil
}
