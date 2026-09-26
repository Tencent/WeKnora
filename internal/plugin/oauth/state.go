package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// stateTTL bounds an authorization from start to callback.
const stateTTL = 10 * time.Minute

// pending is an authorization in flight. It holds the PKCE verifier, a
// secret, so it lives server side and the state parameter is a random key.
type pending struct {
	Target   Target `json:"target"`
	UserID   string `json:"userId"`
	Origin   string `json:"origin"`
	Redirect string `json:"redirect"`
	Verifier string `json:"verifier,omitempty"`
}

// Outcome is how an authorization ended, kept for the form that started it
// to collect by polling when the callback page cannot reach it (a desktop
// app hands the consent screen to the system browser; some providers cut
// the popup off from its opener).
type Outcome struct {
	UserID       string `json:"userId"`
	OK           bool   `json:"ok"`
	ConnectionID string `json:"connectionId,omitempty"`
	Error        string `json:"error,omitempty"`
}

// stateStore keeps pending authorizations and their outcomes in Redis, so
// the callback may land on any node, or in memory on a single node.
type stateStore struct {
	rdb *redis.Client

	mu  sync.Mutex
	mem map[string]memEntry
}

type memEntry struct {
	value   []byte
	expires time.Time
}

func newStateStore(rdb *redis.Client) *stateStore {
	return &stateStore{rdb: rdb, mem: map[string]memEntry{}}
}

func storeKey(kind, state string) string {
	if ns := strings.TrimSpace(os.Getenv("WEKNORA_REDIS_NAMESPACE")); ns != "" {
		return "weknora:plugin_oauth_" + kind + ":" + ns + ":" + state
	}
	return "weknora:plugin_oauth_" + kind + ":" + state
}

func (s *stateStore) set(ctx context.Context, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if s.rdb != nil {
		return s.rdb.Set(ctx, key, raw, stateTTL).Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, e := range s.mem {
		if now.After(e.expires) {
			delete(s.mem, k)
		}
	}
	s.mem[key] = memEntry{value: raw, expires: now.Add(stateTTL)}
	return nil
}

// take returns an entry once and removes it.
func (s *stateStore) take(ctx context.Context, key string, out any) (bool, error) {
	var raw []byte
	if s.rdb != nil {
		b, err := s.rdb.GetDel(ctx, key).Bytes()
		if errors.Is(err, redis.Nil) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		raw = b
	} else {
		s.mu.Lock()
		e, ok := s.mem[key]
		delete(s.mem, key)
		s.mu.Unlock()
		if !ok || time.Now().After(e.expires) {
			return false, nil
		}
		raw = e.value
	}
	return true, json.Unmarshal(raw, out)
}

func (s *stateStore) putPending(ctx context.Context, state string, p pending) error {
	return s.set(ctx, storeKey("state", state), p)
}

// takePending returns a pending authorization once: a state cannot be
// replayed.
func (s *stateStore) takePending(ctx context.Context, state string) (pending, bool, error) {
	var p pending
	ok, err := s.take(ctx, storeKey("state", state), &p)
	return p, ok, err
}

func (s *stateStore) putOutcome(ctx context.Context, state string, o Outcome) error {
	return s.set(ctx, storeKey("result", state), o)
}

func (s *stateStore) takeOutcome(ctx context.Context, state string) (Outcome, bool, error) {
	var o Outcome
	ok, err := s.take(ctx, storeKey("result", state), &o)
	return o, ok, err
}
