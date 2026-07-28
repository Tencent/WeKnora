package artifact

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/redis/go-redis/v9"
)

const (
	artifactLeaseTTL       = 5 * time.Minute
	artifactLeaseKeyPrefix = "weknora:artifact:lease:"
)

// Lease suppresses duplicate provider work across processes. It is only an
// optimization: immutable database put-if-absent remains the correctness
// boundary, and lease failures make Runtime compute normally.
type Lease interface {
	TryAcquire(
		ctx context.Context,
		key types.ProcessingArtifactLookup,
	) (handle LeaseHandle, acquired bool, err error)
}

type LeaseHandle interface {
	Release()
}

type redisLease struct {
	client redis.UniversalClient
	ttl    time.Duration
}

func NewRedisLease(client *redis.Client) Lease {
	if client == nil {
		return nil
	}
	return &redisLease{client: client, ttl: artifactLeaseTTL}
}

func (l *redisLease) TryAcquire(
	ctx context.Context,
	key types.ProcessingArtifactLookup,
) (LeaseHandle, bool, error) {
	if l == nil || l.client == nil {
		return nil, false, errors.New("artifact Redis lease is not configured")
	}
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, false, err
	}
	token := hex.EncodeToString(tokenBytes)
	redisKey := artifactLeaseKeyPrefix + SHA256Hex([]byte(singleflightKey(key)))
	acquired, err := l.client.SetNX(ctx, redisKey, token, l.ttl).Result()
	if err != nil || !acquired {
		return nil, acquired, err
	}
	handle := &redisLeaseHandle{
		client: l.client,
		key:    redisKey,
		token:  token,
		ttl:    l.ttl,
		stop:   make(chan struct{}),
	}
	go handle.renew()
	return handle, true, nil
}

type redisLeaseHandle struct {
	client redis.UniversalClient
	key    string
	token  string
	ttl    time.Duration
	stop   chan struct{}
}

var releaseLeaseScript = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
  return redis.call("del", KEYS[1])
end
return 0
`)

var renewLeaseScript = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
  return redis.call("pexpire", KEYS[1], ARGV[2])
end
return 0
`)

func (h *redisLeaseHandle) Release() {
	if h == nil {
		return
	}
	select {
	case <-h.stop:
		return
	default:
		close(h.stop)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = releaseLeaseScript.Run(ctx, h.client, []string{h.key}, h.token).Err()
}

func (h *redisLeaseHandle) renew() {
	ticker := time.NewTicker(h.ttl / 3)
	defer ticker.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			result, err := renewLeaseScript.Run(
				ctx,
				h.client,
				[]string{h.key},
				h.token,
				h.ttl.Milliseconds(),
			).Int()
			cancel()
			if err != nil || result == 0 {
				return
			}
		}
	}
}
