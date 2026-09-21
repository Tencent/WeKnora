#!/usr/bin/env bash
# verify_intent_policies_schema.sh — IntentGate T20 [cli] 验收脚本（issue #10）
#
# 验收标准：[cli] `\d intent_policies` 结构与设计文档
# docs/plans/2026-09-21-intent-gate-design.md §6.1 一致。
#
# 前置条件：
#   1. postgres dev 容器运行中：
#        DOCKER_API_VERSION=1.47 docker compose -f docker-compose.dev.yml \
#          -f docker-compose.dev.fix.yml up -d postgres
#   2. 已应用 versioned 迁移 000109（全量模式启动 app 会自动迁移：
#        .env 设 DB_DRIVER=postgres / RETRIEVE_DRIVER=postgres 后启动
#        weknora-server；或手动 psql 执行
#        migrations/versioned/000109_intent_policies.up.sql）
#
# 用法：bash scripts/verify_intent_policies_schema.sh
# 退出码：0 = 结构符合 §6.1；1 = 不符合 / 表不存在。
set -euo pipefail

CONTAINER="${POSTGRES_CONTAINER:-WeKnora-postgres-dev}"
DB="${POSTGRES_DB:-WeKnora}"
USER="${POSTGRES_USER:-postgres}"

psql_desc() {
    docker exec "$CONTAINER" psql -U "$USER" -d "$DB" -c '\d intent_policies'
}

if ! output="$(psql_desc 2>&1)"; then
    echo "FAIL: \\d intent_policies 执行失败（表不存在或容器未运行）："
    echo "$output"
    echo "提示：先启动 postgres 容器并应用 versioned 迁移 000109（见脚本头部注释）。"
    exit 1
fi

echo "$output"
echo "---- 校验列结构（设计 §6.1） ----"

fail=0
# 形如 "column|type" 的期望清单；\d 输出里列行格式为 ` name | type | ...`
expect_column() {
    local name="$1" type_pat="$2"
    if echo "$output" | grep -E "^[[:space:]]*${name}[[:space]]*\|[[:space]]*${type_pat}" >/dev/null; then
        echo "OK   column ${name} (${type_pat})"
    else
        echo "FAIL column ${name} 缺失或类型不符（期望 ${type_pat}）"
        fail=1
    fi
}

expect_column id              "character varying\(36\)"
expect_column tenant_id       "bigint"
expect_column scope_type      "character varying\(16\)"
expect_column scope_ref       "character varying\(512\)"
expect_column arg_path        "character varying\(256\)"
expect_column constraint_text "text"
expect_column rule_expr       "text"
expect_column risk_tier       "character varying\(8\)"
expect_column mode            "character varying\(16\)"
expect_column version         "integer"
expect_column enabled         "boolean"
expect_column created_by      "character varying\(36\)"
expect_column created_at      "timestamp with time zone"
expect_column updated_at      "timestamp with time zone"

if echo "$output" | grep -q "idx_intent_policies_scope"; then
    echo "OK   index idx_intent_policies_scope"
else
    echo "FAIL index idx_intent_policies_scope 缺失"
    fail=1
fi

for chk in scope_type risk_tier mode; do
    if echo "$output" | grep -E "CHECK.*${chk}" >/dev/null; then
        echo "OK   CHECK constraint on ${chk}"
    else
        echo "FAIL ${chk} 缺枚举 CHECK 约束"
        fail=1
    fi
done

if [ "$fail" -ne 0 ]; then
    echo "==== RESULT: FAIL ===="
    exit 1
fi
echo "==== RESULT: PASS — intent_policies 结构与设计 §6.1 一致 ===="
