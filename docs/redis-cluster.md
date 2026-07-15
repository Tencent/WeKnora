# Redis standalone and Cluster

WeKnora supports optional Redis Cluster for production topologies while keeping the default standalone behavior.

| Mode | `REDIS_MODE` | Connection |
|------|--------------|------------|
| Standalone (default) | empty or `single` | `REDIS_ADDR` (+ optional `REDIS_DB`) |
| Cluster | `cluster` | `REDIS_CLUSTER_ADDRS` = comma-separated `host:port` |
| Lite (no Redis) | empty | leave `REDIS_ADDR` empty and do not set cluster mode |

Shared options: `REDIS_USERNAME`, `REDIS_PASSWORD`, and TLS via `REDIS_USE_TLS` / `REDIS_TLS_SERVER_NAME` / `REDIS_TLS_INSECURE_SKIP_VERIFY` (see `.env.example`).

Example cluster:

```bash
REDIS_MODE=cluster
REDIS_CLUSTER_ADDRS=10.0.1.1:6379,10.0.1.2:6379,10.0.1.3:6379
REDIS_PASSWORD=...
# optional
REDIS_USE_TLS=true
REDIS_TLS_SERVER_NAME=redis.example.com
```

Compose and Helm forward `REDIS_MODE` and `REDIS_CLUSTER_ADDRS`. Asynq task queues use the same mode switch as the application Redis client (`redis.UniversalClient`).

**Note:** Some legacy injectors still receive `*redis.Client`, which is only non-nil in standalone mode. Prefer `redis.UniversalClient` for new code so both modes work.
