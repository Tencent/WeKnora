package hostpool

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// LeaseTTL is how long a node holds a singleton plugin's lease without
// renewing it: after a node dies, another takes the plugin over within it.
var LeaseTTL = 30 * time.Second

const leaseKeyBase = "weknora:plugin-lease:"

func leaseKey(pluginID string) string {
	if ns := strings.TrimSpace(os.Getenv("WEKNORA_REDIS_NAMESPACE")); ns != "" {
		return leaseKeyBase + ns + ":" + pluginID
	}
	return leaseKeyBase + pluginID
}

// acquireScript takes a free lease or renews this node's; it answers 1
// when the node holds the lease afterwards.
var acquireScript = redis.NewScript(`
local holder = redis.call('GET', KEYS[1])
if holder == ARGV[1] then
  redis.call('PEXPIRE', KEYS[1], ARGV[2])
  return 1
end
if holder then
  return 0
end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
return 1
`)

// releaseScript drops the lease if this node holds it.
var releaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)

// Leases elect the one node that runs each singleton plugin, in Redis. It
// implements host.Leases.
type Leases struct {
	rdb  *redis.Client
	node string
}

// NewLeases elects nodes by the given name, unique to this process.
func NewLeases(rdb *redis.Client, node string) *Leases { return &Leases{rdb: rdb, node: node} }

// Acquire takes a plugin's lease for this node, or renews it.
func (l *Leases) Acquire(ctx context.Context, pluginID string) (bool, error) {
	n, err := acquireScript.Run(ctx, l.rdb, []string{leaseKey(pluginID)}, l.node, LeaseTTL.Milliseconds()).Int()
	return n == 1, err
}

// Release gives the lease up, if this node holds it.
func (l *Leases) Release(ctx context.Context, pluginID string) {
	_ = releaseScript.Run(ctx, l.rdb, []string{leaseKey(pluginID)}, l.node).Err()
}

// TTL implements host.Leases.
func (l *Leases) TTL() time.Duration { return LeaseTTL }

// Holder is the node holding a plugin's lease, "" when none does.
func (l *Leases) Holder(ctx context.Context, pluginID string) string {
	holder, _ := l.rdb.Get(ctx, leaseKey(pluginID)).Result()
	return holder
}
