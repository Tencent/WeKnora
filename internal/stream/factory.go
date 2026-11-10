package stream

import (
	"os"
	"strconv"
	"time"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// 流管理器类型
const (
	TypeMemory = "memory"
	TypeRedis  = "redis"
)

// NewStreamManager 创建流管理器
func NewStreamManager() (interfaces.StreamManager, error) {
	switch os.Getenv("STREAM_MANAGER_TYPE") {
	case TypeRedis:
		db, err := strconv.Atoi(os.Getenv("REDIS_DB"))
		if err != nil {
			db = 0
		}
		// Default 1h. Live-run keys are refreshed while the turn is still
		// streaming (AppendEvent / GetEvents / steer writes), so a run that
		// lasts longer than this TTL does not look idle to /steer.
		ttl := time.Hour
		return NewRedisStreamManager(
			os.Getenv("REDIS_ADDR"),
			os.Getenv("REDIS_USERNAME"),
			os.Getenv("REDIS_PASSWORD"),
			db,
			os.Getenv("REDIS_PREFIX"),
			ttl,
		)
	default:
		return NewMemoryStreamManager(), nil
	}
}
