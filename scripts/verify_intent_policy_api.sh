#!/usr/bin/env bash
# verify_intent_policy_api.sh — IntentGate T21 [api] 验收脚本（issue #11）
#
# 验收标准（issue #11）：
#   1. [api] curl 全流程：POST 创建 → GET 读取 → PUT 更新（version 1→2，
#      v1 仍可查）→ 停用
#   2. [api] 跨租户访问返回 403/404
#   3. [api] mode 缺省为 observe；创建 enforce 策略需显式字段
#
# 前置条件：
#   1. weknora-server 已启动（lite 模式即可，无需 docker 中间件）：
#        export PATH="/c/Program Files/Go/bin:...mingw64/bin:$PATH"
#        export CGO_ENABLED=1
#        export CGO_CFLAGS="-I$(pwd -W)/docker/dev/sqlite3-shim -IC:/Users/Administrator/go/pkg/mod/github.com/mattn/go-sqlite3@v1.14.24"
#        go build -o weknora-server.exe ./cmd/server
#        set -a; source .env; set +a; ./weknora-server.exe &
#   2. 注册开放（默认配置下首个用户注册即建租户）。若实例已关闭注册，
#      请用环境变量直接提供两个不同租户的 JWT：
#        TOKEN_A=... TOKEN_B=... bash scripts/verify_intent_policy_api.sh
#
# 用法：bash scripts/verify_intent_policy_api.sh [BASE_URL]
# 退出码：0 = 全部断言通过；1 = 任一断言失败。
set -euo pipefail

BASE="${1:-${BASE_URL:-http://localhost:8080}}"
API="$BASE/api/v1"
fail=0

ok()   { echo "OK   $*"; }
bad()  { echo "FAIL $*"; fail=1; }

# jq 优先，退回 node / python3 取字段（field 形如 '.data.id' 或
# '.data | length'——后者仅 jq/node 分支支持，python3 不支持管道表达式）。
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
# python3 兜底不支持 '.data | length' 管道表达式，列表长度断言改用 node/jq。
list_len() {
    if command -v jq >/dev/null; then jq -r '.data | length';
    elif command -v node >/dev/null; then node -e "let d='';process.stdin.on('data',c=>d+=c).on('end',()=>console.log(JSON.parse(d).data.length))";
    else python3 -c "import sys,json; print(len(json.load(sys.stdin)['data']))"; fi
}

# ---- 准备两个租户的 JWT --------------------------------------------------
SUFFIX="$(date +%s)$$"
EMAIL_A="intentgate-t21-a-$SUFFIX@example.com"
EMAIL_B="intentgate-t21-b-$SUFFIX@example.com"
PASSWORD="T21-verify-pass-1"

register_and_login() {
    local email="$1"
    local uname="${2:-t21-$SUFFIX}"
    curl -sS -X POST "$API/auth/register" -H 'Content-Type: application/json' \
        -d "{\"username\":\"$uname\",\"email\":\"$email\",\"password\":\"$PASSWORD\"}" >/dev/null
    curl -sS -X POST "$API/auth/login" -H 'Content-Type: application/json' \
        -d "{\"email\":\"$email\",\"password\":\"$PASSWORD\"}"
}

if [ -z "${TOKEN_A:-}" ]; then
    resp="$(register_and_login "$EMAIL_A" "t21-a-$SUFFIX")"
    TOKEN_A="$(echo "$resp" | json_get '.token')"
    [ -n "$TOKEN_A" ] && [ "$TOKEN_A" != "null" ] || { echo "FAIL 租户 A 注册/登录失败：$resp"; exit 1; }
fi
if [ -z "${TOKEN_B:-}" ]; then
    resp="$(register_and_login "$EMAIL_B" "t21-b-$SUFFIX")"
    TOKEN_B="$(echo "$resp" | json_get '.token')"
    [ -n "$TOKEN_B" ] && [ "$TOKEN_B" != "null" ] || { echo "FAIL 租户 B 注册/登录失败：$resp"; exit 1; }
fi
ok "两个租户的 JWT 就绪"

AUTH_A=(-H "Authorization: Bearer $TOKEN_A")
AUTH_B=(-H "Authorization: Bearer $TOKEN_B")
JSON=(-H 'Content-Type: application/json')

# ---- 1. POST 创建（mode 缺省） -------------------------------------------
resp="$(curl -sS -w '\n%{http_code}' -X POST "$API/intent-policies" "${AUTH_A[@]}" "${JSON[@]}" \
    -d '{"scope_type":"tool","scope_ref":"*:wiki_delete_page","constraint_text":"删除页面前必须有用户明确提到删"}')"
code="$(echo "$resp" | tail -1)"; body="$(echo "$resp" | sed '$d')"
[ "$code" = "201" ] && ok "POST 创建返回 201" || bad "POST 创建期望 201 实际 $code：$body"
POLICY_ID="$(echo "$body" | json_get '.data.id')"
MODE="$(echo "$body" | json_get '.data.mode')"
[ "$MODE" = "observe" ] && ok "mode 缺省为 observe" || bad "mode 缺省期望 observe 实际 $MODE"

# 显式 enforce 才可创建 enforce 策略。
resp="$(curl -sS -w '\n%{http_code}' -X POST "$API/intent-policies" "${AUTH_A[@]}" "${JSON[@]}" \
    -d '{"scope_type":"tenant","constraint_text":"高危基线","mode":"enforce","risk_tier":"high"}')"
code="$(echo "$resp" | tail -1)"; body="$(echo "$resp" | sed '$d')"
[ "$code" = "201" ] && [ "$(echo "$body" | json_get '.data.mode')" = "enforce" ] \
    && ok "显式 mode=enforce 创建成功" || bad "显式 enforce 创建失败（$code）：$body"
ENFORCE_ID="$(echo "$body" | json_get '.data.id')"

# 同谱系重复 POST 必须 409（修改走 PUT）。
code="$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$API/intent-policies" "${AUTH_A[@]}" "${JSON[@]}" \
    -d '{"scope_type":"tool","scope_ref":"*:wiki_delete_page","constraint_text":"重复谱系"}')"
[ "$code" = "409" ] && ok "重复谱系 POST 返回 409" || bad "重复谱系 POST 期望 409 实际 $code"

# ---- 2. GET 读取 ----------------------------------------------------------
code="$(curl -sS -o /dev/null -w '%{http_code}' "$API/intent-policies/$POLICY_ID" "${AUTH_A[@]}")"
[ "$code" = "200" ] && ok "GET 按 ID 读取 200" || bad "GET 读取期望 200 实际 $code"
code="$(curl -sS -o /dev/null -w '%{http_code}' "$API/intent-policies" "${AUTH_A[@]}")"
[ "$code" = "200" ] && ok "GET 列表 200" || bad "GET 列表期望 200 实际 $code"

# ---- 3. PUT 更新（version 1→2，v1 仍可查） --------------------------------
resp="$(curl -sS -w '\n%{http_code}' -X PUT "$API/intent-policies/$POLICY_ID" "${AUTH_A[@]}" "${JSON[@]}" \
    -d '{"constraint_text":"删除页面前必须有用户明确提到删，且页面超过 30 天未更新","rule_expr":"value <= 75","arg_path":"$.amount"}')"
code="$(echo "$resp" | tail -1)"; body="$(echo "$resp" | sed '$d')"
V2_VERSION="$(echo "$body" | json_get '.data.version')"
V2_ID="$(echo "$body" | json_get '.data.id')"
[ "$code" = "200" ] && [ "$V2_VERSION" = "2" ] && ok "PUT 更新产生 version=2 新行" \
    || bad "PUT 更新期望 200+version=2 实际 $code/version=$V2_VERSION：$body"
[ "$V2_ID" != "$POLICY_ID" ] && ok "新版本是新 ID（旧版本保留）" || bad "PUT 未产生新行"

v1_version="$(curl -sS "$API/intent-policies/$POLICY_ID" "${AUTH_A[@]}" | json_get '.data.version')"
[ "$v1_version" = "1" ] && ok "v1 仍可查询（version=1）" || bad "v1 查询异常：version=$v1_version"

# ---- 4. 停用 / 启用 --------------------------------------------------------
resp="$(curl -sS -w '\n%{http_code}' -X POST "$API/intent-policies/$V2_ID/disable" "${AUTH_A[@]}")"
code="$(echo "$resp" | tail -1)"; body="$(echo "$resp" | sed '$d')"
[ "$code" = "200" ] && [ "$(echo "$body" | json_get '.data.enabled')" = "false" ] \
    && ok "停用成功（enabled=false）" || bad "停用失败（$code）：$body"
resp="$(curl -sS "$API/intent-policies/$V2_ID/enable" -X POST "${AUTH_A[@]}" | json_get '.data.enabled')"
[ "$resp" = "true" ] && ok "重新启用成功" || bad "重新启用失败：$resp"

# ---- 5. 跨租户访问 → 403/404 ----------------------------------------------
for target in "$POLICY_ID" "$V2_ID" "$ENFORCE_ID"; do
    for method_path in "GET /intent-policies/$target" \
                        "PUT /intent-policies/$target" \
                        "POST /intent-policies/$target/disable"; do
        method="${method_path%% *}"; path="${method_path#* }"
        extra=()
        [ "$method" = "PUT" ] && extra=("${JSON[@]}" -d '{"constraint_text":"x"}')
        code="$(curl -sS -o /dev/null -w '%{http_code}' -X "$method" "$API$path" "${AUTH_B[@]}" "${extra[@]}")"
        case "$code" in
            403|404) ok "跨租户 $method $path → $code" ;;
            *) bad "跨租户 $method $path 期望 403/404 实际 $code" ;;
        esac
    done
done
b_list="$(curl -sS "$API/intent-policies" "${AUTH_B[@]}" | list_len)"
[ "$b_list" = "0" ] && ok "租户 B 列表不含租户 A 的策略" || bad "租户 B 列表泄漏：$b_list 条"

echo
if [ "$fail" = "0" ]; then
    echo "PASS: issue #11 全部 [api] 断言通过"
else
    echo "FAIL: 存在未通过断言（见上）"
fi
exit $fail
