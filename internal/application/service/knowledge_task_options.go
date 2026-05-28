package service

import (
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/hibiken/asynq"
)

func documentProcessTaskOptions(cfg *config.Config, extra ...asynq.Option) []asynq.Option {
	timeout := 2 * time.Hour
	if cfg != nil &&
		cfg.KnowledgeBase != nil &&
		cfg.KnowledgeBase.DocumentProcessTimeout > 0 {
		timeout = cfg.KnowledgeBase.DocumentProcessTimeout
	}

	opts := []asynq.Option{
		asynq.Queue("default"),
		asynq.Timeout(timeout),
	}
	opts = append(opts, extra...)
	return opts
}
