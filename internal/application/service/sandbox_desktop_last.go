package service

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/redis/go-redis/v9"
)

// DefaultSandboxDesktopLastTTL is long enough to outlive a paused sandbox and
// a skill-install rebuild so a reconnect on another replica still sees the
// previous sandbox ID.
const DefaultSandboxDesktopLastTTL = 7 * 24 * time.Hour

const desktopLastRedisPrefix = "weknora:desktop-last-sandbox:"

// SandboxDesktopLastStore remembers which sandbox a session's desktop last
// attached to. SANDBOX_REBUILT is load-bearing: a skill install can recreate
// the sandbox under a live session, and a process-local map misses that when
// the reconnect lands on another replica.
type SandboxDesktopLastStore interface {
	Get(ctx context.Context, sessionID string) (string, error)
	Set(ctx context.Context, sessionID, sandboxID string) error
}

// NewSandboxDesktopLastStore returns a Redis-backed store, or an in-process
// one when Redis is not configured (Lite / single-instance).
func NewSandboxDesktopLastStore(rdb *redis.Client) SandboxDesktopLastStore {
	if rdb != nil {
		return &redisDesktopLastStore{client: rdb, ttl: DefaultSandboxDesktopLastTTL}
	}
	logger.Warnf(context.Background(),
		"[sandbox] No Redis configured, using in-memory desktop last-sandbox store (single-instance)")
	return newMemoryDesktopLastStore(DefaultSandboxDesktopLastTTL)
}

type redisDesktopLastStore struct {
	client redis.UniversalClient
	ttl    time.Duration
}

func (s *redisDesktopLastStore) Get(ctx context.Context, sessionID string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", nil
	}
	val, err := s.client.Get(ctx, desktopLastRedisPrefix+sessionID).Result()
	if err == redis.Nil {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(val), nil
}

func (s *redisDesktopLastStore) Set(ctx context.Context, sessionID, sandboxID string) error {
	sessionID = strings.TrimSpace(sessionID)
	sandboxID = strings.TrimSpace(sandboxID)
	if sessionID == "" || sandboxID == "" {
		return nil
	}
	return s.client.Set(ctx, desktopLastRedisPrefix+sessionID, sandboxID, s.ttl).Err()
}

type memoryDesktopLastEntry struct {
	sandboxID string
	expiresAt time.Time
}

type memoryDesktopLastStore struct {
	mu      sync.Mutex
	entries map[string]memoryDesktopLastEntry
	ttl     time.Duration
}

func newMemoryDesktopLastStore(ttl time.Duration) *memoryDesktopLastStore {
	return &memoryDesktopLastStore{
		entries: make(map[string]memoryDesktopLastEntry),
		ttl:     ttl,
	}
}

func (s *memoryDesktopLastStore) Get(_ context.Context, sessionID string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", nil
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[sessionID]
	if !ok {
		return "", nil
	}
	if now.After(entry.expiresAt) {
		delete(s.entries, sessionID)
		return "", nil
	}
	return entry.sandboxID, nil
}

func (s *memoryDesktopLastStore) Set(_ context.Context, sessionID, sandboxID string) error {
	sessionID = strings.TrimSpace(sessionID)
	sandboxID = strings.TrimSpace(sandboxID)
	if sessionID == "" || sandboxID == "" {
		return nil
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, entry := range s.entries {
		if now.After(entry.expiresAt) {
			delete(s.entries, key)
		}
	}
	s.entries[sessionID] = memoryDesktopLastEntry{sandboxID: sandboxID, expiresAt: now.Add(s.ttl)}
	return nil
}
