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
		ttl := time.Hour // 默认1小时
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
