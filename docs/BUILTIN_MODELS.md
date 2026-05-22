# 内置模型管理指南

## 概述

内置模型是系统级别的模型配置，对所有租户可见，但敏感信息会被隐藏，且不可编辑或删除。内置模型通常用于提供系统默认的模型配置，确保所有租户都能使用统一的模型服务。

## 内置模型特性

- **所有租户可见**：内置模型对所有租户都可见，无需单独配置
- **安全保护**：内置模型的敏感信息（API Key、Base URL）会被隐藏，无法查看详情
- **只读保护**：内置模型不能被编辑或删除，只能设置为默认模型
- **统一管理**：由系统管理员统一维护，确保配置一致性和安全性

## 如何添加内置模型

WeKnora 支持两种方式添加内置模型：**推荐**使用 YAML 声明式配置（自动幂等下发），保留 SQL 直插作为兼容路径。

### 方式一（推荐）：YAML 配置文件

#### 文件位置

默认路径是 `config/builtin_models.yaml`（与 `config.yaml`、`builtin_agents.yaml` 同目录）。如需挂载到其他位置，设置环境变量 `BUILTIN_MODELS_CONFIG=/absolute/path/builtin_models.yaml` 覆盖。

文件不存在时启动期会跳过、不报错；解析失败仅记录 Warning、不影响主流程。每次应用启动会重新读取并按 `id` 字段 UPSERT 到 `models` 表（保留 `created_at`，刷新其他字段）。

#### Schema

```yaml
builtin_models:
  - id: <required, stable, UPSERT key>
    tenant_id: <int, default 10000>          # 与 tenants_id_seq 起点对齐
    name: <string>
    type: KnowledgeQA | Embedding | Rerank | VLLM | ASR
    source: <string, default "remote">       # local | remote | aliyun | ...
    description: <string, optional>
    is_default: <bool, default false>
    status: <string, default "active">
    parameters:
      base_url: <string>
      api_key: <string, supports ${ENV_VAR}>
      provider: <string>                     # openai | generic | moonshot | ...
      embedding_parameters:                  # 仅 Embedding 类型
        dimension: <int>
        truncate_prompt_tokens: <int>
```

#### 完整示例

```yaml
builtin_models:
  - id: builtin-openai-chat
    name: gpt-4o-mini
    type: KnowledgeQA
    source: remote
    description: OpenAI 默认对话模型
    is_default: false
    parameters:
      base_url: https://api.openai.com/v1
      api_key: ${OPENAI_API_KEY}
      provider: openai

  - id: builtin-openai-embeddings
    name: text-embedding-3-small
    type: Embedding
    source: remote
    parameters:
      base_url: https://api.openai.com/v1
      api_key: ${OPENAI_API_KEY}
      provider: openai
      embedding_parameters:
        dimension: 1536
        truncate_prompt_tokens: 0

  - id: builtin-rerank
    name: bge-reranker-v2-m3
    type: Rerank
    source: remote
    parameters:
      base_url: ${RERANK_BASE_URL}
      api_key: ${RERANK_API_KEY}
      provider: generic
```

#### `${ENV}` 插值

`api_key` / `base_url` / `name` 等任何**字符串**字段都可以引用环境变量：`${OPENAI_API_KEY}` 会在启动时被对应的 `os.Getenv("OPENAI_API_KEY")` 替换。

- 环境变量存在 → 替换为实际值
- 环境变量不存在 → **保留字面 `${OPENAI_API_KEY}`** 字符串（让 401 错误能直接看出来 env 没设，便于排查）
- 不支持 `${VAR:-default}` 这种 shell 扩展，行为与现有 `config.yaml` 的插值实现一致
- **非字符串字段不能 env 化**（如 `type`、`dimension`、`is_default`），因为它们必须按 YAML 的目标类型解析

#### env 变量怎么进入容器

`docker-compose.yml` 的 `app` 服务已经预置了：

```yaml
env_file:
  - path: .env
    required: false
```

意味着把变量值写到项目根目录的 `.env` 文件里，启动时自动透传到容器。**无需**在 `environment:` 块里逐个透传。`required: false` 保证 `.env` 不存在时容器仍可启动（适配上游 fresh clone 场景）。

仓库的 `.env.example` 顶部预留了 **Built-in Models** 注释段，列出 LLM / Embedding / Rerank 的参考变量名作为起点；复制 `.env.example` 为 `.env` 后取消注释并填值即可。变量名由 YAML 自行决定，参考段只是常见样板，不是保留字。

完整端到端示例：

`.env`
```bash
LLM_MODEL_NAME=gpt-4o-mini
LLM_BASE_URL=https://api.openai.com/v1
LLM_API_KEY=sk-...
LLM_PROVIDER=openai
```

`config/builtin_models.yaml`
```yaml
builtin_models:
  - id: builtin-llm-default
    type: KnowledgeQA
    is_default: true
    name: ${LLM_MODEL_NAME}
    parameters:
      base_url: ${LLM_BASE_URL}
      api_key: ${LLM_API_KEY}
      provider: ${LLM_PROVIDER}
```

启动：
```bash
docker compose up -d
```

#### 启动后验证

```bash
docker compose logs app | grep -E 'Built-in models? config'
```

会看到类似：

```
Built-in model upserted: id=builtin-openai-chat name=gpt-4o-mini type=KnowledgeQA
Built-in model upserted: id=builtin-openai-embeddings name=text-embedding-3-small type=Embedding
Built-in models config applied: 2 entries from /app/config/builtin_models.yaml.
```

#### Docker 部署

在 `docker-compose.yml` 的 `app` 服务 `volumes` 块挂载文件：

```yaml
services:
  app:
    volumes:
      - ./config/builtin_models.yaml:/app/config/builtin_models.yaml:ro
```

仓库提供了 `config/builtin_models.yaml.example` 作为起点，复制为 `config/builtin_models.yaml` 后按需修改。

### 方式二：直接 SQL 插入

支持的 provider：`generic`（自定义）、`openai`、`aliyun`、`zhipu`、`volcengine`、`hunyuan`、`deepseek`、`minimax`、`mimo`、`siliconflow`、`jina`、`openrouter`、`gemini`、`modelscope`、`moonshot`、`qianfan`、`qiniu`、`longcat`、`gpustack`

```sql
-- 示例：LLM 内置模型
INSERT INTO models (
    id, tenant_id, name, type, source, description,
    parameters, is_default, status, is_builtin
) VALUES (
    'builtin-llm-001',
    10000,
    'gpt-4o-mini',
    'KnowledgeQA',
    'remote',
    '系统内置 LLM 模型',
    '{"base_url": "https://api.openai.com/v1", "api_key": "sk-xxx", "provider": "openai"}'::jsonb,
    false,
    'active',
    true
) ON CONFLICT (id) DO NOTHING;

-- Embedding
INSERT INTO models (
    id, tenant_id, name, type, source, description,
    parameters, is_default, status, is_builtin
) VALUES (
    'builtin-embedding-001',
    10000,
    'text-embedding-3-small',
    'Embedding',
    'remote',
    '系统内置 Embedding 模型',
    '{"base_url": "https://api.openai.com/v1", "api_key": "sk-xxx", "provider": "openai", "embedding_parameters": {"dimension": 1536, "truncate_prompt_tokens": 0}}'::jsonb,
    false,
    'active',
    true
) ON CONFLICT (id) DO NOTHING;

-- Rerank
INSERT INTO models (
    id, tenant_id, name, type, source, description,
    parameters, is_default, status, is_builtin
) VALUES (
    'builtin-rerank-001',
    10000,
    'bge-reranker-v2-m3',
    'Rerank',
    'remote',
    '系统内置 Rerank 模型',
    '{"base_url": "https://api.jina.ai/v1", "api_key": "jina-xxx", "provider": "jina"}'::jsonb,
    false,
    'active',
    true
) ON CONFLICT (id) DO NOTHING;
```

### 验证插入结果

```sql
SELECT id, name, type, is_builtin, status
FROM models
WHERE is_builtin = true
ORDER BY type, created_at;
```

## 将现有模型设置为内置模型

如果你已经手工创建了一个普通模型，想把它升级为内置模型：

```sql
UPDATE models
SET is_builtin = true
WHERE id = '模型ID';
```

## 移除内置模型

YAML 配置删除条目后**不会**自动清理 DB 里的对应行（避免误删 ops 手工维护的内置模型）。如需删除：

```sql
-- 取消 builtin 标记，恢复为普通模型
UPDATE models SET is_builtin = false WHERE id = '模型ID';

-- 或直接删除
DELETE FROM models WHERE id = '模型ID';
```

## 注意事项

1. **ID 命名规范**：建议使用 `builtin-{type}-{slug}` 的格式，例如 `builtin-openai-chat`、`builtin-rerank`
2. **租户ID**：内置模型可以属于任意租户，默认 `10000`（与 `tenants_id_seq` 起点一致）
3. **YAML 与 SQL 并存**：两种方式可以同时使用，YAML 不会覆盖通过 SQL 插入但未在 YAML 出现的 builtin 行
4. **重启即生效**：修改 YAML 后 `docker compose restart app` 即可让新配置生效
5. **加密**：API Key 在 `parameters` JSONB 中以加密形式存储（若 `SYSTEM_AES_KEY` 已配置），未配置时降级为明文兼容路径
6. **安全性**：前端会自动隐藏内置模型的 API Key 和 Base URL，但数据库中的原始数据仍然存在，请妥善保管数据库访问权限
