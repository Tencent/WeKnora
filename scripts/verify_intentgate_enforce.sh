#!/usr/bin/env bash
# verify_intentgate_enforce.sh — IntentGate T40 [api] 验收脚本（issue #17）
#
# 验收标准（issue #17）：
#   [api] 租户 A 开 enforce、租户 B observe：同一策略同一调用，A 拦截 B 放行
#
# 流程：
#   1. 注册两个全新租户用户 A/B 并登录；
#   2. 双方在各自租户创建同内容租户级策略（constraint 相同、rule_expr
#      "1 > 2" 恒 false → 必 deny），A 的 mode=enforce、B 的 mode=observe；
#   3. 双方各自创建同配置 smart-reasoning agent（内置 Kimi 模型 +
#      search_conversations 工具 + 强制先调工具的 system_prompt），
#      POST /api/v1/agent-chat/:session_id 用同一问题触发工具调用；
#   4. 断言（sqlite 直查）：
#      - intent_verdicts：A 策略 verdict=deny/mode=enforce，B 策略
#        verdict=deny/mode=observe（该记的都记下）；
#      - A 会话最新 assistant 消息的 agent_steps 含 DeniedError 文本
#        "被意图策略拒绝"（工具未执行、agent 可见拒绝原因）；
#      - B 会话最新 assistant 消息的 agent_steps 含 search_conversations
#        工具成功结果且不含拒绝文本（不该拦的放行）。
#
# 前置条件（与 verify_intentgate_policy_verdict.sh 相同）：
#   - 后端 lite 模式 :8080；内置模型 builtin-kimi-coding（KIMI_CODING_API_KEY）；
#   - .env 配 SSRF_WHITELIST 含模型 BaseURL 主机（出站代理 fake-ip 场景）；
#   - 模型 id 可用 T40_MODEL_ID 覆盖。
#
# 用法：bash scripts/verify_intentgate_enforce.sh [BASE_URL]
# 退出码：0 = 全部断言通过；1 = 任一断言失败。
set -euo pipefail
export MSYS_NO_PATHCONV=1

BASE="${1:-${BASE_URL:-http://localhost:8080}}"
API="$BASE/api/v1"
fail=0

ok()   { echo "OK   $*"; }
bad()  { echo "FAIL $*"; fail=1; }

# ---- 工具函数（与 T23 脚本同款）------------------------------------------
json_get() {
    local field="$1"
    if command -v jq >/dev/null; then
        jq -r "$field"
    elif command -v node >/dev/null; then
        FIELD="$field" node -e "let d='';process.stdin.on('data',c=>d+=c).on('end',()=>{const o=JSON.parse(d);const f=process.env.FIELD;let v;if(f.includes('|')){const[base,fn]=f.split('|').map(s=>s.trim());v=base.split('.').filter(Boolean).reduce((a,k)=>a?.[k],o);if(fn==='length')v=v.length}else{v=f.split('.').filter(Boolean).reduce((a,k)=>a?.[k],o)}console.log(typeof v==='object'?JSON.stringify(v):v)})"
    else
        FIELD="$field" python -c "import sys,json,os; d=json.load(sys.stdin); v=d
for k in os.environ['FIELD'].strip().split('.'):
    if k and k != '|': v = v.get(k) if isinstance(v, dict) else None
print(v if not isinstance(v,(dict,list)) else json.dumps(v))"
    fi
}

# json_quote 把任意字符串编码为 JSON 字符串字面量（含转义）。
json_quote() {
    if command -v jq >/dev/null; then
        jq -n --arg s "$1" '$s'
    elif command -v node >/dev/null; then
        S="$1" node -e "console.log(JSON.stringify(process.env.S))"
    else
        S="$1" "$PY" -c "import json,os;print(json.dumps(os.environ['S']))"
    fi
}

require_tool() {
    command -v "$1" >/dev/null || { echo "缺少依赖：$1"; exit 1; }
}
require_tool curl
command -v jq >/dev/null || command -v node >/dev/null || command -v python >/dev/null \
    || { echo "缺少 JSON 解析工具（jq/node/python 任一）"; exit 1; }

# Windows 上 python3 常指向应用商店占位 stub（执行静默失败），优先 python。
PY=python
command -v python >/dev/null || PY=python3
require_tool "$PY"

DB_DRIVER="${DB_DRIVER:-sqlite}"
DB_PATH="${T40_DB_PATH:-$(grep -E '^DB_PATH=' .env 2>/dev/null | cut -d= -f2 || true)}"
DB_PATH="${DB_PATH:-./data/weknora.db}"

# db_query <sql>：单行 pipe 拼接输出；空结果输出空串。
db_query() {
    local sql="$1"
    if [ "$DB_DRIVER" = "postgres" ]; then
        local PG_CONTAINER="${T40_PG_CONTAINER:-WeKnora-postgres-dev}"
        local PG_DB="${T40_PG_DB:-WeKnora}"
        DOCKER_API_VERSION=1.47 docker exec "$PG_CONTAINER" \
            psql -U postgres -d "$PG_DB" -tAc "$sql"
    else
        SQL="$sql" DB_PATH="$DB_PATH" "$PY" -c "
import os, sqlite3, sys
sys.stdout.reconfigure(encoding='utf-8')
conn = sqlite3.connect('file:%s?mode=ro' % os.environ['DB_PATH'], uri=True)
try:
    row = conn.execute(os.environ['SQL']).fetchone()
    def cell(c):
        if c is None:
            return ''
        if isinstance(c, bytes):
            return c.decode('utf-8', 'replace')
        return str(c)
    print('' if row is None else '|'.join(cell(c) for c in row))
finally:
    conn.close()"
    fi
}

# db_scalar <sql>：取首行首列（agent_steps 等大字段用）。
db_scalar() { db_query "$1"; }

# wait_steps <session_id>：轮询等待 assistant 消息的 agent_steps 落库
# （QA 完成后由调用方 defer 写回，SSE 结束不等于已持久化），最长 60s。
wait_steps() {
    local sess="$1" steps=""
    for _ in $(seq 1 20); do
        steps="$(db_scalar "SELECT agent_steps FROM messages WHERE session_id = '$sess' AND role = 'assistant' AND agent_steps IS NOT NULL AND agent_steps != '' ORDER BY created_at DESC LIMIT 1" 2>/dev/null || true)"
        if [ -n "$steps" ]; then
            printf '%s' "$steps"
            return 0
        fi
        sleep 3
    done
    return 0
}

# ---- 1. 注册并登录两个租户 ------------------------------------------------
SUFFIX="$(date +%s)$$"
MODEL_ID="${T40_MODEL_ID:-builtin-kimi-coding}"
PASSWORD='Passw0rd!t40'

register_and_login() {
    local uname="$1" email="$2"
    curl -sS -X POST "$API/auth/register" -H 'Content-Type: application/json' \
        -d "{\"username\":\"$uname\",\"email\":\"$email\",\"password\":\"$PASSWORD\"}" >/dev/null
    curl -sS -X POST "$API/auth/login" -H 'Content-Type: application/json' \
        -d "{\"email\":\"$email\",\"password\":\"$PASSWORD\"}"
}

resp="$(register_and_login "t40-a-$SUFFIX" "t40-a-$SUFFIX@e2e.local")"
TOKEN_A="$(echo "$resp" | json_get '.token')"
[ -n "$TOKEN_A" ] && [ "$TOKEN_A" != "null" ] || { echo "FAIL 租户 A 注册/登录失败：$resp"; exit 1; }
resp="$(register_and_login "t40-b-$SUFFIX" "t40-b-$SUFFIX@e2e.local")"
TOKEN_B="$(echo "$resp" | json_get '.token')"
[ -n "$TOKEN_B" ] && [ "$TOKEN_B" != "null" ] || { echo "FAIL 租户 B 注册/登录失败：$resp"; exit 1; }
ok "租户 A/B 注册/登录成功"

AUTH_A=(-H "Authorization: Bearer $TOKEN_A")
AUTH_B=(-H "Authorization: Bearer $TOKEN_B")
JSON=(-H 'Content-Type: application/json')

# ---- 2. 双方创建同内容策略（A enforce / B observe）------------------------
CONSTRAINT='T40 enforce 验收：任何工具调用都视为违反约束'
create_policy() {
    local mode="$1"; shift
    local auth=("$@")
    curl -sS -X POST "$API/intent-policies" "${auth[@]}" "${JSON[@]}" \
        -d "{\"scope_type\":\"tenant\",\"scope_ref\":\"\",\"constraint_text\":\"$CONSTRAINT\",\"rule_expr\":\"1 > 2\",\"risk_tier\":\"low\",\"mode\":\"$mode\"}"
}

resp="$(create_policy enforce "${AUTH_A[@]}")"
POLICY_A="$(echo "$resp" | json_get '.data.id')"
[ -n "$POLICY_A" ] && [ "$POLICY_A" != "null" ] || { echo "FAIL 租户 A 策略创建失败：$resp"; exit 1; }
resp="$(create_policy observe "${AUTH_B[@]}")"
POLICY_B="$(echo "$resp" | json_get '.data.id')"
[ -n "$POLICY_B" ] && [ "$POLICY_B" != "null" ] || { echo "FAIL 租户 B 策略创建失败：$resp"; exit 1; }
ok "策略创建成功 A(enforce)=$POLICY_A B(observe)=$POLICY_B"

# ---- 3. 创建双方 agent；触发 + 断言带重试（LLM 可能无视强制指令直接作答）--
FORCE_TOOL_PROMPT='你必须先调用 search_conversations 工具，再基于工具返回作答；禁止不调用工具直接回答。'
QUESTION='请先调用 search_conversations 工具（query 参数填"1+1"），引用工具返回的结果，然后告诉我 1+1 等于几。'

create_agent() {
    local label="$1"; shift
    local auth=("$@")
    local resp agent_id
    resp="$(curl -sS -X POST "$API/agents" "${auth[@]}" "${JSON[@]}"         -d "{\"name\":\"t40-$label-$SUFFIX\",\"config\":{\"agent_mode\":\"smart-reasoning\",\"model_id\":\"$MODEL_ID\",\"allowed_tools\":[\"search_conversations\"],\"system_prompt\":\"$FORCE_TOOL_PROMPT\"}}")"
    agent_id="$(echo "$resp" | json_get '.data.id')"
    [ -n "$agent_id" ] && [ "$agent_id" != "null" ] || { echo "FAIL $label agent 创建失败：$resp"; exit 1; }
    echo "$agent_id"
}

AGENT_A="$(create_agent a "${AUTH_A[@]}")"
AGENT_B="$(create_agent b "${AUTH_B[@]}")"
ok "双方 agent 就绪 A=$AGENT_A B=$AGENT_B"

# trigger_chat <label> <agent_id> <auth...>：建新会话、发题、返回 session_id。
trigger_chat() {
    local label="$1" agent_id="$2"; shift 2
    local auth=("$@")
    local resp sess_id
    resp="$(curl -sS -X POST "$API/sessions" "${auth[@]}" "${JSON[@]}"         -d "{\"title\":\"t40-$label\",\"description\":\"T40 acceptance\"}")"
    sess_id="$(echo "$resp" | json_get '.data.id')"
    [ -n "$sess_id" ] && [ "$sess_id" != "null" ] || { echo "FAIL $label 会话创建失败：$resp"; exit 1; }
    local chat_body
    chat_body="{\"query\":$(json_quote "$QUESTION"),\"agent_id\":\"$agent_id\",\"disable_title\":true}"
    curl -sS --max-time 180 -X POST "$API/agent-chat/$sess_id" "${auth[@]}" "${JSON[@]}"         -d "$chat_body" >/dev/null 2>&1 || true
    echo "$sess_id"
}

# assert_tenant <label> <policy_id> <want_mode> <sess> <expect_block:true|false>
# 返回 0=通过。verdict 行 + agent_steps 双重断言。
assert_tenant() {
    local label="$1" pol="$2" want_mode="$3" sess="$4" expect_block="$5"
    local row steps
    row="$(db_query "SELECT verdict, mode_at_decision, layer FROM intent_verdicts WHERE policy_id = '$pol' ORDER BY created_at DESC LIMIT 1" 2>/dev/null || true)"
    if [ "$row" != "deny|$want_mode|rule" ]; then
        echo "FAIL $label verdict 行 = ${row:-<空>}，want deny|$want_mode|rule"
        return 1
    fi
    echo "OK   $label 策略 verdict=deny mode=$want_mode layer=rule"
    steps="$(wait_steps "$sess")"
    if [ "$expect_block" = "true" ]; then
        if printf '%s' "$steps" | grep -q '被意图策略拒绝'; then
            echo "OK   $label 会话 agent_steps 含拒绝文本（enforce 拦截生效，agent 可见原因）"
            return 0
        fi
        echo "FAIL $label 会话 agent_steps 不含拒绝文本（长度 ${#steps}）"
        return 1
    fi
    if printf '%s' "$steps" | grep -q '被意图策略拒绝'; then
        echo "FAIL $label 会话 agent_steps 竟含拒绝文本（observe 不该拦截）"
        return 1
    fi
    if printf '%s' "$steps" | grep -q 'search_conversations'; then
        echo "OK   $label 会话 search_conversations 工具正常执行（observe 放行）"
        return 0
    fi
    echo "FAIL $label 会话 agent_steps 未见工具调用记录（长度 ${#steps}）"
    return 1
}

# run_with_retry <label> <agent_id> <policy_id> <want_mode> <expect_block> <auth...>
run_with_retry() {
    local label="$1" agent_id="$2" pol="$3" want_mode="$4" expect_block="$5"; shift 5
    local auth=("$@")
    local attempt sess
    for attempt in 1 2 3; do
        sess="$(trigger_chat "$label" "$agent_id" "${auth[@]}")"
        echo "OK   $label 第 $attempt 次 agent-chat 完成 session=$sess"
        if assert_tenant "$label" "$pol" "$want_mode" "$sess" "$expect_block"; then
            return 0
        fi
        echo "    $label 第 $attempt 次未达断言（LLM 可能未调工具），重试…"
        sleep 2
    done
    return 1
}

fail=0
run_with_retry A "$AGENT_A" "$POLICY_A" enforce true  "${AUTH_A[@]}" || fail=1
run_with_retry B "$AGENT_B" "$POLICY_B" observe false "${AUTH_B[@]}" || fail=1

# ---- 5. audit 断言（T43，issue #20）：enforce deny 进 audit_logs；------
#      observe deny 不进（只进 verdict 观测表）。
row_audit="$(db_query "SELECT action, target_type, target_id, outcome, length(actor_user_id) FROM audit_logs WHERE target_id = '$POLICY_A' AND action = 'intent_policy.enforced_deny' ORDER BY id DESC LIMIT 1" 2>/dev/null || true)"
case "$row_audit" in
    "intent_policy.enforced_deny|intent_policy|$POLICY_A|denied|"*[!0]*)
        ok "A 策略 audit 行正确（action/target/outcome=denied/actor 非空）"
        ;;
    *)
        bad "A 策略 audit 行 = ${row_audit:-<空>}，want intent_policy.enforced_deny|intent_policy|<id>|denied|<actorlen>"
        ;;
esac
cnt_b="$(db_query "SELECT count(*) FROM audit_logs WHERE target_id = '$POLICY_B' AND action = 'intent_policy.enforced_deny'" 2>/dev/null || true)"
if [ "$cnt_b" = "0" ]; then
    ok "B 策略（observe）audit 表无记录（只进 verdict 表）"
else
    bad "B 策略 observe deny 竟出现在 audit 表（count=$cnt_b）"
fi

# ---- 收尾：停用双方策略，避免影响后续会话 ---------------------------------
curl -sS -X POST "$API/intent-policies/$POLICY_A/disable" "${AUTH_A[@]}" >/dev/null 2>&1 || true
curl -sS -X POST "$API/intent-policies/$POLICY_B/disable" "${AUTH_B[@]}" >/dev/null 2>&1 || true

if [ "$fail" -eq 0 ]; then
    echo "PASS: T40 [api] 验收通过——同一策略同一调用，enforce 租户拦截、observe 租户放行"
else
    echo "T40 [api] 验收存在失败项"
fi
exit "$fail"
