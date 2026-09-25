package hostapi

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// ScopeKV grants a plugin its own key-value store in the calling tenant.
const ScopeKV = "kv"

// KV is the plugins' key-value store. Every operation is confined to the
// plugin and tenant of the token, so a plugin never sees another plugin's
// or another workspace's data.
type KV struct {
	repo interfaces.PluginKVRepository
	now  func() time.Time
}

// NewKV creates the store.
func NewKV(repo interfaces.PluginKVRepository) *KV { return &KV{repo: repo, now: time.Now} }

func checkKey(key string) error {
	switch {
	case key == "":
		return pluginapi.Errorf(pluginapi.CodeBadRequest, "key is required")
	case len(key) > pluginapi.KVMaxKeyBytes:
		return pluginapi.Errorf(pluginapi.CodeBadRequest, "key is over %d bytes", pluginapi.KVMaxKeyBytes)
	case !utf8.ValidString(key) || strings.ContainsFunc(key, unicode.IsControl):
		return pluginapi.Errorf(pluginapi.CodeBadRequest, "key must be printable UTF-8")
	}
	return nil
}

func toEntry(e *types.PluginKV) pluginapi.KVEntry {
	return pluginapi.KVEntry{
		Key:       e.Key,
		Value:     json.RawMessage(e.Value),
		ExpiresAt: e.ExpiresAt,
		UpdatedAt: e.UpdatedAt,
	}
}

// Get reads a key.
func (s *KV) Get(ctx context.Context, c *Claims, key string) (*pluginapi.KVEntry, error) {
	if err := checkKey(key); err != nil {
		return nil, err
	}
	e, err := s.repo.Get(ctx, c.PluginID, c.TenantID, key)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, pluginapi.Errorf(pluginapi.CodeNotFound, "no key %q", key)
	}
	out := toEntry(e)
	return &out, nil
}

// Put writes a key, refusing new keys past the per-tenant quota.
func (s *KV) Put(ctx context.Context, c *Claims, in pluginapi.KVPut) (*pluginapi.KVEntry, error) {
	if err := checkKey(in.Key); err != nil {
		return nil, err
	}
	if len(in.Value) == 0 || !json.Valid(in.Value) {
		return nil, pluginapi.Errorf(pluginapi.CodeBadRequest, "value must be JSON")
	}
	if len(in.Value) > pluginapi.KVMaxValueBytes {
		return nil, pluginapi.Errorf(pluginapi.CodeBadRequest, "value is over %d bytes", pluginapi.KVMaxValueBytes)
	}
	if in.TTLSeconds < 0 {
		return nil, pluginapi.Errorf(pluginapi.CodeBadRequest, "ttlSeconds must not be negative")
	}
	existing, err := s.repo.Get(ctx, c.PluginID, c.TenantID, in.Key)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		n, err := s.repo.Count(ctx, c.PluginID, c.TenantID)
		if err != nil {
			return nil, err
		}
		if n >= pluginapi.KVMaxKeys {
			return nil, pluginapi.Errorf(
				pluginapi.CodeRateLimited,
				"the store holds its maximum of %d keys",
				pluginapi.KVMaxKeys,
			)
		}
	}
	now := s.now()
	e := &types.PluginKV{
		PluginID: c.PluginID, TenantID: c.TenantID, Key: in.Key, Value: types.JSON(in.Value), UpdatedAt: now,
	}
	if in.TTLSeconds > 0 {
		exp := now.Add(time.Duration(in.TTLSeconds) * time.Second)
		e.ExpiresAt = &exp
	}
	if err := s.repo.Put(ctx, e); err != nil {
		return nil, err
	}
	out := toEntry(e)
	return &out, nil
}

// Delete removes a key; deleting a missing key is not an error.
func (s *KV) Delete(ctx context.Context, c *Claims, key string) error {
	if err := checkKey(key); err != nil {
		return err
	}
	_, err := s.repo.Delete(ctx, c.PluginID, c.TenantID, key)
	return err
}

// List pages through keys with a prefix.
func (s *KV) List(ctx context.Context, c *Claims, prefix, after string, limit int) (*pluginapi.KVList, error) {
	if limit <= 0 {
		limit = 100
	}
	limit = min(limit, pluginapi.KVMaxListLimit)
	rows, err := s.repo.List(ctx, c.PluginID, c.TenantID, prefix, after, limit+1)
	if err != nil {
		return nil, err
	}
	out := &pluginapi.KVList{Entries: []pluginapi.KVEntry{}}
	for i := range rows {
		if i == limit {
			out.Next = rows[i-1].Key
			break
		}
		out.Entries = append(out.Entries, toEntry(&rows[i]))
	}
	return out, nil
}

// StartSweeper deletes expired entries every interval until ctx ends.
func (s *KV) StartSweeper(ctx context.Context, interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if n, err := s.repo.DeleteExpired(ctx, s.now()); err != nil {
					logger.Warnf(ctx, "[plugin] sweep expired kv: %v", err)
				} else if n > 0 {
					logger.Infof(ctx, "[plugin] swept %d expired kv entries", n)
				}
			}
		}
	}()
}
