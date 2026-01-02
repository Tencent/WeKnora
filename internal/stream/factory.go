package stream

import (
	"os"
	"strconv"
	"time"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// 스트림 관리자 유형
const (
	TypeMemory = "memory"
	TypeRedis  = "redis"
)

// NewStreamManager 스트림 관리자 생성
func NewStreamManager() (interfaces.StreamManager, error) {
	switch os.Getenv("STREAM_MANAGER_TYPE") {
	case TypeRedis:
		db, err := strconv.Atoi(os.Getenv("REDIS_DB"))
		if err != nil {
			db = 0
		}
		ttl := time.Hour // 기본 1시간
		return NewRedisStreamManager(
			os.Getenv("REDIS_ADDR"),
			os.Getenv("REDIS_PASSWORD"),
			db,
			os.Getenv("REDIS_PREFIX"),
			ttl,
		)
	default:
		return NewMemoryStreamManager(), nil
	}
}
