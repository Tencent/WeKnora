# 内置模型管理指南

## 概述

内置模型是系统级别的模型配置，对所有租户可见，但敏感信息会被隐藏，且不可编辑或删除。内置模型通常用于提供系统默认的模型配置，确保所有租户都能使用统一的模型服务。

## 内置模型特性

- **所有租户可见**：内置模型对所有租户都可见，无需单独配置
- **安全保护**：内置模型的敏感信息（API Key、Base URL）会被隐藏，无法查看详情
- **只读保护**：内置模型不能被编辑或删除，只能设置为默认模型
- **统一管理**：由系统管理员统一维护，确保配置一致性和安全性

## 如何添加内置模型

内置模型需要通过数据库直接插入。以下是添加内置模型的步骤：

### 1. 准备模型数据

首先，确保你已经有了要设置为内置模型的模型配置信息，包括：
- 模型名称（name）
- 模型类型（type）：`KnowledgeQA`、`Embedding`、`Rerank` 或 `VLLM`
- 模型来源（source）：`local` 或 `remote`
- 模型参数（parameters）：包括 base_url、api_key、provider 等
- 租户ID（tenant_id）：建议使用小于10000的租户ID，避免冲突

**支持的服务商（provider）**：`generic`（自定义）、`openai`、`aliyun`、`zhipu`、`volcengine`、`hunyuan`、`deepseek`、`minimax`、`mimo`、`siliconflow`、`jina`、`openrouter`、`gemini`、`modelscope`、`moonshot`、`qianfan`、`qiniu`、`longcat`、`gpustack`

### 2. 执行 SQL 插入语句

使用以下 SQL 语句插入内置模型：

```sql
-- 示例：插入一个 LLM 内置模型
INSERT INTO models (
    id,
    tenant_id,
    name,
    type,
    source,
    description,
    parameters,
    is_default,
    status,
    is_builtin
) VALUES (
    'builtin-llm-001',                    -- 使用固定ID，建议使用 builtin- 前缀
    10000,                                -- 租户ID（使用第一个租户）
    'GPT-4',                              -- 模型名称
    'KnowledgeQA',                        -- 模型类型
    'remote',                             -- 模型来源
    '内置 LLM 模型',                       -- 描述
    '{"base_url": "https://api.openai.com/v1", "api_key": "sk-xxx", "provider": "openai"}'::jsonb,  -- 参数（JSON格式）
    false,                                -- 是否默认
    'active',                             -- 状态
    true                                  -- 标记为内置模型
) ON CONFLICT (id) DO NOTHING;

-- 示例：插入一个 Embedding 内置模型
INSERT INTO models (
    id,
    tenant_id,
    name,
    type,
    source,
    description,
    parameters,
    is_default,
    status,
    is_builtin
) VALUES (
    'builtin-embedding-001',
    10000,
    'text-embedding-ada-002',
    'Embedding',
    'remote',
    '内置 Embedding 模型',
    '{"base_url": "https://api.openai.com/v1", "api_key": "sk-xxx", "provider": "openai", "embedding_parameters": {"dimension": 1536, "truncate_prompt_tokens": 0}}'::jsonb,
    false,
    'active',
    true
) ON CONFLICT (id) DO NOTHING;

-- 示例：插入一个 ReRank 内置模型
INSERT INTO models (
    id,
    tenant_id,
    name,
    type,
    source,
    description,
    parameters,
    is_default,
    status,
    is_builtin
) VALUES (
    'builtin-rerank-001',
    10000,
    'bge-reranker-base',
    'Rerank',
    'remote',
    '内置 ReRank 模型',
    '{"base_url": "https://api.jina.ai/v1", "api_key": "jina-xxx", "provider": "jina"}'::jsonb,
    false,
    'active',
    true
) ON CONFLICT (id) DO NOTHING;

-- 示例：插入一个 VLLM 内置模型
INSERT INTO models (
    id,
    tenant_id,
    name,
    type,
    source,
    description,
    parameters,
    is_default,
    status,
    is_builtin
) VALUES (
    'builtin-vllm-001',
    10000,
    'gpt-4-vision',
    'VLLM',
    'remote',
    '内置 VLLM 模型',
    '{"base_url": "https://dashscope.aliyuncs.com/compatible-mode/v1", "api_key": "sk-xxx", "provider": "aliyun"}'::jsonb,
    false,
    'active',
    true
) ON CONFLICT (id) DO NOTHING;
```

### 3. 验证插入结果

执行以下 SQL 查询验证内置模型是否成功插入：

```sql
SELECT id, name, type, is_builtin, status 
FROM models 
WHERE is_builtin = true
ORDER BY type, created_at;
```

## 注意事项

1. **ID 命名规范**：建议使用 `builtin-{type}-{序号}` 的格式，例如 `builtin-llm-001`、`builtin-embedding-001`
2. **租户ID**：内置模型可以属于任意租户，但建议使用第一个租户ID（通常是 10000）
3. **参数格式**：`parameters` 字段必须是有效的 JSON 格式
4. **幂等性**：使用 `ON CONFLICT (id) DO NOTHING` 确保重复执行不会报错
5. **安全性**：内置模型的 API Key 和 Base URL 在前端会被自动隐藏，但数据库中的原始数据仍然存在，请妥善保管数据库访问权限

## 将现有模型设置为内置模型

如果你已经有一个模型，想将其设置为内置模型，可以使用 UPDATE 语句：

```sql
UPDATE models 
SET is_builtin = true 
WHERE id = '模型ID' AND name = '模型名称';
```

## 移除内置模型

如果需要移除内置模型标记（恢复为普通模型），执行：

```sql
UPDATE models 
SET is_builtin = false 
WHERE id = '模型ID';
```

注意：移除内置模型标记后，该模型将恢复为普通模型，可以被编辑和删除。

