#!/usr/bin/env bash
# verify_intentgate_approval.sh — IntentGate T41 [api]/[cli] 验收（issue #18）
#
# 验收标准：
#   [e2e-ui] 触发需审批调用 → 审批卡片出现 → 批准 → 工具执行成功（本脚本
#            覆盖卡片事件出现 + API 批准；UI 点击版见
#            tests/e2e/intentgate-approval-ui.spec.cjs）
#   [e2e-ui] 拒绝 → 工具未执行，agent 收到拒绝原因
#   [cli] 审批决策（含 ModifiedArgs）可从审批存储查出
#
# 依赖：mcp-echo-server 已在 :8765 运行（go build -o mcp-echo-server.exe
# ./cmd/mcp-echo-server && ./mcp-echo-server.exe &）；后端 :8080 含 T41+#31。
#
# 流程：
#   1. 注册新租户；注册 MCP 服务（指向 echo server）；
#   2. 建 enforce 租户级策略（rule_expr=require_approval 哨兵，#31）；
#   3. 建 agent（选中该 MCP 服务 + 引导 discover→call 两步）；
#   4. Run A：agent-chat（SSE 落盘）→ 轮询 SSE 提取 pending_id → 断言卡片
#      描述含「意图策略要求人工审批」→ POST 批准（带 ModifiedArgs
#      {"text":"approved-hi"}）→ 断言 agent_steps 含 echo:approved-hi；
#   5. Run B：同触发 → 拒绝 → 断言工具未执行（无 echo 成功记录）且
#      agent_steps 含拒绝原因；
#   6. verdict 表：两次 require_approval 均落库（mode=enforce）。
set -uo pipefail
export MSYS_NO_PATHCONV=1

BASE="${1:-${BASE_URL:-http://localhost:8080}}"
API="$BASE/api/v1"
ECHO_MCP_URL="${T41_ECHO_MCP_URL:-http://localhost:8765/mcp}"
SUFFIX="$(date +%s)$$"
PASSWORD='Passw0rd!t41'
fail=0
ok()  { echo "OK   $*"; }
bad() { echo "FAIL $*"; fail=1; }

PY=python
command -v python >/dev/null || PY=python3
DB_PATH="${T41_DB_PATH:-$(grep -E '^DB_PATH=' .env 2>/dev/null | cut -d= -f2)}"
DB_PATH="${DB_PATH:-./data/weknora.db}"

db_scalar() {
    SQL="$1" DB_PATH="$DB_PATH" "$PY" -c "
import os, sqlite3, sys
sys.stdout.reconfigure(encoding='utf-8')
conn = sqlite3.connect('file:%s?mode=ro' % os.environ['DB_PATH'], uri=True)
try:
    row = conn.execute(os.environ['SQL']).fetchone()
    def cell(c):
        if c is None: return ''
        if isinstance(c, bytes): return c.decode('utf-8', 'replace')
        return str(c)
    print('' if row is None else '|'.join(cell(c) for c in row))
finally:
    conn.close()"
}

# wait_steps <session_id>：assistant 消息的 agent_steps 落库晚于 SSE 结束
# （QA 完成后的 defer 写回），轮询最长 60s。
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

json_get() {
    local field="$1"
    if command -v jq >/dev/null; then jq -r "$field"
    else FIELD="$field" "$PY" -c "import sys,json,os; d=json.load(sys.stdin); v=d
for k in os.environ['FIELD'].strip().split('.'):
    if k and k != '|': v = v.get(k) if isinstance(v, dict) else None
if v is None: v=''
print(v if not isinstance(v,(dict,list)) else json.dumps(v,ensure_ascii=False))"
    fi
}

# ---- 1. 注册 + 登录 ---------------------------------------------------------
curl -sS -X POST "$API/auth/register" -H 'Content-Type: application/json' \
    -d "{\"username\":\"t41-$SUFFIX\",\"email\":\"t41-$SUFFIX@e2e.local\",\"password\":\"$PASSWORD\"}" >/dev/null
resp="$(curl -sS -X POST "$API/auth/login" -H 'Content-Type: application/json' \
    -d "{\"email\":\"t41-$SUFFIX@e2e.local\",\"password\":\"$PASSWORD\"}")"
TOKEN="$(echo "$resp" | json_get '.token')"
[ -n "$TOKEN" ] || { echo "FAIL 注册/登录：$resp"; exit 1; }
AUTH=(-H "Authorization: Bearer $TOKEN")
JSON=(-H 'Content-Type: application/json')
ok "注册 + 登录"

# ---- 2. 注册 MCP 服务 --------------------------------------------------------
resp="$(curl -sS -X POST "$API/mcp-services" "${AUTH[@]}" "${JSON[@]}" \
    -d "{\"name\":\"echo-$SUFFIX\",\"description\":\"T41 echo\",\"transport_type\":\"http-streamable\",\"url\":\"$ECHO_MCP_URL\"}")"
SVC_ID="$(echo "$resp" | json_get '.data.id')"
[ -n "$SVC_ID" ] || { echo "FAIL MCP 服务注册：$resp"; exit 1; }
ok "MCP 服务注册 id=$SVC_ID"

# ---- 3. enforce 哨兵策略 -----------------------------------------------------
resp="$(curl -sS -X POST "$API/intent-policies" "${AUTH[@]}" "${JSON[@]}" \
    -d '{"scope_type":"tenant","scope_ref":"","constraint_text":"T41 验收：管辖调用一律人工审批","rule_expr":"require_approval","risk_tier":"low","mode":"enforce"}')"
POLICY_ID="$(echo "$resp" | json_get '.data.id')"
[ -n "$POLICY_ID" ] && [ "$POLICY_ID" != "null" ] || { echo "FAIL 策略创建：$resp"; exit 1; }
ok "哨兵策略创建 id=$POLICY_ID"

# ---- 4. agent ---------------------------------------------------------------
resp="$(curl -sS -X POST "$API/agents" "${AUTH[@]}" "${JSON[@]}" \
    -d "{\"name\":\"t41-$SUFFIX\",\"config\":{\"agent_mode\":\"smart-reasoning\",\"model_id\":\"builtin-kimi-coding\",\"allowed_tools\":[\"discover_mcp_tools\",\"call_mcp_tool\"],\"mcp_selection_mode\":\"selected\",\"mcp_services\":[\"$SVC_ID\"],\"system_prompt\":\"你必须依次完成两步：第一步调用 discover_mcp_tools（参数 {\\\"mode\\\":\\\"list_tools\\\",\\\"server_id\\\":\\\"$SVC_ID\\\"}），从返回结果中取 echo 工具的 tool_ref；第二步用该 tool_ref 调用 call_mcp_tool（arguments 为 {\\\"text\\\":\\\"hello\\\"}）。禁止不调用工具直接作答。\"}}")"
AGENT_ID="$(echo "$resp" | json_get '.data.id')"
[ -n "$AGENT_ID" ] && [ "$AGENT_ID" != "null" ] || { echo "FAIL agent 创建：$resp"; exit 1; }
ok "agent 就绪 id=$AGENT_ID"

# ---- 触发一轮：SSE 落盘 + 提取 pending_id + 决策 ----------------------------
ROUND_OK=1
run_round() {
    ROUND_OK=1
    local label="$1" decision="$2" modified="$3"
    local resp sess sse_file resolved_file pid body n_resolved
    resp="$(curl -sS -X POST "$API/sessions" "${AUTH[@]}" "${JSON[@]}"         -d "{\"title\":\"t41-$label\",\"description\":\"T41 $label\"}")"
    sess="$(echo "$resp" | json_get '.data.id')"
    if [ -z "$sess" ]; then
        echo "FAIL $label 会话创建失败"
        ROUND_OK=0
        echo ""
        return 0
    fi
    sse_file="/tmp/t41-$label-$SUFFIX.sse"
    resolved_file="/tmp/t41-$label-resolved-$SUFFIX"
    rm -f "$sse_file" "$resolved_file"
    : > "$resolved_file"
    ( curl -sS -N --max-time 240 -X POST "$API/agent-chat/$sess" "${AUTH[@]}" "${JSON[@]}"         -d "{\"query\":\"按系统指令完成工具调用\",\"agent_id\":\"$AGENT_ID\",\"disable_title\":true}"         > "$sse_file" 2>/dev/null || true ) &
    local curl_pid=$!
    # 解析循环：MCP 服务同时暴露 call_mcp_tool 调度器和按工具直注册名，
    # 模型可能对两者各发一次审批——每张卡片都按本 round 的决策处理，直到
    # SSE 结束（模型不再重试）。
    while kill -0 $curl_pid 2>/dev/null; do
        for pid in $(grep -o '"pending_id":"[^"]*"' "$sse_file" 2>/dev/null | cut -d'"' -f4 | sort -u); do
            if grep -qx "$pid" "$resolved_file" 2>/dev/null; then continue; fi
            echo "$pid" >> "$resolved_file"
            echo "OK   $label 审批卡片出现 pending_id=$pid"
            if [ "$label" = "A" ] && ! grep -q '意图策略要求人工审批' "$sse_file" 2>/dev/null; then
                echo "FAIL $label 卡片描述不含策略理由"
                fail=1
            fi
            if [ -n "$modified" ]; then
                body="{\"decision\":\"$decision\",\"modified_args\":$modified}"
            else
                body="{\"decision\":\"$decision\",\"reason\":\"t41 reject\"}"
            fi
            curl -sS -X POST "$API/agent/tool-approvals/$pid" "${AUTH[@]}" "${JSON[@]}" -d "$body" >/dev/null 2>&1
            echo "OK   $label 决策已提交 decision=$decision"
        done
        sleep 2
    done
    # QA 侧每次被拒后模型可能重试并发新卡：curl（SSE）结束后 agent 还在
    # 服务端继续跑，解析循环必须活到 assistant steps 落库（上限 8 分钟）。
    STEPS_LAST=""
    for _ in $(seq 1 160); do
        for pid in $(grep -o '"pending_id":"[^"]*"' "$sse_file" 2>/dev/null | cut -d'"' -f4 | sort -u); do
            if grep -qx "$pid" "$resolved_file" 2>/dev/null; then continue; fi
            echo "$pid" >> "$resolved_file"
            echo "OK   $label 审批卡片出现 pending_id=$pid"
            if [ "$label" = "A" ] && ! grep -q '意图策略要求人工审批' "$sse_file" 2>/dev/null; then
                echo "FAIL $label 卡片描述不含策略理由"
                fail=1
            fi
            if [ -n "$modified" ]; then
                body="{\"decision\":\"$decision\",\"modified_args\":$modified}"
            else
                body="{\"decision\":\"$decision\",\"reason\":\"t41 reject\"}"
            fi
            curl -sS -X POST "$API/agent/tool-approvals/$pid" "${AUTH[@]}" "${JSON[@]}" -d "$body" >/dev/null 2>&1
            echo "OK   $label 决策已提交 decision=$decision"
        done
        STEPS_LAST="$(db_scalar "SELECT replace(replace(COALESCE(agent_steps,''), char(10), ' '), char(13), ' ') FROM messages WHERE session_id = '$sess' AND role = 'assistant' AND agent_steps IS NOT NULL AND agent_steps != '' ORDER BY created_at DESC LIMIT 1" 2>/dev/null || true)"
        if [ -n "$STEPS_LAST" ]; then
            printf '%s' "$STEPS_LAST" > "/tmp/t41-$label-steps-$SUFFIX"
            break
        fi
        sleep 3
    done
    n_resolved="$(wc -l < "$resolved_file" | tr -d ' ')"
    if [ "$n_resolved" -eq 0 ]; then
        echo "FAIL $label 全程未出现审批卡片"
        ROUND_OK=0
    fi
    echo "$sess"
}

# run_round_retry <label> <decision> <modified> <expect_grep>：
# MCP 首连/模型行为有 flake，整轮重试（新会话，服务/策略复用），直到
# steps 命中预期模式。
run_round_retry() {
    local label="$1" decision="$2" modified="$3" pattern="$4"
    local i sess steps
    for i in 1 2 3; do
        sess="$(run_round "$label" "$decision" "$modified" | tail -1)"
        steps="$(cat "/tmp/t41-$label-steps-$SUFFIX" 2>/dev/null || true)"
        if [ "$ROUND_OK" = "1" ] && printf '%s' "$steps" | grep -q "$pattern"; then
            echo "$steps" > "/tmp/t41-$label-final-$SUFFIX"
            return 0
        fi
        echo "    $label 第 $i 次未达预期（MCP 连接/模型 flake），重试…"
        sleep 3
    done
    echo "$steps" > "/tmp/t41-$label-final-$SUFFIX"
    return 1
}

SESS_A="$(run_round A approve '{"text":"approved-hi"}' | tail -1)"
run_round_retry A approve '{"text":"approved-hi"}' 'echo:approved-hi' || fail=1

# Run A 断言：ModifiedArgs 生效 → echo:approved-hi 进 agent_steps。
steps_a="$(cat "/tmp/t41-A-final-$SUFFIX" 2>/dev/null || true)"
if printf '%s' "$steps_a" | grep -q 'echo:approved-hi'; then
    ok "Run A 批准后工具执行且 ModifiedArgs 生效（agent_steps 含 echo:approved-hi）"
else
    bad "Run A agent_steps 不含 echo:approved-hi（长度 ${#steps_a}）"
fi

SESS_B="$(run_round B reject '' | tail -1)"
run_round_retry B reject '' 'reject' || fail=1

steps_b="$(cat "/tmp/t41-B-final-$SUFFIX" 2>/dev/null || true)"
if printf '%s' "$steps_b" | grep -q 'echo:hello'; then
    bad "Run B 拒绝后工具竟被执行（agent_steps 含 echo:hello）"
elif printf '%s' "$steps_b" | grep -qiE 'reject|拒绝'; then
    ok "Run B 拒绝后工具未执行且 agent 收到拒绝原因"
else
    bad "Run B agent_steps 未见拒绝痕迹（长度 ${#steps_b}）"
fi

# verdict 表：两次 require_approval 均落库。
vc="$(db_scalar "SELECT count(*) FROM intent_verdicts WHERE policy_id='$POLICY_ID' AND verdict='require_approval' AND mode_at_decision='enforce'")"
if [ "$vc" -ge 2 ] 2>/dev/null; then
    ok "verdict 表 require_approval 落库 $vc 条（mode=enforce）"
else
    bad "verdict 表 require_approval 落库 $vc 条，want >=2"
fi

curl -sS -X POST "$API/intent-policies/$POLICY_ID/disable" "${AUTH[@]}" >/dev/null 2>&1 || true

if [ "$fail" -eq 0 ]; then
    echo "PASS: T41 [api]/[cli] 验收通过——哨兵触发审批卡片，批准执行（ModifiedArgs 生效），拒绝拦截"
else
    echo "T41 验收存在失败项"
fi
exit "$fail"
