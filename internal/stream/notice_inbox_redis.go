package stream

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/redis/go-redis/v9"
)

// noticeKeyTTL is a backstop so an abandoned session's inbox disappears.
const noticeKeyTTL = interfaces.NoticeRetention + time.Hour

// pushNoticeScript writes a notice and refreshes the key TTL in one round
// trip. As two commands, a failed EXPIRE after a successful HSETNX would return
// an error for a notice that is already stored; the caller's retry would then
// see first=false and work_finished would never be emitted.
//
// KEYS[1] inbox key; ARGV[1] id, ARGV[2] encoded notice, ARGV[3] TTL in ms.
var pushNoticeScript = redis.NewScript(`
local first = redis.call('HSETNX', KEYS[1], ARGV[1], ARGV[2])
redis.call('PEXPIRE', KEYS[1], ARGV[3])
return first
`)

// dropNoticesScript deletes each field only if it still holds the value the
// caller read. Deleting by ID alone could remove a notice that was acked and
// pushed again between the caller's read and this delete.
//
// KEYS[1] inbox key; ARGV pairs of id, encoded notice as read.
var dropNoticesScript = redis.NewScript(`
local n = 0
for i = 1, #ARGV, 2 do
  if redis.call('HGET', KEYS[1], ARGV[i]) == ARGV[i + 1] then
    n = n + redis.call('HDEL', KEYS[1], ARGV[i])
  end
end
return n
`)

type redisNoticeValue struct {
	Kind         string `json:"kind"`
	Text         string `json:"text"`
	FinishedAtMS int64  `json:"finished_at_ms"`
}

// RedisNoticeInbox is the multi-replica NoticeInbox. It lives next to the
// steer lists and uses the same key prefix.
type RedisNoticeInbox struct {
	mgr   *RedisStreamManager
	clock func() time.Time
}

var _ interfaces.NoticeInbox = (*RedisNoticeInbox)(nil)

// NewRedisNoticeInbox returns an inbox on m's Redis client. A nil clock means time.Now.
func NewRedisNoticeInbox(m *RedisStreamManager, clock func() time.Time) *RedisNoticeInbox {
	if clock == nil {
		clock = time.Now
	}
	return &RedisNoticeInbox{mgr: m, clock: clock}
}

func (b *RedisNoticeInbox) key(s interfaces.SessionRef) string {
	return fmt.Sprintf("%s:%s:bg-inbox", b.mgr.prefix, s.SessionID)
}

// Push implements interfaces.NoticeInbox. An error means the notice was not
// stored. The one exception is a reply lost after the script ran: the retry
// then sees first=false, and the UI's background-work polling catches up.
func (b *RedisNoticeInbox) Push(
	ctx context.Context, s interfaces.SessionRef, n interfaces.BackgroundNotice,
) (bool, error) {
	raw, err := json.Marshal(redisNoticeValue{
		Kind: n.Kind, Text: n.Text, FinishedAtMS: n.FinishedAt.UnixMilli(),
	})
	if err != nil {
		return false, fmt.Errorf("encode background notice: %w", err)
	}
	first, err := pushNoticeScript.Run(ctx, b.mgr.client, []string{b.key(s)},
		n.ID, raw, noticeKeyTTL.Milliseconds()).Int()
	if err != nil {
		return false, fmt.Errorf("push background notice: %w", err)
	}
	return first == 1, nil
}

// Pending implements interfaces.NoticeInbox.
func (b *RedisNoticeInbox) Pending(
	ctx context.Context, s interfaces.SessionRef,
) ([]interfaces.BackgroundNotice, error) {
	key := b.key(s)
	all, err := b.mgr.client.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("read background notices: %w", err)
	}
	cutoff := b.clock().Add(-interfaces.NoticeRetention)
	out := make([]interfaces.BackgroundNotice, 0, len(all))
	expired := make(map[string]string)
	for id, raw := range all {
		var v redisNoticeValue
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, fmt.Errorf("decode background notice %s: %w", id, err)
		}
		finished := time.UnixMilli(v.FinishedAtMS).UTC()
		if finished.Before(cutoff) {
			expired[id] = raw
			continue
		}
		out = append(out, interfaces.BackgroundNotice{ID: id, Kind: v.Kind, Text: v.Text, FinishedAt: finished})
	}
	if err := b.dropIfUnchanged(ctx, key, expired); err != nil {
		return nil, fmt.Errorf("drop expired background notices: %w", err)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].FinishedAt.Equal(out[j].FinishedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].FinishedAt.Before(out[j].FinishedAt)
	})
	return out, nil
}

// dropIfUnchanged deletes each id whose stored value still equals read[id].
func (b *RedisNoticeInbox) dropIfUnchanged(ctx context.Context, key string, read map[string]string) error {
	if len(read) == 0 {
		return nil
	}
	args := make([]any, 0, 2*len(read))
	for id, raw := range read {
		args = append(args, id, raw)
	}
	return dropNoticesScript.Run(ctx, b.mgr.client, []string{key}, args...).Err()
}

// Ack implements interfaces.NoticeInbox.
func (b *RedisNoticeInbox) Ack(ctx context.Context, s interfaces.SessionRef, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if err := b.mgr.client.HDel(ctx, b.key(s), ids...).Err(); err != nil {
		return fmt.Errorf("ack background notices: %w", err)
	}
	return nil
}

// Clear implements interfaces.NoticeInbox.
func (b *RedisNoticeInbox) Clear(ctx context.Context, s interfaces.SessionRef) error {
	if err := b.mgr.client.Del(ctx, b.key(s)).Err(); err != nil {
		return fmt.Errorf("clear background notices: %w", err)
	}
	return nil
}
