#!/bin/bash

# Agent 구성 기능 테스트 스크립트

set -e

echo "========================================="
echo "Agent 구성 기능 테스트"
echo "========================================="
echo ""

# 색상 정의
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 구성
API_BASE_URL="http://localhost:8080"
KB_ID="kb-00000001"  # 귀하의 지식 베이스 ID로 수정
TENANT_ID="1"

echo "구성 정보:"
echo "  API 주소: ${API_BASE_URL}"
echo "  지식 베이스 ID: ${KB_ID}"
echo "  테넌트 ID: ${TENANT_ID}"
echo ""

# 테스트 1: 현재 구성 가져오기
echo -e "${YELLOW}테스트 1: 현재 구성 가져오기${NC}"
echo "GET ${API_BASE_URL}/api/v1/initialization/config/${KB_ID}"
RESPONSE=$(curl -s -X GET "${API_BASE_URL}/api/v1/initialization/config/${KB_ID}")
echo "응답:"
echo "$RESPONSE" | jq '.data.agent' || echo "$RESPONSE"
echo ""

# 테스트 2: Agent 구성 저장
echo -e "${YELLOW}테스트 2: Agent 구성 저장${NC}"
echo "POST ${API_BASE_URL}/api/v1/initialization/initialize/${KB_ID}"

# 테스트 데이터 준비 (완전한 구성 포함 필요)
TEST_DATA='{
  "llm": {
    "source": "local",
    "modelName": "qwen3:0.6b",
    "baseUrl": "",
    "apiKey": ""
  },
  "embedding": {
    "source": "local",
    "modelName": "nomic-embed-text:latest",
    "baseUrl": "",
    "apiKey": "",
    "dimension": 768
  },
  "rerank": {
    "enabled": false
  },
  "multimodal": {
    "enabled": false
  },
  "documentSplitting": {
    "chunkSize": 512,
    "chunkOverlap": 100,
    "separators": ["\n\n", "\n", "。", "！", "？", ";", "；"]
  },
  "nodeExtract": {
    "enabled": false
  },
  "agent": {
    "enabled": true,
    "maxIterations": 8,
    "temperature": 0.8,
    "allowedTools": ["knowledge_search", "multi_kb_search", "list_knowledge_bases"]
  }
}'

RESPONSE=$(curl -s -X POST "${API_BASE_URL}/api/v1/initialization/initialize/${KB_ID}" \
  -H "Content-Type: application/json" \
  -d "$TEST_DATA")

if echo "$RESPONSE" | grep -q '"success":true'; then
  echo -e "${GREEN}✓ Agent 구성 저장 성공${NC}"
  echo "$RESPONSE" | jq '.' || echo "$RESPONSE"
else
  echo -e "${RED}✗ Agent 구성 저장 실패${NC}"
  echo "$RESPONSE"
fi
echo ""

# 데이터가 저장되었는지 확인하기 위해 잠시 대기
sleep 1

# 테스트 3: 구성이 저장되었는지 확인
echo -e "${YELLOW}테스트 3: 구성이 저장되었는지 확인${NC}"
echo "GET ${API_BASE_URL}/api/v1/initialization/config/${KB_ID}"
RESPONSE=$(curl -s -X GET "${API_BASE_URL}/api/v1/initialization/config/${KB_ID}")
AGENT_CONFIG=$(echo "$RESPONSE" | jq '.data.agent')

echo "Agent 구성:"
echo "$AGENT_CONFIG" | jq '.'

# 구성이 올바른지 확인
ENABLED=$(echo "$AGENT_CONFIG" | jq -r '.enabled')
MAX_ITER=$(echo "$AGENT_CONFIG" | jq -r '.maxIterations')
TEMP=$(echo "$AGENT_CONFIG" | jq -r '.temperature')

if [ "$ENABLED" == "true" ] && [ "$MAX_ITER" == "8" ] && [ "$TEMP" == "0.8" ]; then
  echo -e "${GREEN}✓ 구성 확인 성공 - 모든 값 정확함${NC}"
else
  echo -e "${RED}✗ 구성 확인 실패${NC}"
  echo "  enabled: $ENABLED (기대값: true)"
  echo "  maxIterations: $MAX_ITER (기대값: 8)"
  echo "  temperature: $TEMP (기대값: 0.8)"
fi
echo ""

# 테스트 4: Tenant API를 사용하여 구성 가져오기
echo -e "${YELLOW}테스트 4: Tenant API를 사용하여 구성 가져오기${NC}"
echo "GET ${API_BASE_URL}/api/v1/tenants/${TENANT_ID}/agent-config"
RESPONSE=$(curl -s -X GET "${API_BASE_URL}/api/v1/tenants/${TENANT_ID}/agent-config")
echo "응답:"
echo "$RESPONSE" | jq '.' || echo "$RESPONSE"
echo ""

# 테스트 5: 데이터베이스 확인 (접근 가능한 경우)
echo -e "${YELLOW}테스트 5: 데이터베이스 확인${NC}"
echo "힌트: 데이터를 확인하려면 다음 SQL 쿼리를 수동으로 실행하세요:"
echo ""
echo "MySQL:"
echo "  mysql -u root -p weknora -e \"SELECT id, agent_config FROM tenants WHERE id = ${TENANT_ID};\""
echo ""
echo "PostgreSQL:"
echo "  psql -U postgres -d weknora -c \"SELECT id, agent_config FROM tenants WHERE id = ${TENANT_ID};\""
echo ""

echo "========================================="
echo "테스트 완료!"
echo "========================================="
echo ""
echo "모든 테스트를 통과하면 Agent 구성 기능이 정상적으로 작동하는 것입니다."
echo "테스트에 실패한 경우 다음을 확인하세요:"
echo "  1. 백엔드 서비스가 실행 중인지"
echo "  2. 데이터베이스 마이그레이션이 실행되었는지"
echo "  3. 지식 베이스 ID가 올바른지"
echo ""
