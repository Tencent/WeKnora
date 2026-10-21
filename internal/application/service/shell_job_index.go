package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	shellJobIndexKey    = "weknora:background:shelljob:index"
	shellJobSweepKeyFmt = "weknora:background:shelljob:sweep:{%s}"
	shellJobSweepTTL    = 15 * time.Second
	shellJobHandoffSec  = 5 // index score = yield_until + 5s (D1)
)

type ShellJobIndex struct {
	client    redis.UniversalClient
	namespace string
}

type ShellJobIndexEntry struct {
	SessionRef interfaces.SessionRef
	WorkID     string
	SandboxID  string
	YieldUntil time.Time
}

func NewShellJobIndex(client redis.UniversalClient, namespace string) *ShellJobIndex {
	if strings.TrimSpace(namespace) == "" {
		namespace = "weknora"
	}
	return &ShellJobIndex{client: client, namespace: namespace}
}

func (x *ShellJobIndex) member(e ShellJobIndexEntry) string {
	return fmt.Sprintf("%s:%d:%s:%s:%s",
		x.namespace, e.SessionRef.TenantID, e.SessionRef.SessionID, e.SandboxID, e.WorkID)
}

func (x *ShellJobIndex) parseMember(member string) (ShellJobIndexEntry, bool) {
	parts := strings.SplitN(member, ":", 5)
	if len(parts) != 5 || parts[0] != x.namespace {
		return ShellJobIndexEntry{}, false
	}
	var tenant uint64
	if _, err := fmt.Sscanf(parts[1], "%d", &tenant); err != nil {
		return ShellJobIndexEntry{}, false
	}
	if !strings.Contains(parts[4], "sj_") {
		return ShellJobIndexEntry{}, false
	}
	return ShellJobIndexEntry{
		SessionRef: interfaces.SessionRef{TenantID: tenant, SessionID: parts[2]},
		SandboxID:  parts[3], WorkID: parts[4],
	}, true
}

func (x *ShellJobIndex) Add(ctx context.Context, e ShellJobIndexEntry) error {
	score := float64(e.YieldUntil.Unix()) + shellJobHandoffSec
	return x.client.ZAdd(ctx, shellJobIndexKey, redis.Z{
		Score: score, Member: x.member(e),
	}).Err()
}

func (x *ShellJobIndex) Requeue(ctx context.Context, e ShellJobIndexEntry) error {
	return x.Add(ctx, e)
}

func (x *ShellJobIndex) Due(ctx context.Context, now time.Time) ([]ShellJobIndexEntry, error) {
	scored, err := x.client.ZRangeByScoreWithScores(ctx, shellJobIndexKey, &redis.ZRangeBy{
		Min: "-inf", Max: fmt.Sprintf("%d", now.Unix()),
	}).Result()
	if err != nil {
		return nil, err
	}
	out := make([]ShellJobIndexEntry, 0, len(scored))
	for _, z := range scored {
		member, ok := z.Member.(string)
		if !ok {
			continue
		}
		e, ok := x.parseMember(member)
		if !ok {
			continue
		}
		// Score is yield_until plus the 5s handoff. Callers use YieldUntil to
		// tell a just-due job from one whose window ended long ago.
		e.YieldUntil = time.Unix(int64(z.Score)-shellJobHandoffSec, 0).UTC()
		out = append(out, e)
	}
	return out, nil
}

func (x *ShellJobIndex) Claim(ctx context.Context, now time.Time, replica string) (
	map[interfaces.SessionRef][]ShellJobIndexEntry, error,
) {
	due, err := x.Due(ctx, now)
	if err != nil {
		return nil, err
	}
	// 先按会话分组，再每会话一次 SET NX（shell 稿 §5.4 的顺序）。绝不能逐条
	// SetNX：同一会话的锁 key 相同，第二笔会被自己刚设的锁挡掉、从本轮丢出。
	grouped := make(map[interfaces.SessionRef][]ShellJobIndexEntry)
	for _, e := range due {
		grouped[e.SessionRef] = append(grouped[e.SessionRef], e)
	}
	out := make(map[interfaces.SessionRef][]ShellJobIndexEntry)
	for ref, entries := range grouped {
		key := fmt.Sprintf(shellJobSweepKeyFmt, x.hashTag(ref))
		ok, err := x.client.SetNX(ctx, key, replica, shellJobSweepTTL).Result()
		if err != nil {
			return nil, err
		}
		if ok {
			out[ref] = entries
		}
	}
	return out, nil
}

func (x *ShellJobIndex) Release(ctx context.Context, s interfaces.SessionRef, replica string) error {
	key := fmt.Sprintf(shellJobSweepKeyFmt, x.hashTag(s))
	current, err := x.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return nil
	}
	if err != nil {
		return err
	}
	if current != replica {
		return nil
	}
	return x.client.Del(ctx, key).Err()
}

func (x *ShellJobIndex) ClaimWork(ctx context.Context, s interfaces.SessionRef, workID string) (bool, error) {
	// Member carries the sandbox id; ZREM by the session+work pair alone is not
	// possible, so remove by range: find the member first.
	members, err := x.client.ZRangeByScore(ctx, shellJobIndexKey, &redis.ZRangeBy{
		Min: "-inf", Max: "+inf",
	}).Result()
	if err != nil {
		return false, err
	}
	prefix := x.sessionPrefix(s)
	for _, m := range members {
		if strings.HasPrefix(m, prefix) && strings.HasSuffix(m, ":"+workID) {
			n, err := x.client.ZRem(ctx, shellJobIndexKey, m).Result()
			if err != nil {
				return false, err
			}
			return n == 1, nil
		}
	}
	// Not in the index (already claimed by the submitter's ZREM): not ours.
	return false, nil
}

// SandboxIDs resolves the expected sandbox of each id from this session's index
// members (D1: the member carries sandbox_id). Ids absent from the index map to
// "" — the caller falls back to the current binding.
func (x *ShellJobIndex) SandboxIDs(ctx context.Context, s interfaces.SessionRef,
	ids []string,
) map[string]string {
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out
	}
	members, err := x.client.ZRange(ctx, shellJobIndexKey, 0, -1).Result()
	if err != nil {
		return out
	}
	prefix := x.sessionPrefix(s)
	byWork := make(map[string]string, len(members))
	for _, m := range members {
		if strings.HasPrefix(m, prefix) {
			rest := strings.TrimPrefix(m, prefix) // <sandbox_id>:<work_id>
			if i := strings.LastIndex(rest, ":"); i > 0 {
				byWork[rest[i+1:]] = rest[:i]
			}
		}
	}
	for _, id := range ids {
		out[id] = byWork[id]
	}
	return out
}

func (x *ShellJobIndex) sessionPrefix(s interfaces.SessionRef) string {
	return fmt.Sprintf("%s:%d:%s:", x.namespace, s.TenantID, s.SessionID)
}

func (x *ShellJobIndex) RemoveSession(ctx context.Context, s interfaces.SessionRef) error {
	members, err := x.client.ZRange(ctx, shellJobIndexKey, 0, -1).Result()
	if err != nil {
		return err
	}
	prefix := fmt.Sprintf("%s:%d:%s:", x.namespace, s.TenantID, s.SessionID)
	victims := make([]any, 0, len(members))
	for _, m := range members {
		if strings.HasPrefix(m, prefix) {
			victims = append(victims, m)
		}
	}
	if len(victims) == 0 {
		return nil
	}
	return x.client.ZRem(ctx, shellJobIndexKey, victims...).Err()
}

func (x *ShellJobIndex) hashTag(s interfaces.SessionRef) string {
	return fmt.Sprintf("%s:%d:%s", x.namespace, s.TenantID, s.SessionID)
}

func (x *ShellJobIndex) outcomeKey(s interfaces.SessionRef, workID string) string {
	return fmt.Sprintf("weknora:background:shelljob:outcome:%s:%d:%s:%s",
		x.namespace, s.TenantID, s.SessionID, workID)
}

// RecordOutcome keeps a terminal state where every replica can see it. The
// submitting process is the only one that can run a credential write-back, and
// an inbox notice can be acknowledged before that process sweeps.
func (x *ShellJobIndex) RecordOutcome(ctx context.Context, s interfaces.SessionRef, workID, state string) error {
	if x == nil || workID == "" || state == "" {
		return nil
	}
	return x.client.Set(ctx, x.outcomeKey(s, workID), state, interfaces.NoticeRetention).Err()
}

func (x *ShellJobIndex) Outcome(ctx context.Context, s interfaces.SessionRef, workID string) (string, bool, error) {
	if x == nil || workID == "" {
		return "", false, nil
	}
	val, err := x.client.Get(ctx, x.outcomeKey(s, workID)).Result()
	if err == redis.Nil {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if val == "" {
		return "", false, nil
	}
	return val, true, nil
}

func (x *ShellJobIndex) cursorKey(s interfaces.SessionRef, runID, workID string) string {
	return fmt.Sprintf("weknora:background:shelljob:cursor:%s:%d:%s:%s:%s",
		x.namespace, s.TenantID, s.SessionID, runID, workID)
}

func (x *ShellJobIndex) cursorRunKey(s interfaces.SessionRef, runID string) string {
	return fmt.Sprintf("weknora:background:shelljob:cursors:%s:%d:%s:%s",
		x.namespace, s.TenantID, s.SessionID, runID)
}

func (x *ShellJobIndex) cursorSessionKey(s interfaces.SessionRef) string {
	return fmt.Sprintf("weknora:background:shelljob:cursors:%s:%d:%s",
		x.namespace, s.TenantID, s.SessionID)
}

// PutCursor stores one run's read offset so another replica can continue it.
func (x *ShellJobIndex) PutCursor(ctx context.Context, s interfaces.SessionRef, runID, workID string,
	from sandbox.ShellJobOffsets, ttl time.Duration,
) error {
	if x == nil || runID == "" || workID == "" {
		return nil
	}
	if ttl <= 0 {
		ttl = time.Hour
	}
	raw, err := json.Marshal(from)
	if err != nil {
		return err
	}
	key := x.cursorKey(s, runID, workID)
	runSet := x.cursorRunKey(s, runID)
	sessionSet := x.cursorSessionKey(s)
	pipe := x.client.TxPipeline()
	pipe.Set(ctx, key, raw, ttl)
	pipe.SAdd(ctx, runSet, workID)
	pipe.Expire(ctx, runSet, ttl)
	pipe.SAdd(ctx, sessionSet, key, runSet)
	pipe.Expire(ctx, sessionSet, ttl)
	_, err = pipe.Exec(ctx)
	return err
}

func (x *ShellJobIndex) GetCursor(ctx context.Context, s interfaces.SessionRef, runID, workID string) (
	sandbox.ShellJobOffsets, bool, error,
) {
	if x == nil || runID == "" || workID == "" {
		return sandbox.ShellJobOffsets{}, false, nil
	}
	raw, err := x.client.Get(ctx, x.cursorKey(s, runID, workID)).Result()
	if err == redis.Nil {
		return sandbox.ShellJobOffsets{}, false, nil
	}
	if err != nil {
		return sandbox.ShellJobOffsets{}, false, err
	}
	var from sandbox.ShellJobOffsets
	if err := json.Unmarshal([]byte(raw), &from); err != nil {
		return sandbox.ShellJobOffsets{}, false, err
	}
	return from, true, nil
}

// TouchRunCursors extends every cursor for one run. EndShellJobRun calls it
// so the offsets live for an hour after the run, not after the last read.
func (x *ShellJobIndex) TouchRunCursors(ctx context.Context, s interfaces.SessionRef, runID string, ttl time.Duration) error {
	if x == nil || runID == "" {
		return nil
	}
	if ttl <= 0 {
		ttl = time.Hour
	}
	runSet := x.cursorRunKey(s, runID)
	ids, err := x.client.SMembers(ctx, runSet).Result()
	if err != nil {
		return err
	}
	pipe := x.client.TxPipeline()
	for _, id := range ids {
		pipe.Expire(ctx, x.cursorKey(s, runID, id), ttl)
	}
	pipe.Expire(ctx, runSet, ttl)
	pipe.Expire(ctx, x.cursorSessionKey(s), ttl)
	_, err = pipe.Exec(ctx)
	return err
}

// DeleteSessionCursors removes every cursor key recorded for the session.
func (x *ShellJobIndex) DeleteSessionCursors(ctx context.Context, s interfaces.SessionRef) error {
	if x == nil {
		return nil
	}
	sessionSet := x.cursorSessionKey(s)
	keys, err := x.client.SMembers(ctx, sessionSet).Result()
	if err != nil {
		return err
	}
	victims := make([]string, 0, len(keys)+1)
	victims = append(victims, keys...)
	victims = append(victims, sessionSet)
	return x.client.Del(ctx, victims...).Err()
}
