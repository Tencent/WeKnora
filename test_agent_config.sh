#!/bin/bash

# Agent 配置功能测试脚本

set -e

echo "========================================="
echo "Agent 配置功能测试"
echo "========================================="
echo ""

# 颜色定义
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 配置
API_BASE_URL="http://localhost:8080"
KB_ID="kb-00000001"  # 修改为你的知识库ID
TENANT_ID="1"

echo "配置信息："
echo "  API地址: ${API_BASE_URL}"
echo "  知识库ID: ${KB_ID}"
echo "  空间ID: ${TENANT_ID}"
echo ""

# 测试 1：获取当前配置
echo -e "${YELLOW}测试 1: 获取当前配置${NC}"
echo "GET ${API_BASE_URL}/api/v1/initialization/config/${KB_ID}"
RESPONSE=$(curl -s -X GET "${API_BASE_URL}/api/v1/initialization/config/${KB_ID}")
echo "响应:"
echo "$RESPONSE" | jq '.data.agent' || echo "$RESPONSE"
echo ""

# 测试 2：保存 Agent 配置
echo -e "${YELLOW}测试 2: 保存 Agent 配置${NC}"
echo "POST ${API_BASE_URL}/api/v1/initialization/initialize/${KB_ID}"

# 准备测试数据（需要包含完整的配置）
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
  echo -e "${GREEN}✓ Agent 配置保存成功${NC}"
  echo "$RESPONSE" | jq '.' || echo "$RESPONSE"
else
  echo -e "${RED}✗ Agent 配置保存失败${NC}"
  echo "$RESPONSE"
fi
echo ""

# 等待一下，确保数据已保存
sleep 1

# 测试 3：验证配置已保存
echo -e "${YELLOW}测试 3: 验证配置已保存${NC}"
echo "GET ${API_BASE_URL}/api/v1/initialization/config/${KB_ID}"
RESPONSE=$(curl -s -X GET "${API_BASE_URL}/api/v1/initialization/config/${KB_ID}")
AGENT_CONFIG=$(echo "$RESPONSE" | jq '.data.agent')

echo "Agent 配置:"
echo "$AGENT_CONFIG" | jq '.'

# 检查配置是否正确
ENABLED=$(echo "$AGENT_CONFIG" | jq -r '.enabled')
MAX_ITER=$(echo "$AGENT_CONFIG" | jq -r '.maxIterations')
TEMP=$(echo "$AGENT_CONFIG" | jq -r '.temperature')

if [ "$ENABLED" == "true" ] && [ "$MAX_ITER" == "8" ] && [ "$TEMP" == "0.8" ]; then
  echo -e "${GREEN}✓ 配置验证成功 - 所有值正确${NC}"
else
  echo -e "${RED}✗ 配置验证失败${NC}"
  echo "  enabled: $ENABLED (期望: true)"
  echo "  maxIterations: $MAX_ITER (期望: 8)"
  echo "  temperature: $TEMP (期望: 0.8)"
fi
echo ""

# 测试 4：使用 Tenant API 获取配置
echo -e "${YELLOW}测试 4: 使用 Tenant API 获取配置${NC}"
echo "GET ${API_BASE_URL}/api/v1/tenants/${TENANT_ID}/agent-config"
RESPONSE=$(curl -s -X GET "${API_BASE_URL}/api/v1/tenants/${TENANT_ID}/agent-config")
echo "响应:"
echo "$RESPONSE" | jq '.' || echo "$RESPONSE"
echo ""

# 测试 5：数据库验证（如果可以访问）
echo -e "${YELLOW}测试 5: 数据库验证${NC}"
echo "提示: 请手动运行以下 SQL 查询验证数据："
echo ""
echo "MySQL:"
echo "  mysql -u root -p weknora -e \"SELECT id, agent_config FROM tenants WHERE id = ${TENANT_ID};\""
echo ""
echo "PostgreSQL:"
echo "  psql -U postgres -d weknora -c \"SELECT id, agent_config FROM tenants WHERE id = ${TENANT_ID};\""
echo ""

echo "========================================="
echo "测试完成！"
echo "========================================="
echo ""
echo "如果所有测试都通过，Agent 配置功能已正常工作。"
echo "如果有测试失败，请检查："
echo "  1. 后端服务是否正在运行"
echo "  2. 数据库迁移是否已执行"
echo "  3. 知识库ID是否正确"
echo ""

