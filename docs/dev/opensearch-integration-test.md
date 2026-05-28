# OpenSearch k-NN driver — local integration test

This guide brings up a single-node OpenSearch cluster and exercises the
OpenSearch retrieve engine end to end. The driver lives in
`internal/application/repository/retriever/opensearch/`.

## 1. Start a dev cluster

```bash
docker compose -f docker-compose.dev.yml --profile opensearch up -d
```

This starts:

- `opensearch` on `http://localhost:9200` — single-node, **security plugin
  disabled** (plain HTTP, no auth/TLS). The image bundles the
  `opensearch-knn` plugin.

> **OpenSearch Dashboards is optional** and lives in a separate
> `opensearch-ui` profile, so it is *not* started by `--profile opensearch`.
> The whole integration test below is curl-verifiable against `:9200`. If you
> want the web UI (Dev Tools console / visual index inspection), start it on
> demand:
>
> ```bash
> docker compose -f docker-compose.dev.yml --profile opensearch-ui up -d
> # opensearch-dashboards on http://localhost:5601 (depends_on pulls the cluster in)
> ```

Verify:

```bash
curl -s localhost:9200 | jq '.version.distribution, .version.number'
# "opensearch" "3.3.2"
curl -s 'localhost:9200/_cat/plugins?format=json' | jq -r '.[].component' | grep opensearch-knn
```

> Production clusters must enable the security plugin (TLS + auth). The dev
> profile disables it only to keep local setup trivial. When connecting to a
> secured cluster, set `username` / `password` and — for self-signed certs in
> dev only — `insecure_skip_verify=true`.

## 2. Register the store

### Option A — DB store (UI / API)

`POST /api/v1/vector-stores`:

```json
{
  "name": "opensearch-local",
  "engine_type": "opensearch",
  "connection_config": { "addr": "http://localhost:9200" },
  "index_config": {
    "number_of_shards": 1,
    "number_of_replicas": 0,
    "hnsw_m": 16,
    "hnsw_ef_construction": 100,
    "knn_engine": "lucene"
  }
}
```

`CreateStore` runs the connection probe (version + k-NN plugin) before
persisting; a bad address / unsupported version / missing plugin is rejected
with `400`.

### Option B — env store

```bash
export RETRIEVE_DRIVER=opensearch
export OPENSEARCH_ADDR=http://localhost:9200
# export OPENSEARCH_USERNAME / OPENSEARCH_PASSWORD for a secured cluster
# export OPENSEARCH_INSECURE_SKIP_VERIFY=true   # self-signed dev TLS only
```

## 3. Single-node note (important)

On a single-node cluster, any index created with `number_of_replicas >= 1`
leaves its replica shard **unassigned**, so the index health goes **Yellow**.
Yellow does **not** block reads or writes — it is safe for local testing — but
to keep the cluster Green set **`number_of_replicas: 0`** at store
registration (as in the Option A example above). The driver default is `1`
(it assumes a ≥2-node cluster).

## 4. Exercise the flow

1. Bind a knowledge base to the store and ingest a few documents.
2. Confirm the per-dimension index appears:
   `curl -s 'localhost:9200/_cat/indices?v' | grep weknora`
   (e.g. `weknora_<storeprefix>_768` + alias, plus `weknora_<storeprefix>_keywords`).
3. Run a retrieval query against the bound KB and confirm hits come back.
4. Copy the KB to another KB and confirm the docs are reindexed
   (`opensearch.reindex_executed` audit event).
5. Toggle chunk enabled-status / tag and confirm `_update_by_query` applies it.

## 5. Tear down

```bash
docker compose -f docker-compose.dev.yml --profile opensearch down -v
```

## Scope notes

- Large-batch async reindex / delete (task polling) is a follow-up; the sync
  paths handle typical KB sizes (pagination is bounded by `max_result_window`,
  default 10000).
- Native `hybrid` query + search pipeline is out of scope — fusion stays at the
  service layer (RRF).
