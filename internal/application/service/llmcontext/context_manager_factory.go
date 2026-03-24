package llmcontext

import (
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// NewContextManagerFromConfig creates a ContextManager.
// messageRepo is optional — when provided, context will be rebuilt from
// the persistent messages table if the cache (Redis/memory) is empty.
func NewContextManagerFromConfig(
	storage ContextStorage,
	messageRepo interfaces.MessageRepository,
) interfaces.ContextManager {
	if storage == nil {
		storage = NewMemoryStorage()
	}
	return NewContextManager(storage, messageRepo)
}
