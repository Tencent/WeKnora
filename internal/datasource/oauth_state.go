package datasource

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

const dataSourceOAuthStateTTL = 10 * time.Minute

type dataSourceOAuthState struct {
	TenantID          uint64 `json:"tenant_id"`
	DataSourceID      string `json:"data_source_id"`
	AuthorizedBy      string `json:"authorized_by"`
	ConnectionVersion uint64 `json:"connection_version"`
	CodeVerifier      string `json:"code_verifier"`
	ReplaceConnection bool   `json:"replace_connection"`
}

type dataSourceOAuthStateEntry struct {
	value     dataSourceOAuthState
	expiresAt time.Time
}

type dataSourceOAuthStateStore struct {
	rdb *redis.Client
	mu  sync.Mutex
	mem map[string]dataSourceOAuthStateEntry
}

func newDataSourceOAuthStateStore(rdb *redis.Client) *dataSourceOAuthStateStore {
	return &dataSourceOAuthStateStore{rdb: rdb, mem: make(map[string]dataSourceOAuthStateEntry)}
}

func (s *dataSourceOAuthStateStore) key(state string) string {
	ns := strings.TrimSpace(os.Getenv("WEKNORA_REDIS_NAMESPACE"))
	if ns != "" {
		return "weknora:datasource_oauth_state:" + ns + ":" + state
	}
	return "weknora:datasource_oauth_state:" + state
}

func (s *dataSourceOAuthStateStore) put(ctx context.Context, state string, value dataSourceOAuthState) error {
	if s.rdb != nil {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		return s.rdb.Set(ctx, s.key(state), data, dataSourceOAuthStateTTL).Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mem[state] = dataSourceOAuthStateEntry{value: value, expiresAt: time.Now().Add(dataSourceOAuthStateTTL)}
	return nil
}

func (s *dataSourceOAuthStateStore) take(ctx context.Context, state string) (dataSourceOAuthState, error) {
	if s.rdb != nil {
		data, err := s.rdb.GetDel(ctx, s.key(state)).Bytes()
		if err != nil {
			if err == redis.Nil {
				return dataSourceOAuthState{}, fmt.Errorf("oauth state not found or expired")
			}
			return dataSourceOAuthState{}, err
		}
		var value dataSourceOAuthState
		if err := json.Unmarshal(data, &value); err != nil {
			return dataSourceOAuthState{}, err
		}
		return value, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.mem[state]
	delete(s.mem, state)
	if !ok || time.Now().After(entry.expiresAt) {
		return dataSourceOAuthState{}, fmt.Errorf("oauth state not found or expired")
	}
	return entry.value, nil
}
