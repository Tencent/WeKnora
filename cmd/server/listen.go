package main

import (
	"context"
	"net"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
)

func listenWithRetry(addr string, maxRetries int, baseDelay time.Duration) (net.Listener, error) {
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		listener, err := net.Listen("tcp", addr)
		if err == nil {
			return listener, nil
		}
		lastErr = err
		if i < maxRetries-1 {
			delay := baseDelay * time.Duration(1<<uint(i))
			if delay > 3*time.Second {
				delay = 3 * time.Second
			}
			logger.Warnf(context.Background(), "Port %s in use, retrying in %v... (%d/%d)", addr, delay, i+1, maxRetries)
			time.Sleep(delay)
		}
	}
	return nil, lastErr
}
