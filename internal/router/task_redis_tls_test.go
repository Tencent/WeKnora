package router

import (
	"crypto/tls"
	"os"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetAsynqRedisConnOpt_StandaloneTLS(t *testing.T) {
	t.Setenv("REDIS_MODE", "")
	t.Setenv("REDIS_ADDR", "127.0.0.1:6379")
	t.Setenv("REDIS_USE_TLS", "true")
	t.Setenv("REDIS_TLS_INSECURE_SKIP_VERIFY", "")
	t.Setenv("REDIS_TLS_SERVER_NAME", "")

	opt, ok := getAsynqRedisConnOpt().(*asynq.RedisClientOpt)
	require.True(t, ok)
	require.NotNil(t, opt.TLSConfig)
	assert.Equal(t, uint16(tls.VersionTLS12), opt.TLSConfig.MinVersion)
	assert.False(t, opt.TLSConfig.InsecureSkipVerify)
}

func TestGetAsynqRedisConnOpt_ClusterTLS(t *testing.T) {
	t.Setenv("REDIS_MODE", "cluster")
	t.Setenv("REDIS_CLUSTER_ADDRS", "10.0.0.1:6379,10.0.0.2:6379")
	t.Setenv("REDIS_USE_TLS", "true")
	t.Setenv("REDIS_TLS_SERVER_NAME", "redis.internal")
	t.Setenv("REDIS_TLS_INSECURE_SKIP_VERIFY", "true")

	opt, ok := getAsynqRedisConnOpt().(*asynq.RedisClusterClientOpt)
	require.True(t, ok)
	require.NotNil(t, opt.TLSConfig)
	assert.Equal(t, "redis.internal", opt.TLSConfig.ServerName)
	assert.True(t, opt.TLSConfig.InsecureSkipVerify)
	assert.Equal(t, []string{"10.0.0.1:6379", "10.0.0.2:6379"}, opt.Addrs)
}

func TestGetAsynqRedisConnOpt_PlaintextDefault(t *testing.T) {
	t.Setenv("REDIS_MODE", "cluster")
	t.Setenv("REDIS_CLUSTER_ADDRS", "10.0.0.1:6379")
	os.Unsetenv("REDIS_USE_TLS")

	opt, ok := getAsynqRedisConnOpt().(*asynq.RedisClusterClientOpt)
	require.True(t, ok)
	assert.Nil(t, opt.TLSConfig)
}
