package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// oauthStateTTL bounds how long an in-flight authorization may take from
// "authorize-url issued" to "callback received".
const oauthStateTTL = 10 * time.Minute

// OAuthState is the transient data needed to complete an authorization-code
// exchange. It is keyed by the opaque OAuth `state` parameter and MUST hold
// the PKCE code_verifier, which is a secret that must never reach the
// authorization server — hence server-side storage rather than encoding it
// into the state parameter.
type OAuthState struct {
	TenantID     uint64 `json:"tenant_id"`
	UserID       string `json:"user_id"`
	ServiceID    string `json:"service_id"`
	CodeVerifier string `json:"code_verifier"`
	ClientID     string `json:"client_id"`
	RedirectURI  string `json:"redirect_uri"`
	// FrontendRedirect is where the backend callback redirects the browser
	// after completing (or failing) the exchange.
	FrontendRedirect string `json:"frontend_redirect"`
}

// oauthStateStore persists in-flight OAuth states. Backed by Redis when
// available (so the callback can land on any backend replica); falls back to
// a TTL in-memory map for single-instance / Lite deployments.
type oauthStateStore struct {
	rdb *redis.Client

	mu  sync.Mutex
	mem map[string]memStateEntry
}

type memStateEntry struct {
	value     OAuthState
	expiresAt time.Time
}

func newOAuthStateStore(rdb *redis.Client) *oauthStateStore {
	s := &oauthStateStore{rdb: rdb, mem: make(map[string]memStateEntry)}
	if rdb == nil {
		go s.gcLoop()
	}
	return s
}

func (s *oauthStateStore) key(state string) string {
	ns := strings.TrimSpace(os.Getenv("WEKNORA_REDIS_NAMESPACE"))
	if ns != "" {
		return "weknora:mcp_oauth_state:" + ns + ":" + state
	}
	return "weknora:mcp_oauth_state:" + state
}

// Put stores a state with a fixed TTL.
func (s *oauthStateStore) Put(ctx context.Context, state string, value OAuthState) error {
	if s.rdb != nil {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		return s.rdb.Set(ctx, s.key(state), data, oauthStateTTL).Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mem[state] = memStateEntry{value: value, expiresAt: time.Now().Add(oauthStateTTL)}
	return nil
}

// Take retrieves and deletes a state (single-use). Returns an error if the
// state is unknown or expired.
func (s *oauthStateStore) Take(ctx context.Context, state string) (OAuthState, error) {
	if s.rdb != nil {
		data, err := s.rdb.GetDel(ctx, s.key(state)).Bytes()
		if err != nil {
			if err == redis.Nil {
				return OAuthState{}, fmt.Errorf("oauth state not found or expired")
			}
			return OAuthState{}, err
		}
		var v OAuthState
		if err := json.Unmarshal(data, &v); err != nil {
			return OAuthState{}, err
		}
		return v, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.mem[state]
	if !ok {
		return OAuthState{}, fmt.Errorf("oauth state not found or expired")
	}
	delete(s.mem, state)
	if time.Now().After(entry.expiresAt) {
		return OAuthState{}, fmt.Errorf("oauth state not found or expired")
	}
	return entry.value, nil
}

func (s *oauthStateStore) gcLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		s.mu.Lock()
		for k, v := range s.mem {
			if now.After(v.expiresAt) {
				delete(s.mem, k)
			}
		}
		s.mu.Unlock()
	}
}
