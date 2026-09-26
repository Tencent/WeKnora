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

// stateStore keeps pending authorizations in Redis, so the callback may land
// on any node, or in memory on a single node.
type stateStore struct {
	rdb *redis.Client

	mu  sync.Mutex
	mem map[string]memState
}

type memState struct {
	value   pending
	expires time.Time
}

func newStateStore(rdb *redis.Client) *stateStore {
	return &stateStore{rdb: rdb, mem: map[string]memState{}}
}

func stateKey(state string) string {
	if ns := strings.TrimSpace(os.Getenv("WEKNORA_REDIS_NAMESPACE")); ns != "" {
		return "weknora:plugin_oauth_state:" + ns + ":" + state
	}
	return "weknora:plugin_oauth_state:" + state
}

func (s *stateStore) put(ctx context.Context, state string, p pending) error {
	if s.rdb != nil {
		raw, err := json.Marshal(p)
		if err != nil {
			return err
		}
		return s.rdb.Set(ctx, stateKey(state), raw, stateTTL).Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, v := range s.mem {
		if now.After(v.expires) {
			delete(s.mem, k)
		}
	}
	s.mem[state] = memState{value: p, expires: now.Add(stateTTL)}
	return nil
}

// take returns a pending authorization once: a state cannot be replayed.
func (s *stateStore) take(ctx context.Context, state string) (pending, bool, error) {
	if s.rdb != nil {
		raw, err := s.rdb.GetDel(ctx, stateKey(state)).Bytes()
		if errors.Is(err, redis.Nil) {
			return pending{}, false, nil
		}
		if err != nil {
			return pending{}, false, err
		}
		var p pending
		return p, true, json.Unmarshal(raw, &p)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.mem[state]
	delete(s.mem, state)
	if !ok || time.Now().After(v.expires) {
		return pending{}, false, nil
	}
	return v.value, true, nil
}
