package service

import (
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/hibiken/asynq"
)

func documentProcessTaskOptions(cfg *config.Config, extra ...asynq.Option) []asynq.Option {
	opts := []asynq.Option{
		asynq.Queue("default"),
		asynq.Timeout(config.DocumentProcessTimeout(cfg)),
	}
	opts = append(opts, extra...)
	return opts
}
