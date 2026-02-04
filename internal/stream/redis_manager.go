package stream

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/redis/go-redis/v9"
)

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
		Addr:     redisAddr,
		Username: redisUsername,
		Password: redisPassword,
		DB:       redisDB,
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

	// No new events
	if len(results) == 0 {
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

	return events, nextOffset, nil
}

// Close closes the Redis connection
func (r *RedisStreamManager) Close() error {
	return r.client.Close()
}

// Ensure RedisStreamManager implements StreamManager interface
var _ interfaces.StreamManager = (*RedisStreamManager)(nil)
