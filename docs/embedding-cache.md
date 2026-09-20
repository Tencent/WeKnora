# Embedding result cache

Repeated indexing and queries can reuse successful embedding vectors. The cache is opt-in and sits outside the existing provider wrappers. Cache hits avoid provider calls; misses keep the existing provider, concurrency limiter, retries, and tracing path.

## Configuration

Set these variables in the server environment (or `.env` for the standard Docker Compose deployment):

```dotenv
WEKNORA_EMBEDDING_CACHE_ENABLED=true
WEKNORA_EMBEDDING_CACHE_TTL=168h
WEKNORA_EMBEDDING_CACHE_MEMORY_MAX_ENTRIES=2000
WEKNORA_EMBEDDING_CACHE_PREFIX=weknora:embedding:v1
WEKNORA_EMBEDDING_CACHE_DISTRIBUTED_LOCK_ENABLED=true
WEKNORA_EMBEDDING_CACHE_LOCK_LEASE=30s
WEKNORA_EMBEDDING_CACHE_LOCK_WAIT=2s
WEKNORA_EMBEDDING_CACHE_LOCK_RENEWAL=10s
```

Restart the server after changing these settings. With the switch unset or false, the original embedder is returned without cache access. When enabled, an available Redis client supplies shared TTL storage; deployments without Redis use an in-process LRU with a bounded entry count and TTL. Redis outages do not switch to a separate memory cache: requests fall back to the provider.

The prefix must start with `weknora:` and contain no whitespace or wildcards. Use a different prefix for experiments and delete only that prefix's keys. The memory limit is a count of vectors, not a byte budget. Redis capacity and eviction remain deployment settings.

## Identity and invalidation

Keys bind the cache version, tenant, persisted model ID and update timestamp, provider/source, model name, endpoint, dimensions, truncation and extra model configuration, and original UTF-8 text bytes. Text is not trimmed or normalized. Keys contain hashes rather than raw text or credentials. Changes to persisted model configuration, including custom headers and credentials, invalidate entries through the model update timestamp.

Shared-model lookups use the explicitly selected model tenant. Calls without a tenant bypass caching. Changing an embedding model can also require rebuilding knowledge-base vectors; invalidating the cache does not migrate an existing vector index.

## Batch and failure behavior

- `Embed`, `BatchEmbed`, and `BatchEmbedWithPool` support reuse. Batches deduplicate identical texts, send only misses, and restore the original result order with independent vector slices. Pooled calls filter misses before entering the existing batch pool.
- Concurrent identical misses on a wrapper share a single in-flight calculation. Redis additionally coordinates identical single requests or identical ordered miss batches across wrappers/processes, with token-owned locks and renewal. Partially overlapping batches can still issue duplicate provider requests.
- Redis lock acquisition waits are bounded. Acquisition errors/timeouts fall back to the provider. Cache reads/writes and lock release failures do not replace the provider result.
- Successful vectors are cached only when their dimensions are valid and every value is finite. Provider errors, mismatched batch lengths, and canceled requests are not cached.
- Existing waiters can cancel without waiting for the leader. The shared computation uses the leader's context, so cancellation of the leader can fail that in-flight group; later requests can retry.

## Observability and validation

`embedding.CacheMetrics(cache)` returns process-local counters for logical requests, hit/miss input positions, provider operations/items, storage errors, invalid entries, and coalescing/lock fallback activity. A pooled miss counts as one provider operation here; the pool can split it into several HTTP requests. These are application-cache counters, not provider prompt-cache token statistics or billing. No statistics endpoint is added.

```bash
go test ./internal/models/embedding ./internal/application/service ./internal/container -count=1
go test -race ./internal/models/embedding -run 'Test(ResultCache|EmbeddingCache|EmbeddingRedisLock|MemoryResultCache|NewEmbeddingResultCache|WrapResultCache)' -count=1
go test ./internal/models/embedding -run '^$' -bench '^BenchmarkResultCacheModes$' -benchmem
```

The benchmark uses a deterministic fake provider to compare disabled, cold, and warm modes. Its provider-call counts validate reuse; its timings are not estimates of production latency. Redis protocol tests use miniredis, including separate clients; they do not substitute for deployment/load testing.

To exercise real Redis storage, TTL, cross-client reuse, and corrupt-entry recovery, point `WEKNORA_TEST_REDIS_ADDR` at a dedicated test instance and run `go test ./internal/models/embedding -run '^TestResultCacheRedisIntegration$' -count=1 -v`. This test allocates a unique key prefix and removes only its own entries.
