package router

import (
	"errors"
	"strings"

	"github.com/hibiken/asynq"
)

const (
	asynqQueueNotFoundPrefix = `NOT_FOUND: queue "`
	asynqQueueNotFoundSuffix = `" does not exist`
)

// isAsynqQueueNotFound handles both the public sentinel and the internal error
// leaked by Inspector.GetQueueInfo in asynq v0.26.0.
func isAsynqQueueNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, asynq.ErrQueueNotFound) {
		return true
	}

	message := err.Error()
	start := strings.LastIndex(message, asynqQueueNotFoundPrefix)
	if start < 0 || (start > 0 && !strings.HasSuffix(message[:start], ": ")) {
		return false
	}
	queueAndSuffix := message[start+len(asynqQueueNotFoundPrefix):]
	if !strings.HasSuffix(queueAndSuffix, asynqQueueNotFoundSuffix) {
		return false
	}
	queue := strings.TrimSuffix(queueAndSuffix, asynqQueueNotFoundSuffix)
	return queue != "" && !strings.Contains(queue, `"`)
}
