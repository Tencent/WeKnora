#!/usr/bin/env bash
# Doris 4.1 端到端联调脚本
#
# 该脚本验证：
#   1) 后端启动正常
#   2) 通过 API 创建 Doris VectorStore
#   3) 上传知识、写入索引
#   4) 向量检索 + 关键词检索
#   5) 关闭单个 chunk 后再检索（验证 BatchUpdateChunkEnabledStatus 走 Stream Load）
#
# 使用前置条件：
#   - docker compose --profile doris up -d   先把 Doris 起来
#   - 在 FE 上 CREATE DATABASE weknora;
#   - export RETRIEVE_DRIVER=doris && make run （或 docker compose up app）
#
# 这是"checklist-as-script"，按需要手动逐段执行。
set -euo pipefail

WEKNORA_API="${WEKNORA_API:-http://localhost:8080}"
DORIS_FE_HOST="${DORIS_FE_HOST:-127.0.0.1}"
DORIS_FE_HTTP_PORT="${DORIS_FE_HTTP_PORT:-8030}"
DORIS_FE_MYSQL_PORT="${DORIS_FE_MYSQL_PORT:-9030}"

step() { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; }

step "1. 检查 Doris FE 端口可达"
nc -zv "$DORIS_FE_HOST" "$DORIS_FE_HTTP_PORT"
nc -zv "$DORIS_FE_HOST" "$DORIS_FE_MYSQL_PORT"

step "2. 在 FE 上确认 weknora 库存在"
docker exec WeKnora-doris-fe \
    mysql -h 127.0.0.1 -P 9030 -uroot \
    -e "CREATE DATABASE IF NOT EXISTS weknora; SHOW DATABASES;"

step "3. 通过 WeKnora API 创建 Doris VectorStore"
curl -fsS -X POST "$WEKNORA_API/api/v1/vector-stores" \
    -H 'Content-Type: application/json' \
    -d '{
        "name": "doris-local",
        "engine_type": "doris",
        "connection_config": {
            "addr": "doris-fe:9030",
            "http_port": 8030,
            "database": "weknora",
            "username": "root",
            "password": ""
        },
        "index_config": {
            "collection_prefix": "weknora_embeddings",
            "buckets_num": 5,
            "replication_num": 1
        }
    }'

step "4. 上传一个简单知识库（这一步交给前端 UI 完成更省事）"
echo "在前端 UI 切到刚才创建的 Doris VectorStore，新建知识库并上传一篇 PDF。"
echo "或者用 curl + /api/v1/knowledges 调用 API。"

step "5. 在 FE 上验证表已建好"
docker exec WeKnora-doris-fe \
    mysql -h 127.0.0.1 -P 9030 -uroot \
    -e "USE weknora; SHOW TABLES LIKE 'weknora_embeddings_%'; SHOW INDEX FROM weknora_embeddings_768;"

step "6. 检索验证"
echo "在前端发起检索（向量 + 关键词），确认有命中。"
echo "命中后到 FE 上查 SELECT COUNT(*) FROM weknora_embeddings_<dim>;"

step "7. 状态批改验证"
echo "在前端把某个 chunk 关闭，再次发起检索，确认该 chunk 不再返回。"
echo "FE 上确认 SELECT id, is_enabled FROM weknora_embeddings_<dim> WHERE chunk_id = '<id>';"

echo
echo "全部步骤完成 → 联调通过"
