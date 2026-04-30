package handler

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	oidcCallbackTicketTTL         = 5 * time.Minute
	oidcCallbackTicketBytes       = 32
	oidcCallbackTicketRedisPrefix = "auth:oidc:callback:"
)

var errOIDCCallbackTicketNotFound = errors.New("OIDC callback ticket is invalid or expired")

type oidcCallbackTicketStore interface {
	Save(ctx context.Context, resp *types.OIDCCallbackResponse) (string, error)
	Consume(ctx context.Context, ticket string) (*types.OIDCCallbackResponse, error)
}

func newOIDCCallbackTicketStore(redisClient *redis.Client) oidcCallbackTicketStore {
	return &redisOIDCCallbackTicketStore{
		redisClient: redisClient,
		fallback: &memoryOIDCCallbackTicketStore{
			entries: make(map[string]memoryOIDCCallbackTicketEntry),
			ttl:     oidcCallbackTicketTTL,
			now:     time.Now,
		},
	}
}

type redisOIDCCallbackTicketStore struct {
	redisClient *redis.Client
	fallback    *memoryOIDCCallbackTicketStore
}

func (s *redisOIDCCallbackTicketStore) Save(ctx context.Context, resp *types.OIDCCallbackResponse) (string, error) {
	payload, err := json.Marshal(resp)
	if err != nil {
		return "", fmt.Errorf("failed to encode OIDC callback response: %w", err)
	}

	ticket, err := generateOIDCCallbackTicket()
	if err != nil {
		return "", fmt.Errorf("failed to generate OIDC callback ticket: %w", err)
	}

	if s.redisClient != nil {
		if err := s.redisClient.Set(ctx, s.redisKey(ticket), payload, oidcCallbackTicketTTL).Err(); err == nil {
			return ticket, nil
		} else {
			logger.Warnf(ctx, "OIDC callback ticket Redis store unavailable, falling back to in-memory store: %v", err)
		}
	}

	if err := s.fallback.SavePayload(ticket, payload); err != nil {
		return "", err
	}

	return ticket, nil
}

func (s *redisOIDCCallbackTicketStore) Consume(ctx context.Context, ticket string) (*types.OIDCCallbackResponse, error) {
	ticket = strings.TrimSpace(ticket)
	if ticket == "" {
		return nil, errOIDCCallbackTicketNotFound
	}

	if s.redisClient != nil {
		payload, err := s.redisClient.GetDel(ctx, s.redisKey(ticket)).Bytes()
		switch {
		case err == nil:
			return decodeOIDCCallbackTicketPayload(payload)
		case errors.Is(err, redis.Nil):
		default:
			logger.Warnf(ctx, "OIDC callback ticket Redis consume failed, trying in-memory fallback: %v", err)
			if payload, ok, fallbackErr := s.fallback.ConsumePayload(ticket); fallbackErr == nil && ok {
				return decodeOIDCCallbackTicketPayload(payload)
			}
			return nil, fmt.Errorf("failed to consume OIDC callback ticket: %w", err)
		}
	}

	payload, ok, err := s.fallback.ConsumePayload(ticket)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errOIDCCallbackTicketNotFound
	}

	return decodeOIDCCallbackTicketPayload(payload)
}

func (s *redisOIDCCallbackTicketStore) redisKey(ticket string) string {
	return oidcCallbackTicketRedisPrefix + ticket
}

type memoryOIDCCallbackTicketStore struct {
	mu      sync.Mutex
	entries map[string]memoryOIDCCallbackTicketEntry
	ttl     time.Duration
	now     func() time.Time
}

type memoryOIDCCallbackTicketEntry struct {
	payload   []byte
	expiresAt time.Time
}

func (s *memoryOIDCCallbackTicketStore) SavePayload(ticket string, payload []byte) error {
	if s == nil {
		return errors.New("OIDC callback ticket store is not initialized")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	s.cleanupExpiredLocked(now)
	s.entries[ticket] = memoryOIDCCallbackTicketEntry{
		payload:   append([]byte(nil), payload...),
		expiresAt: now.Add(s.ttl),
	}
	return nil
}

func (s *memoryOIDCCallbackTicketStore) ConsumePayload(ticket string) ([]byte, bool, error) {
	if s == nil {
		return nil, false, errors.New("OIDC callback ticket store is not initialized")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	s.cleanupExpiredLocked(now)

	entry, ok := s.entries[ticket]
	if !ok {
		return nil, false, nil
	}
	delete(s.entries, ticket)
	if !entry.expiresAt.After(now) {
		return nil, false, nil
	}

	return append([]byte(nil), entry.payload...), true, nil
}

func (s *memoryOIDCCallbackTicketStore) cleanupExpiredLocked(now time.Time) {
	for ticket, entry := range s.entries {
		if !entry.expiresAt.After(now) {
			delete(s.entries, ticket)
		}
	}
}

func decodeOIDCCallbackTicketPayload(payload []byte) (*types.OIDCCallbackResponse, error) {
	var resp types.OIDCCallbackResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode OIDC callback response: %w", err)
	}
	return &resp, nil
}

func generateOIDCCallbackTicket() (string, error) {
	buffer := make([]byte, oidcCallbackTicketBytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}
