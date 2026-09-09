package stream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/redis/go-redis/v9"
)

// ErrLiveRunExists is returned by SetLiveRun when another assistant already
// holds the session. Follow-up handoff uses ClaimLiveRun to overwrite.
var ErrLiveRunExists = errors.New("session already has a live run")

// RedisStreamManager implements StreamManager using Redis Lists for append-only event streaming
type RedisStreamManager struct {
	client *redis.Client
	ttl    time.Duration // TTL for stream data in Redis
	prefix string        // Redis key prefix
}

// NewRedisStreamManager creates a new Redis-based stream manager
func NewRedisStreamManager(redisAddr, redisUsername, redisPassword string,
	redisDB int, prefix string, ttl time.Duration,
) (*RedisStreamManager, error) {
	client := redis.NewClient(&redis.Options{
		Addr:      redisAddr,
		Username:  redisUsername,
		Password:  redisPassword,
		DB:        redisDB,
		TLSConfig: common.RedisTLSConfig(),
	})

	// Verify connection
	_, err := client.Ping(context.Background()).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	if ttl == 0 {
		ttl = 24 * time.Hour // Default TTL: 24 hours
	}

	if prefix == "" {
		prefix = "stream:events" // Default prefix
	}

	return &RedisStreamManager{
		client: client,
		ttl:    ttl,
		prefix: prefix,
	}, nil
}

// buildKey builds the Redis key for event list
func (r *RedisStreamManager) buildKey(sessionID, messageID string) string {
	return fmt.Sprintf("%s:%s:%s", r.prefix, sessionID, messageID)
}

// buildSteerKey builds the Redis key for the per-run steer sub-list. Keeping it
// in a separate key keeps control events off the user-visible SSE stream while
// reusing the same TTL lifecycle.
func (r *RedisStreamManager) buildSteerKey(sessionID, messageID string) string {
	return fmt.Sprintf("%s:%s:%s:steer", r.prefix, sessionID, messageID)
}

// AppendEvent appends a single event to the stream using Redis RPush
func (r *RedisStreamManager) AppendEvent(
	ctx context.Context,
	sessionID, messageID string,
	event interfaces.StreamEvent,
) error {
	key := r.buildKey(sessionID, messageID)

	// Set timestamp if not already set
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	// Serialize event to JSON
	eventJSON, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	// Append to Redis list with RPush (O(1) operation)
	if err := r.client.RPush(ctx, key, eventJSON).Err(); err != nil {
		return fmt.Errorf("failed to append event to Redis: %w", err)
	}

	// Set/refresh TTL on the key
	if err := r.client.Expire(ctx, key, r.ttl).Err(); err != nil {
		return fmt.Errorf("failed to set TTL: %w", err)
	}
	r.touchLiveRun(ctx, sessionID)

	return nil
}

// GetEvents gets events starting from offset using Redis LRange
// Returns: events slice, next offset, error
func (r *RedisStreamManager) GetEvents(
	ctx context.Context,
	sessionID, messageID string,
	fromOffset int,
) ([]interfaces.StreamEvent, int, error) {
	key := r.buildKey(sessionID, messageID)

	// Get all events from offset to end using LRange
	// LRange is inclusive, so fromOffset to -1 gets all remaining elements
	results, err := r.client.LRange(ctx, key, int64(fromOffset), -1).Result()
	if err != nil {
		if err == redis.Nil {
			// Key doesn't exist - return empty slice
			return []interfaces.StreamEvent{}, fromOffset, nil
		}
		return nil, fromOffset, fmt.Errorf("failed to get events from Redis: %w", err)
	}

	// No new events. Still refresh the live-run marker: the SSE poll loop
	// hits this path while the model is thinking, which is exactly when a
	// long turn would otherwise outlive the one-shot SetLiveRun TTL.
	if len(results) == 0 {
		r.touchLiveRun(ctx, sessionID)
		return []interfaces.StreamEvent{}, fromOffset, nil
	}

	// Unmarshal events
	events := make([]interfaces.StreamEvent, 0, len(results))
	for _, result := range results {
		var event interfaces.StreamEvent
		if err := json.Unmarshal([]byte(result), &event); err != nil {
			// Log error but continue with other events
			continue
		}
		events = append(events, event)
	}

	// Calculate next offset
	nextOffset := fromOffset + len(results)
	r.touchLiveRun(ctx, sessionID)

	return events, nextOffset, nil
}

// AppendSteerEvents appends control events to the per-run steer sub-list.
func (r *RedisStreamManager) AppendSteerEvents(
	ctx context.Context,
	sessionID, messageID string,
	events []interfaces.StreamEvent,
) error {
	if len(events) == 0 {
		return nil
	}
	key := r.buildSteerKey(sessionID, messageID)

	payloads := make([]interface{}, 0, len(events))
	for i := range events {
		if events[i].Timestamp.IsZero() {
			events[i].Timestamp = time.Now()
		}
		eventJSON, err := json.Marshal(events[i])
		if err != nil {
			return fmt.Errorf("failed to marshal steer event: %w", err)
		}
		payloads = append(payloads, eventJSON)
	}

	// Deduplication is atomic with append: a timed-out POST can be retried
	// while another replica is still accepting the same client ID.
	args := append([]interface{}{int64(r.ttl / time.Second)}, payloads...)
	if err := steerAppendUnique.Run(ctx, r.client, []string{key}, args...).Err(); err != nil {
		return fmt.Errorf("failed to append steer events to Redis: %w", err)
	}
	_ = r.client.Expire(ctx, r.buildLiveRunKey(sessionID), r.ttl).Err()
	return nil
}

var steerAppendUnique = redis.NewScript(`
local seen = {}
for _, raw in ipairs(redis.call('LRANGE', KEYS[1], 0, -1)) do
  local ok, event = pcall(cjson.decode, raw)
  if ok and event.id then seen[event.id] = true end
end
for i = 2, #ARGV do
  local event = cjson.decode(ARGV[i])
  if not event.id or event.id == '' or not seen[event.id] then
    redis.call('RPUSH', KEYS[1], ARGV[i])
    if event.id then seen[event.id] = true end
  end
end
redis.call('EXPIRE', KEYS[1], ARGV[1])
return 1
`)

// GetSteerEvents drains the steer sub-list starting fromOffset.
func (r *RedisStreamManager) GetSteerEvents(
	ctx context.Context,
	sessionID, messageID string,
	fromOffset int,
) ([]interfaces.StreamEvent, int, error) {
	key := r.buildSteerKey(sessionID, messageID)

	results, err := r.client.LRange(ctx, key, int64(fromOffset), -1).Result()
	if err != nil {
		if err == redis.Nil {
			return []interfaces.StreamEvent{}, fromOffset, nil
		}
		return nil, fromOffset, fmt.Errorf("failed to get steer events from Redis: %w", err)
	}
	if len(results) == 0 {
		r.touchLiveRun(ctx, sessionID)
		return []interfaces.StreamEvent{}, fromOffset, nil
	}

	events := make([]interfaces.StreamEvent, 0, len(results))
	for _, result := range results {
		var event interfaces.StreamEvent
		if err := json.Unmarshal([]byte(result), &event); err != nil {
			continue
		}
		events = append(events, event)
	}

	r.touchLiveRun(ctx, sessionID)
	return events, fromOffset + len(results), nil
}

// steerUpdateCAS rewrites one list slot only if it still holds the value we
// read. Rebuilding the whole list (DEL + RPUSH) would silently drop a steer
// message appended by another replica in between, so every mutation is either
// a compare-and-set on a single index or an LREM of an exact value.
var steerUpdateCAS = redis.NewScript(`
if redis.call('LINDEX', KEYS[1], ARGV[1]) ~= ARGV[2] then
  return 0
end
redis.call('LSET', KEYS[1], ARGV[1], ARGV[3])
return 1
`)

// steerUpdateMaxAttempts bounds the CAS retry loop. Contention here is one
// user pressing buttons on their own queue, so a lost race is rare and a
// couple of retries is plenty.
const steerUpdateMaxAttempts = 3

// UpdateSteerEventData merges keys into a queued steer event's Data map.
func (r *RedisStreamManager) UpdateSteerEventData(
	ctx context.Context,
	sessionID, messageID, eventID string,
	data map[string]interface{},
) (bool, error) {
	key := r.buildSteerKey(sessionID, messageID)

	for attempt := 0; attempt < steerUpdateMaxAttempts; attempt++ {
		results, err := r.client.LRange(ctx, key, 0, -1).Result()
		if err != nil {
			if err == redis.Nil {
				return false, nil
			}
			return false, fmt.Errorf("failed to read steer events from Redis: %w", err)
		}

		index := -1
		var current string
		var event interfaces.StreamEvent
		for i, result := range results {
			var candidate interfaces.StreamEvent
			if err := json.Unmarshal([]byte(result), &candidate); err != nil {
				continue
			}
			if candidate.ID != eventID {
				continue
			}
			index, current, event = i, result, candidate
			break
		}
		if index < 0 {
			return false, nil
		}

		if event.Data == nil {
			event.Data = map[string]interface{}{}
		}
		for k, v := range data {
			event.Data[k] = v
		}
		payload, err := json.Marshal(event)
		if err != nil {
			return false, fmt.Errorf("failed to marshal steer event: %w", err)
		}

		updated, err := steerUpdateCAS.Run(ctx, r.client, []string{key}, index, current, payload).Int()
		if err != nil {
			return false, fmt.Errorf("failed to update steer event in Redis: %w", err)
		}
		if updated == 1 {
			_ = r.client.Expire(ctx, key, r.ttl).Err()
			return true, nil
		}
		// Someone rewrote or removed that slot; re-read and try again.
	}
	return false, fmt.Errorf("steer event %s changed concurrently, giving up after %d attempts",
		eventID, steerUpdateMaxAttempts)
}

// DeleteSteerEvent removes a queued steer event by ID from Redis.
func (r *RedisStreamManager) DeleteSteerEvent(
	ctx context.Context,
	sessionID, messageID, eventID string,
) (bool, error) {
	key := r.buildSteerKey(sessionID, messageID)
	for attempt := 0; attempt < steerUpdateMaxAttempts; attempt++ {
		results, err := r.client.LRange(ctx, key, 0, -1).Result()
		if err != nil {
			if err == redis.Nil {
				return false, nil
			}
			return false, fmt.Errorf("failed to read steer events from Redis: %w", err)
		}

		var current string
		var event interfaces.StreamEvent
		found := false
		for _, result := range results {
			if err := json.Unmarshal([]byte(result), &event); err != nil {
				continue
			}
			if event.ID != eventID {
				continue
			}
			current = result
			found = true
			break
		}
		if !found {
			return false, nil
		}
		if event.Data != nil {
			if consumed, _ := event.Data["consumed"].(bool); consumed {
				return false, nil
			}
		}
		removed, err := r.client.LRem(ctx, key, 1, current).Result()
		if err != nil {
			return false, fmt.Errorf("failed to delete steer event from Redis: %w", err)
		}
		if removed > 0 {
			return true, nil
		}
	}
	return false, nil
}

// buildLiveRunKey builds the Redis key holding the session's generating run.
func (r *RedisStreamManager) buildLiveRunKey(sessionID string) string {
	return fmt.Sprintf("%s:%s:live-run", r.prefix, sessionID)
}

// liveRunPayload is the stored shape of the live-run marker.
type liveRunPayload struct {
	AssistantMessageID string `json:"assistant_message_id"`
	RequestID          string `json:"request_id"`
}

func (r *RedisStreamManager) marshalLiveRun(assistantMessageID, requestID string) ([]byte, error) {
	payload, err := json.Marshal(liveRunPayload{
		AssistantMessageID: assistantMessageID,
		RequestID:          requestID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal live run marker: %w", err)
	}
	return payload, nil
}

// SetLiveRun records the session's currently generating assistant message so
// every replica routes steer requests to the same run. It refuses to
// replace a different live assistant; ClaimLiveRun is the overwrite path.
func (r *RedisStreamManager) SetLiveRun(
	ctx context.Context,
	sessionID, assistantMessageID, requestID string,
) error {
	payload, err := r.marshalLiveRun(assistantMessageID, requestID)
	if err != nil {
		return err
	}
	ok, err := r.client.SetNX(ctx, r.buildLiveRunKey(sessionID), payload, r.ttl).Result()
	if err != nil {
		return fmt.Errorf("failed to set live run marker in Redis: %w", err)
	}
	if ok {
		return nil
	}
	current, _, err := r.GetLiveRun(ctx, sessionID)
	if err != nil {
		return err
	}
	if current == assistantMessageID {
		return nil
	}
	if current == "" {
		ok, err = r.client.SetNX(ctx, r.buildLiveRunKey(sessionID), payload, r.ttl).Result()
		if err != nil {
			return fmt.Errorf("failed to set live run marker in Redis: %w", err)
		}
		if ok {
			return nil
		}
	}
	return ErrLiveRunExists
}

// ClaimLiveRun overwrites the live-run marker for follow-up handoff.
func (r *RedisStreamManager) ClaimLiveRun(
	ctx context.Context,
	sessionID, assistantMessageID, requestID string,
) error {
	payload, err := r.marshalLiveRun(assistantMessageID, requestID)
	if err != nil {
		return err
	}
	if err := r.client.Set(ctx, r.buildLiveRunKey(sessionID), payload, r.ttl).Err(); err != nil {
		return fmt.Errorf("failed to claim live run marker in Redis: %w", err)
	}
	return nil
}

// GetLiveRun returns the session's generating assistant message, if any.
func (r *RedisStreamManager) GetLiveRun(
	ctx context.Context,
	sessionID string,
) (string, string, error) {
	raw, err := r.client.Get(ctx, r.buildLiveRunKey(sessionID)).Result()
	if err != nil {
		if err == redis.Nil {
			return "", "", nil
		}
		return "", "", fmt.Errorf("failed to read live run marker from Redis: %w", err)
	}
	var payload liveRunPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		// Do not collapse a corrupt marker into "no run is live": callers
		// treat empty as new_run and would start a second AgentQA.
		return "", "", fmt.Errorf("failed to decode live run marker: %w", err)
	}
	r.touchLiveRun(ctx, sessionID)
	return payload.AssistantMessageID, payload.RequestID, nil
}

// touchLiveRun extends the live-run marker so a turn longer than the
// StreamManager TTL does not look idle. Expire on a missing key is a no-op.
func (r *RedisStreamManager) touchLiveRun(ctx context.Context, sessionID string) {
	if sessionID == "" {
		return
	}
	_ = r.client.Expire(ctx, r.buildLiveRunKey(sessionID), r.ttl).Err()
}

// clearLiveRunCAS deletes the marker only when it still names the run being
// torn down, so a follow-up run that already claimed the session keeps it.
var clearLiveRunCAS = redis.NewScript(`
local raw = redis.call('GET', KEYS[1])
if not raw then
  return 0
end
if string.find(raw, ARGV[1], 1, true) == nil then
  return 0
end
redis.call('DEL', KEYS[1])
return 1
`)

// ClearLiveRun drops the marker only when it still points at assistantMessageID.
func (r *RedisStreamManager) ClearLiveRun(
	ctx context.Context,
	sessionID, assistantMessageID string,
) error {
	if assistantMessageID == "" {
		return nil
	}
	// Match the serialized field rather than decoding in Lua: the marker is
	// written by SetLiveRun above, so the encoding is ours to rely on.
	idJSON, err := json.Marshal(assistantMessageID)
	if err != nil {
		return fmt.Errorf("failed to marshal assistant message id: %w", err)
	}
	needle := `"assistant_message_id":` + string(idJSON)
	if err := clearLiveRunCAS.Run(ctx, r.client,
		[]string{r.buildLiveRunKey(sessionID)}, needle).Err(); err != nil && err != redis.Nil {
		return fmt.Errorf("failed to clear live run marker in Redis: %w", err)
	}
	return nil
}

// Close closes the Redis connection
func (r *RedisStreamManager) Close() error {
	return r.client.Close()
}

// Ensure RedisStreamManager implements StreamManager interface
var _ interfaces.StreamManager = (*RedisStreamManager)(nil)
