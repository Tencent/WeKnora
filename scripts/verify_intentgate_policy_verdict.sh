#!/usr/bin/env bash
# verify_intentgate_policy_verdict.sh — IntentGate T23 [api] 验收脚本（issue #13）
#
# 验收标准（issue #13）：
#   [api] 创建策略后，下一次工具调用的 verdict 记录 policy_id 与 policy_version
#
# 流程：
#   1. 注册唯一用户（新租户）并登录；
#   2. POST /api/v1/intent-policies 创建租户级策略（rule_expr "1 > 2"：
#      确定性表达式恒 false → 任何工具调用都产出该策略的 deny verdict，
#      observe 模式只记录不拦截，对新租户零行为影响）；
#   3. 创建 smart-reasoning agent + session，POST /api/v1/agent-chat/:session_id
#      触发一次工具调用（问题默认引导模型使用工具）；
#   4. 轮询 intent_verdicts 表，断言出现 policy_id=新策略 的记录，
#      且 policy_version / mode_at_decision 正确。
#
# 前置条件：
#   1. weknora-server 已启动；agent-chat 需要一个可用的 chat 模型——脚本
#      默认给创建的 agent 显式挂内置模型 builtin-kimi-coding（可用
#      T23_MODEL_ID 覆盖为其他模型 id）。至少一个可用工具（知识库检索 /
#      MCP / shell 任一）由 smart-reasoning 默认工具集提供。可通过环境
#      变量跳过自建触发：
#        TOKEN=<jwt> T23_SESSION_ID=<已有 agent 会话> \
#        T23_AGENT_ID=<agent id> bash scripts/verify_intentgate_policy_verdict.sh
#   2. DB 访问：lite 模式（sqlite）用 python3 读 DB_PATH（默认 ./data/weknora.db，
#      可被 T23_DB_PATH 覆盖）；postgres 模式用 docker exec
#      （T23_PG_CONTAINER，默认 WeKnora-postgres-dev）。DB_DRIVER 从 .env 读取。
#
# 用法：bash scripts/verify_intentgate_policy_verdict.sh [BASE_URL]
# 退出码：0 = 全部断言通过；1 = 任一断言失败。
set -euo pipefail
export MSYS_NO_PATHCONV=1

BASE="${1:-${BASE_URL:-http://localhost:8080}}"
API="$BASE/api/v1"
fail=0

ok()   { echo "OK   $*"; }
bad()  { echo "FAIL $*"; fail=1; }

# jq 优先，退回 node / python3 取字段（与 verify_intent_policy_api.sh 同款）。
json_get() {
    local field="$1"
    if command -v jq >/dev/null; then
        jq -r "$field"
    elif command -v node >/dev/null; then
        FIELD="$field" node -e "let d='';process.stdin.on('data',c=>d+=c).on('end',()=>{const o=JSON.parse(d);const f=process.env.FIELD;let v;if(f.includes('|')){const[base,fn]=f.split('|').map(s=>s.trim());v=base.split('.').filter(Boolean).reduce((a,k)=>a?.[k],o);if(fn==='length')v=v.length}else{v=f.split('.').filter(Boolean).reduce((a,k)=>a?.[k],o)}console.log(typeof v==='object'?JSON.stringify(v):v)})"
    else
        FIELD="$field" python3 -c "import sys,json,os; d=json.load(sys.stdin); v=d
for k in os.environ['FIELD'].strip().split('.'):
    if k and k != '|': v = v.get(k) if isinstance(v, dict) else None
print(v if not isinstance(v,(dict,list)) else json.dumps(v))"
    fi
}

require_tool() {
    command -v "$1" >/dev/null || { echo "缺少依赖：$1"; exit 1; }
}
require_tool curl
command -v jq >/dev/null || command -v node >/dev/null || command -v python3 >/dev/null \
    || { echo "缺少 JSON 解析工具（jq/node/python3 任一）"; exit 1; }

# ---- DB 查询封装 -----------------------------------------------------------
DB_DRIVER="${DB_DRIVER:-$(grep -E '^DB_DRIVER=' .env 2>/dev/null | cut -d= -f2 || true)}"
DB_DRIVER="${DB_DRIVER:-sqlite}"
DB_PATH="${T23_DB_PATH:-$(grep -E '^DB_PATH=' .env 2>/dev/null | cut -d= -f2 || true)}"
DB_PATH="${DB_PATH:-./data/weknora.db}"
PG_CONTAINER="${T23_PG_CONTAINER:-WeKnora-postgres-dev}"
PG_DB="${T23_PG_DB:-WeKnora}"

# db_query <sql>：单行单列结果原样输出。
db_query() {
    local sql="$1"
    if [ "$DB_DRIVER" = "postgres" ]; then
        DOCKER_API_VERSION=1.47 docker exec "$PG_CONTAINER" \
            psql -U postgres -d "$PG_DB" -tAc "$sql"
    else
        # Windows 上 python3 常指向应用商店占位 stub（执行静默失败），
        # 优先用真解释器 python，退回 python3。
        local py=python
        command -v python >/dev/null || py=python3
        require_tool "$py"
        SQL="$sql" DB_PATH="$DB_PATH" "$py" -c "
import os, sqlite3
conn = sqlite3.connect('file:%s?mode=ro' % os.environ['DB_PATH'], uri=True)
try:
    row = conn.execute(os.environ['SQL']).fetchone()
    print('' if row is None else '|'.join('' if c is None else str(c) for c in row))
finally:
    conn.close()"
    fi
}

# ---- 1. 注册唯一用户（username 必须唯一，避免与其他验收脚本撞名） ----------
SUFFIX="$(date +%s)$$"
# agent-chat 必须显式挂 chat 模型：新建的租户没有任何默认模型映射，
# 不显式给 model_id 会在 agent-chat 阶段报 "chat model is not configured"。
# 默认用平台内置模型（对所有租户可见），可用 T23_MODEL_ID 覆盖。
MODEL_ID="${T23_MODEL_ID:-builtin-kimi-coding}"
if [ -z "${TOKEN:-}" ]; then
    EMAIL="intentgate-t23-$SUFFIX@example.com"
    UNAME="t23-$SUFFIX"
    PASSWORD="T23-verify-pass-1"
    curl -sS -X POST "$API/auth/register" -H 'Content-Type: application/json' \
        -d "{\"username\":\"$UNAME\",\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\"}" >/dev/null
    resp="$(curl -sS -X POST "$API/auth/login" -H 'Content-Type: application/json' \
        -d "{\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\"}")"
    TOKEN="$(echo "$resp" | json_get '.token')"
    [ -n "$TOKEN" ] && [ "$TOKEN" != "null" ] || { echo "FAIL 注册/登录失败：$resp"; exit 1; }
    ok "租户注册/登录成功（username=$UNAME）"
else
    ok "使用 TOKEN 环境变量"
fi
AUTH=(-H "Authorization: Bearer $TOKEN")
JSON=(-H 'Content-Type: application/json')

# ---- 2. 创建租户级策略（rule_expr 恒 false → 任何工具调用都产出该策略的判定）
resp="$(curl -sS -w '\n%{http_code}' -X POST "$API/intent-policies" "${AUTH[@]}" "${JSON[@]}" \
    -d '{"scope_type":"tenant","constraint_text":"T23 验收：任何工具调用均视为违反约束（恒 false 表达式）","rule_expr":"1 > 2"}')"
code="$(echo "$resp" | tail -1)"; body="$(echo "$resp" | sed '$d')"
POLICY_ID="$(echo "$body" | json_get '.data.id')"
POLICY_VERSION="$(echo "$body" | json_get '.data.version')"
MODE="$(echo "$body" | json_get '.data.mode')"
if [ "$code" = "201" ] && [ -n "$POLICY_ID" ] && [ "$POLICY_ID" != "null" ]; then
    ok "策略创建成功 id=$POLICY_ID version=$POLICY_VERSION mode=$MODE"
else
    echo "FAIL 策略创建失败（$code）：$body"; exit 1
fi
[ "$MODE" = "observe" ] && ok "mode 缺省 observe（只记录不拦截）" \
    || bad "mode 期望 observe 实际 $MODE"

# ---- 3. 触发一次工具调用 ----------------------------------------------------
# 默认问题引导模型调用会话检索工具（allowed_tools 只留 search_conversations：
# 知识库检索工具链强制要求 rerank 模型，新建租户没有会硬失败；门禁钩子
# 挂在所有工具调用的执行点上，任何工具触发都能产出 verdict）。模型可能
# 无视指令直接作答，故同时给 agent 挂了强制先调工具的 system_prompt。
QUESTION="${T23_QUESTION:-请先调用 search_conversations 工具（query 参数填"1+1"），引用工具返回的结果，然后告诉我 1+1 等于几。}"
if [ -z "${T23_SESSION_ID:-}" ]; then
    resp="$(curl -sS -X POST "$API/agents" "${AUTH[@]}" "${JSON[@]}" \
        -d "{\"name\":\"t23-verify-$SUFFIX\",\"config\":{\"agent_mode\":\"smart-reasoning\",\"model_id\":\"$MODEL_ID\",\"allowed_tools\":[\"search_conversations\"],\"system_prompt\":\"你必须先调用 search_conversations 工具，再基于工具返回作答；禁止不调用工具直接回答。\"}}")"
    AGENT_ID="$(echo "$resp" | json_get '.data.id')"
    [ -n "$AGENT_ID" ] && [ "$AGENT_ID" != "null" ] \
        || { echo "FAIL 创建 agent 失败：$resp"; exit 1; }
    resp="$(curl -sS -X POST "$API/sessions" "${AUTH[@]}" "${JSON[@]}" \
        -d '{"title":"t23-verify","description":"T23 acceptance trigger"}')"
    T23_SESSION_ID="$(echo "$resp" | json_get '.data.id')"
    [ -n "$T23_SESSION_ID" ] && [ "$T23_SESSION_ID" != "null" ] \
        || { echo "FAIL 创建 session 失败：$resp"; exit 1; }
    ok "agent=$AGENT_ID session=$T23_SESSION_ID 就绪"
else
    AGENT_ID="${T23_AGENT_ID:-}"
    ok "使用既有 session=$T23_SESSION_ID"
fi

# json_quote 把任意字符串编码为 JSON 字符串字面量（含转义）。
json_quote() {
    if command -v jq >/dev/null; then
        jq -n --arg s "$1" '$s'
    elif command -v node >/dev/null; then
        S="$1" node -e "console.log(JSON.stringify(process.env.S))"
    else
        S="$1" python3 -c "import json,os;print(json.dumps(os.environ['S']))"
    fi
}

# agent-chat 是 SSE 流；触达工具调用后即完成使命，curl 限时读取不阻塞。
chat_body="{\"query\":$(json_quote "$QUESTION"),\"agent_id\":\"$AGENT_ID\",\"disable_title\":true}"
curl -sS --max-time 120 -X POST "$API/agent-chat/$T23_SESSION_ID" "${AUTH[@]}" "${JSON[@]}" \
    -d "$chat_body" >/dev/null 2>&1 || true
ok "agent-chat 已触发（问题：$QUESTION）"

# ---- 4. 轮询 intent_verdicts：断言 policy_id / policy_version --------------
found=""
for _ in $(seq 1 24); do
    row="$(db_query "SELECT policy_id, policy_version, verdict, layer, mode_at_decision FROM intent_verdicts WHERE policy_id = '$POLICY_ID' ORDER BY created_at DESC LIMIT 1" 2>/dev/null || true)"
    if [ -n "$row" ]; then found="$row"; break; fi
    sleep 5
done
if [ -z "$found" ]; then
    bad "120s 内未出现 policy_id=$POLICY_ID 的 verdict 记录。"
    echo "     排查方向：模型是否调用了工具（server 日志搜 '[Agent][IntentGate] verdict'）；"
    echo "     该租户是否有可用 chat 模型与至少一个工具；agent-chat 返回的 SSE 是否报错。"
    exit 1
fi
ok "verdict 记录出现：$found"

v_policy_id="$(echo "$found" | cut -d'|' -f1)"
v_version="$(echo "$found" | cut -d'|' -f2)"
v_verdict="$(echo "$found" | cut -d'|' -f3)"
v_layer="$(echo "$found" | cut -d'|' -f4)"
v_mode="$(echo "$found" | cut -d'|' -f5)"

[ "$v_policy_id" = "$POLICY_ID" ] && ok "policy_id=$v_policy_id 与创建的策略一致" \
    || bad "policy_id=$v_policy_id 期望 $POLICY_ID"
[ "$v_version" = "$POLICY_VERSION" ] && ok "policy_version=$v_version 与创建版本一致" \
    || bad "policy_version=$v_version 期望 $POLICY_VERSION"
[ "$v_mode" = "observe" ] && ok "mode_at_decision=observe（只记录不拦截）" \
    || bad "mode_at_decision=$v_mode 期望 observe"
case "$v_verdict" in
    deny|allow|uncertain) ok "verdict=$v_verdict layer=$v_layer（rule_expr 恒 false 时预期 deny/rule）" ;;
    *) bad "verdict 取值异常：$v_verdict" ;;
esac

# ---- 收尾 -------------------------------------------------------------------
curl -sS -X POST "$API/intent-policies/$POLICY_ID/disable" "${AUTH[@]}" >/dev/null || true
ok "验收策略已停用（避免影响该租户后续会话）"

if [ "$fail" = "0" ]; then
    echo "PASS: T23 [api] 验收通过——创建策略后，下一次工具调用的 verdict 记录了 policy_id 与 policy_version"
else
    echo "FAIL: 存在未通过断言"
fi
exit "$fail"
