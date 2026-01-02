package container

import (
	"context"
	"log"
	"sync"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ResourceCleaner 리소스를 정리하는 데 사용할 수 있는 리소스 정리기입니다.
type ResourceCleaner struct {
	mu       sync.Mutex
	cleanups []types.CleanupFunc
}

// NewResourceCleaner 새로운 리소스 정리기를 생성합니다.
func NewResourceCleaner() interfaces.ResourceCleaner {
	return &ResourceCleaner{
		cleanups: make([]types.CleanupFunc, 0),
	}
}

// Register 정리 함수를 등록합니다.
// 참고: 정리 함수는 역순으로 실행됩니다 (마지막에 등록된 것이 먼저 실행됨).
func (c *ResourceCleaner) Register(cleanup types.CleanupFunc) {
	if cleanup == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.cleanups = append(c.cleanups, cleanup)
}

// RegisterWithName 로깅 추적을 위해 이름을 가진 정리 함수를 등록합니다.
func (c *ResourceCleaner) RegisterWithName(name string, cleanup types.CleanupFunc) {
	if cleanup == nil {
		return
	}

	wrappedCleanup := func() error {
		log.Printf("Cleaning up resource: %s", name)
		err := cleanup()
		if err != nil {
			log.Printf("Error cleaning up resource %s: %v", name, err)
		} else {
			log.Printf("Successfully cleaned up resource: %s", name)
		}
		return err
	}

	c.Register(wrappedCleanup)
}

// Cleanup 모든 정리 함수를 실행합니다.
// 정리 함수 중 하나가 실패하더라도 다른 정리 함수는 계속 실행됩니다.
func (c *ResourceCleaner) Cleanup(ctx context.Context) (errs []error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 정리 함수를 역순으로 실행합니다 (마지막에 등록된 것이 먼저 실행됨).
	for i := len(c.cleanups) - 1; i >= 0; i-- {
		select {
		case <-ctx.Done():
			errs = append(errs, ctx.Err())
			return errs
		default:
			if err := c.cleanups[i](); err != nil {
				errs = append(errs, err)
			}
		}
	}

	return errs
}

// Reset 등록된 모든 정리 함수를 지웁니다.
func (c *ResourceCleaner) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cleanups = make([]types.CleanupFunc, 0)
}
