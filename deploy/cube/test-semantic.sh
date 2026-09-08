#!/usr/bin/env bash
# 数据建模模块回归测试: 需要 app 在 8080 运行且 CUBE_ENABLE=true、Cube 容器就绪、
# mock ERP (erp_test 连接) 已建。用法:
#   EMAIL=xxx PASS=xxx ./deploy/cube/test-semantic.sh
set -euo pipefail
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
PROJECT_ROOT="$( cd "$SCRIPT_DIR/../.." && pwd )"
WS_DIR="${CUBE_WS:-$PROJECT_ROOT/deploy/cube/workspace}"
B="http://127.0.0.1:8080/api/v1/semantic"
EMAIL="${EMAIL:?need EMAIL}"; PASS="${PASS:?need PASS}"

TOKEN=$(curl -s -X POST http://127.0.0.1:8080/api/v1/auth/login -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL\",\"password\":\"$PASS\"}" | python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))")
H1="Authorization: Bearer $TOKEN"; H2="X-Tenant-ID: 10001"; H3="Content-Type: application/json"

pass=0; fail=0
check() { local name="$1" cond="$2"; if eval "$cond"; then echo "PASS  $name"; pass=$((pass+1)); else echo "FAIL  $name"; fail=$((fail+1)); fi; }

# 1 模块健康
check "info enabled+cube_ready" \
  "[ \"\$(curl -s $B/info -H \"$H1\" -H \"$H2\" | python3 -c 'import sys,json; d=json.load(sys.stdin); print(d[\"enabled\"] and d[\"cube_ready\"])')\" = 'True' ]"

# 2 引导类型
check "guided types 4" \
  "[ \$(curl -s $B/info -H \"$H1\" -H \"$H2\" | python3 -c 'import sys,json; print(len(json.load(sys.stdin)[\"guided_types\"]))') -eq 4 ]"

# 3 连接列表非空且无密码下发
CONN=$(curl -s $B/connections -H "$H1" -H "$H2")
check "connections non-empty" "[ \$(echo '$CONN' | python3 -c 'import sys,json; print(len(json.load(sys.stdin)[\"connections\"]))') -gt 0 ]"
check "no password leak" "[ \$(echo '$CONN' | grep -c '\"password\"') -eq 0 ]"

# 4 模型 meta 召回 (当前用户可见)
META=$(curl -s $B/meta -H "$H1" -H "$H2")
check "meta has visible models" "[ \$(echo '$META' | python3 -c 'import sys,json; print(len(json.load(sys.stdin)[\"cubes\"]))') -gt 0 ]"

# 5 已发布模型可预览
MODEL=$(echo "$META" | python3 -c "
import sys, json
cubes = json.load(sys.stdin)['cubes']
print([c for c in cubes if c['name']=='sn_list'][0]['name'] if any(c['name']=='sn_list' for c in cubes) else '')")
if [ -n "$MODEL" ]; then
  MID=$(curl -s $B/models -H "$H1" -H "$H2" | python3 -c "
import sys, json
models = json.load(sys.stdin)['models']
print([m for m in models if m['name']=='sn_list'][0]['id'])")
  ROWS=$(curl -s -X POST "$B/models/$MID/preview" -H "$H1" -H "$H2" -H "$H3" \
    -d '{"measures":["sn_list.count"],"limit":1}' | python3 -c 'import sys,json; print(len(json.load(sys.stdin).get("data",[])))')
  check "sn_list preview rows>=1" "[ $ROWS -ge 1 ]"
else
  fail=$((fail+1)); echo "FAIL  sn_list model missing (skipped preview)"
fi

# 6 datasources.yaml 与库同步 (erp_test 存在)
check "datasources.yaml has erp_test" \
  "grep -q erp_test \"$WS_DIR/datasources.yaml\""

# 7 审计有记录
check "audit non-empty" \
  "[ \$(curl -s $B/audit -H \"$H1\" -H \"$H2\" | python3 -c 'import sys,json; print(len(json.load(sys.stdin)[\"audit_logs\"]))') -gt 0 ]"

echo "-----------------------------"
echo "PASS=$pass FAIL=$fail"
[ $fail -eq 0 ]
