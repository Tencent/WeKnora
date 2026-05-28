package service

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseDocumentProcessOpts(t *testing.T, opts []asynq.Option) (queue string, timeout time.Duration, maxRetry *int) {
	t.Helper()
	for _, opt := range opts {
		switch opt.Type() {
		case asynq.QueueOpt:
			queue, _ = opt.Value().(string)
		case asynq.TimeoutOpt:
			timeout, _ = opt.Value().(time.Duration)
		case asynq.MaxRetryOpt:
			n, _ := opt.Value().(int)
			maxRetry = &n
		default:
			t.Fatalf("unexpected asynq option type %v", opt.Type())
		}
	}
	return queue, timeout, maxRetry
}

func TestDocumentProcessTaskOptions_defaults(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  *config.Config
	}{
		{"nil config", nil},
		{"nil knowledge base", &config.Config{}},
		{"zero timeout", &config.Config{KnowledgeBase: &config.KnowledgeBaseConfig{}}},
		{"negative timeout", &config.Config{
			KnowledgeBase: &config.KnowledgeBaseConfig{DocumentProcessTimeout: -time.Minute},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts := documentProcessTaskOptions(tc.cfg)
			queue, timeout, maxRetry := parseDocumentProcessOpts(t, opts)
			assert.Equal(t, "default", queue)
			assert.Equal(t, config.DefaultDocumentProcessTimeout, timeout)
			assert.Nil(t, maxRetry)
		})
	}
}

func TestDocumentProcessTaskOptions_configuredTimeout(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		KnowledgeBase: &config.KnowledgeBaseConfig{
			DocumentProcessTimeout: 90 * time.Minute,
		},
	}
	opts := documentProcessTaskOptions(cfg)
	queue, timeout, maxRetry := parseDocumentProcessOpts(t, opts)
	assert.Equal(t, "default", queue)
	assert.Equal(t, 90*time.Minute, timeout)
	assert.Nil(t, maxRetry)
}

func TestDocumentProcessTaskOptions_extraMaxRetry(t *testing.T) {
	t.Parallel()
	opts := documentProcessTaskOptions(nil, asynq.MaxRetry(3))
	queue, timeout, maxRetry := parseDocumentProcessOpts(t, opts)
	assert.Equal(t, "default", queue)
	assert.Equal(t, config.DefaultDocumentProcessTimeout, timeout)
	require.NotNil(t, maxRetry)
	assert.Equal(t, 3, *maxRetry)
}
