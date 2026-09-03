package workspaceevent

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/redis/go-redis/v9"
)

// subscriptionIndex gates Emit: only tenants with an enabled endpoint that
// subscribes to the event type write outbox rows. Redis holds the type union;
// when Redis is nil or errors, ListEnabledByTenant is the fallback.
type subscriptionIndex struct {
	rdb       *redis.Client
	endpoints interfaces.WebhookEndpointRepository
}

// NewSubscriptionIndex builds the Emit subscription gate. rdb may be nil (Lite).
func NewSubscriptionIndex(
	rdb *redis.Client,
	endpoints interfaces.WebhookEndpointRepository,
) interfaces.WebhookSubscriptionIndex {
	return &subscriptionIndex{rdb: rdb, endpoints: endpoints}
}

func subscriptionKey(tenantID uint64) string {
	return fmt.Sprintf("%s%d", types.WebhookSubRedisKeyPrefix, tenantID)
}

func (i *subscriptionIndex) Subscribes(ctx context.Context, tenantID uint64, eventType string) bool {
	if i == nil || tenantID == 0 || eventType == "" {
		return false
	}
	if eventType == types.EventWebhookTest {
		// Test is never a subscription member; DispatchTest bypasses the gate.
		return false
	}
	if i.rdb != nil {
		key := subscriptionKey(tenantID)
		pipe := i.rdb.Pipeline()
		memberCmd := pipe.SIsMember(ctx, key, eventType)
		existsCmd := pipe.Exists(ctx, key)
		if _, err := pipe.Exec(ctx); err == nil {
			if member, _ := memberCmd.Result(); member {
				return true
			}
			if exists, _ := existsCmd.Result(); exists > 0 {
				return false
			}
			// miss → rebuild below
		} else {
			logger.Warnf(ctx, "webhook sub cache lookup tenant=%d: %v", tenantID, err)
		}
	}
	typesSet, err := i.loadFromDB(ctx, tenantID)
	if err != nil {
		// Fail open toward writing outbox: dispatcher still filters; better a
		// useless row than a silent drop when DB is briefly unavailable.
		logger.Warnf(ctx, "webhook sub load tenant=%d: %v (allow emit)", tenantID, err)
		return true
	}
	i.cachePut(ctx, tenantID, typesSet)
	_, ok := typesSet[eventType]
	return ok
}

func (i *subscriptionIndex) Invalidate(ctx context.Context, tenantID uint64) error {
	if i == nil || tenantID == 0 || i.rdb == nil {
		return nil
	}
	if err := i.rdb.Del(ctx, subscriptionKey(tenantID)).Err(); err != nil {
		logger.Warnf(ctx, "webhook sub invalidate tenant=%d: %v", tenantID, err)
		return err
	}
	return nil
}

// Warm rebuilds the Redis set after Invalidate so the next Emit does not miss.
func (i *subscriptionIndex) Warm(ctx context.Context, tenantID uint64) {
	if i == nil || tenantID == 0 {
		return
	}
	typesSet, err := i.loadFromDB(ctx, tenantID)
	if err != nil {
		logger.Warnf(ctx, "webhook sub warm tenant=%d: %v", tenantID, err)
		return
	}
	i.cachePut(ctx, tenantID, typesSet)
}

func (i *subscriptionIndex) loadFromDB(ctx context.Context, tenantID uint64) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	if i.endpoints == nil {
		return out, nil
	}
	rows, err := i.endpoints.ListEnabledByTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	for _, ep := range rows {
		if ep == nil {
			continue
		}
		for _, t := range ep.Events {
			if t == "" || t == types.EventWebhookTest {
				continue
			}
			out[t] = struct{}{}
		}
	}
	return out, nil
}

func (i *subscriptionIndex) cachePut(ctx context.Context, tenantID uint64, typesSet map[string]struct{}) {
	if i.rdb == nil {
		return
	}
	key := subscriptionKey(tenantID)
	ttl := types.WebhookSubNegativeTTL
	members := make([]interface{}, 0, len(typesSet)+1)
	if len(typesSet) == 0 {
		members = append(members, types.WebhookSubEmptyMarker)
	} else {
		ttl = types.WebhookSubPositiveTTL
		for t := range typesSet {
			members = append(members, t)
		}
	}
	pipe := i.rdb.TxPipeline()
	pipe.Del(ctx, key)
	pipe.SAdd(ctx, key, members...)
	pipe.Expire(ctx, key, ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		logger.Warnf(ctx, "webhook sub cache put tenant=%d: %v", tenantID, err)
	}
}

// AlwaysSubscribe is a test/helper index that never gates Emit.
type AlwaysSubscribe struct{}

func (AlwaysSubscribe) Subscribes(context.Context, uint64, string) bool { return true }
func (AlwaysSubscribe) Invalidate(context.Context, uint64) error         { return nil }
func (AlwaysSubscribe) Warm(context.Context, uint64)                     {}
