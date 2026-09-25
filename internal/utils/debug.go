package utils

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// CleanupStaleRunningTasks 清理可能残留的running task keys
// 这是一个调试和维护工具，可以用来清理因异常情况导致的残留running keys
func CleanupStaleRunningTasks(ctx context.Context, redisClient *redis.Client, keyPrefix string, maxAge time.Duration) (int, error) {
	// 获取所有匹配的keys
	keys, err := redisClient.Keys(ctx, keyPrefix+"*").Result()
	if err != nil {
		return 0, fmt.Errorf("failed to get keys: %w", err)
	}

	if len(keys) == 0 {
		return 0, nil
	}

	// 检查每个key的TTL
	var staleTasks []string
	for _, key := range keys {
		ttl, err := redisClient.TTL(ctx, key).Result()
		if err != nil {
			continue // 跳过错误的key
		}

		// 如果TTL小于0（永不过期）或者剩余时间太长（可能是残留的），标记为stale
		if ttl < 0 || ttl > maxAge {
			staleTasks = append(staleTasks, key)
		}
	}

	if len(staleTasks) == 0 {
		return 0, nil
	}

	// 删除stale keys
	deleted, err := redisClient.Del(ctx, staleTasks...).Result()
	if err != nil {
		return 0, fmt.Errorf("failed to delete stale keys: %w", err)
	}

	return int(deleted), nil
}

// CheckRunningTaskStatus 检查指定running task的状态
func CheckRunningTaskStatus(ctx context.Context, redisClient *redis.Client, runningKey, progressKey string) (map[string]interface{}, error) {
	result := make(map[string]interface{})

	// 检查running key
	runningTaskID, err := redisClient.Get(ctx, runningKey).Result()
	if err != nil {
		if err == redis.Nil {
			result["running_task_exists"] = false
		} else {
			return nil, fmt.Errorf("failed to get running task: %w", err)
		}
	} else {
		result["running_task_exists"] = true
		result["running_task_id"] = runningTaskID

		// 获取running key的TTL
		ttl, _ := redisClient.TTL(ctx, runningKey).Result()
		result["running_task_ttl"] = ttl.String()
	}

	// 检查progress key
	progressData, err := redisClient.Get(ctx, progressKey).Result()
	if err != nil {
		if err == redis.Nil {
			result["progress_exists"] = false
		} else {
			return nil, fmt.Errorf("failed to get progress: %w", err)
		}
	} else {
		result["progress_exists"] = true
		result["progress_data"] = progressData

		// 获取progress key的TTL
		ttl, _ := redisClient.TTL(ctx, progressKey).Result()
		result["progress_ttl"] = ttl.String()
	}

	return result, nil
}
